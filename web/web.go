// Package web serves the built React app from app/dist. The binary embeds it,
// so a deploy is one image and the SPA needs no separate container or CDN.
//
// The orchestrator must survive a missing frontend: the node claims work over
// the same process, so an unbuilt UI answers 503 on HTML routes and takes
// nothing else down with it.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:app/dist
var dist embed.FS

// Handler serves the SPA at "/": real files when they exist, index.html for any
// other path so client routing survives a reload.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "app/dist")
	if err != nil {
		panic(err) // an embedded tree that cannot be addressed is a build defect
	}
	return handlerFor(sub)
}

// handlerFor is the handler with its filesystem injected, so the tests can prove
// the fallback, the 404 and the not-built branch without building a frontend.
func handlerFor(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	index, indexErr := fs.ReadFile(fsys, "index.html")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))

		// The root is the app; anything with a file extension is an asset and is
		// never answered with index.html — a missing bundle must fail loudly.
		if clean == "." || clean == "/" || strings.HasPrefix(clean, "..") {
			serveIndex(w, index, indexErr)
			return
		}
		if f, err := fsys.Open(clean); err == nil {
			_ = f.Close()
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(clean) != "" {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, index, indexErr)
	})
}

func serveIndex(w http.ResponseWriter, index []byte, indexErr error) {
	if indexErr != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("frontend not built: run `make web`\n"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html names hashed assets, so it must not be cached; the assets may be.
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}

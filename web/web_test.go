package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const indexHTML = `<!doctype html><html><body><div id="root"></div></body></html>`

func builtFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(indexHTML)},
		"favicon.svg":   &fstest.MapFile{Data: []byte("<svg/>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
}

func TestServesIndexForClientRoutes(t *testing.T) {
	h := handlerFor(builtFS())
	for _, p := range []string{"/", "/queue", "/files/12", "/runs", "/settings", "/queue/anything"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200", p, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("%s: body is not index.html", p)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Fatalf("%s: Cache-Control %q, want no-cache", p, got)
		}
	}
}

func TestServesAssetsWithLongCache(t *testing.T) {
	h := handlerFor(builtFS())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("asset: status %d body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("asset Cache-Control %q, want immutable", got)
	}
}

// A missing bundle is a 404, not the app: index.html served for a .js request
// turns a broken deploy into a syntax error in the browser.
func TestMissingAssetIs404(t *testing.T) {
	h := handlerFor(builtFS())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/index-old.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `id="root"`) {
		t.Fatal("missing asset got index.html")
	}
}

// Without a frontend the HTML routes answer 503 with one sentence; the API is a
// different package and keeps working, which is the point of the branch.
func TestUnbuiltFrontendIs503(t *testing.T) {
	h := handlerFor(fstest.MapFS{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/queue", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "make web") {
		t.Fatalf("body %q does not name the fix", rec.Body.String())
	}
}

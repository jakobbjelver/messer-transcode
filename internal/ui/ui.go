// Package ui renders the film-slate pages and serves the static assets.
//
// Templates are parsed once at start: a broken template is a startup failure,
// not a 500 at three in the morning.
package ui

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"time"
)

//go:embed templates/*.html
var templates embed.FS

//go:embed static
var static embed.FS

var funcs = template.FuncMap{
	"bytes": humanBytes,
	"gb": func(n int64) string {
		return fmt.Sprintf("%.1f", float64(n)/1e9)
	},
	"clock": func(unix int64) string {
		if unix == 0 {
			return "—"
		}
		return time.Unix(unix, 0).Format("15:04")
	},
	"base": filepath.Base,
	"dur":  humanDuration,
	"pct": func(v float64) string {
		return fmt.Sprintf("%.0f", v)
	},
}

var pages = map[string]*template.Template{}

func init() {
	for _, name := range []string{"dashboard", "queue", "file", "settings"} {
		pages[name] = template.Must(
			template.New("base.html").Funcs(funcs).
				ParseFS(templates, "templates/base.html", "templates/"+name+".html"),
		)
	}
}

// Render writes a page. Errors happen before any bytes are written for the
// usual case, so a failure here is a 500 the caller can still report.
func Render(w io.Writer, page string, data any) error {
	t, ok := pages[page]
	if !ok {
		return fmt.Errorf("ui: unknown page %q", page)
	}
	return t.ExecuteTemplate(w, "base.html", data)
}

// Static serves the stylesheet and icon.
func Static() http.Handler {
	sub, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // an embedded tree that cannot be addressed is a build defect
	}
	return http.FileServerFS(sub)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1e12:
		return fmt.Sprintf("%.2f TB", float64(n)/1e12)
	case n >= 1e9:
		return fmt.Sprintf("%.2f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f KB", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func humanDuration(seconds float64) string {
	if seconds <= 0 {
		return "—"
	}
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %02ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

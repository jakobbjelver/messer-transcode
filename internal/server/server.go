// Package server is the orchestrator's HTTP surface: the read API, the embedded
// React app, and the endpoints a node claims work through.
//
// The server never touches the media volume. Everything it knows was reported
// by a node, which is the only reason it can live in a container on a machine
// that cannot see /Volumes at all.
package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/web"
)

// Server holds what every handler needs. No mutable request state lives here;
// progress is in the database so that any process can read it.
type Server struct {
	cfg     *config.Config
	st      *store.Store
	cfgPath string
	dbPath  string
	started time.Time
	log     *slog.Logger
}

// New wires a server. The logger may be nil, which silences request logs.
func New(cfg *config.Config, st *store.Store, cfgPath, dbPath string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Server{cfg: cfg, st: st, cfgPath: cfgPath, dbPath: dbPath, started: time.Now(), log: log}
}

// Handler returns the full route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /api/v1/health", s.apiHealth)
	mux.HandleFunc("GET /api/v1/runs", s.apiRuns)
	mux.HandleFunc("GET /api/v1/status", s.apiStatus)
	mux.HandleFunc("GET /api/v1/live", s.apiLive)
	mux.HandleFunc("GET /api/v1/files", s.apiFiles)
	mux.HandleFunc("GET /api/v1/files/{id}", s.apiFile)
	mux.HandleFunc("GET /api/v1/events", s.apiEvents)
	mux.HandleFunc("GET /api/v1/config", s.apiConfig)
	mux.HandleFunc("GET /api/v1/stream", s.apiStream)

	mux.HandleFunc("POST /api/v1/node/register", s.nodeGuard(s.nodeRegister))
	mux.HandleFunc("POST /api/v1/node/claim", s.nodeGuard(s.nodeClaim))
	mux.HandleFunc("POST /api/v1/node/event", s.nodeGuard(s.nodeEvent))
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/heartbeat", s.nodeGuard(s.nodeHeartbeat))
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/state", s.nodeGuard(s.nodeState))

	// The SPA owns every path that is not an API route. An unknown /api path must
	// answer JSON, never index.html: a client parsing HTML as JSON reports the
	// wrong fault, and a missing endpoint that looks like a page hides itself.
	spa := web.Handler()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.fail(w, http.StatusNotFound, "E_NO_ROUTE", "no such endpoint")
			return
		}
		spa.ServeHTTP(w, r)
	})

	return s.recoverer(s.logger(mux))
}

// ── middleware ────────────────────────────────────────────────────────────

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic", "path", r.URL.Path, "panic", v)
				s.fail(w, http.StatusInternalServerError, "E_INTERNAL", "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", rec.code, "ms", time.Since(start).Milliseconds())
	})
}

type recorder struct {
	http.ResponseWriter
	code int
}

func (r *recorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer so SSE works through the recorder.
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// ── errors ────────────────────────────────────────────────────────────────

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (s *Server) fail(w http.ResponseWriter, status int, code, message string) {
	var body apiError
	body.Error.Code = code
	body.Error.Message = message
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

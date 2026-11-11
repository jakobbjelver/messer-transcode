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
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/web"
)

// Server holds what every handler needs. No mutable request state lives here;
// progress is in the database so that any process can read it.
//
// The config is the one mutable field: it is swapped when a save or a hand edit
// changes it, so every read goes through config() and every write through
// install(). A reader must never see a half-applied config.
type Server struct {
	cfgMu   sync.RWMutex
	cfg     *config.Config
	cfgSHA  string
	diskSHA string
	diskErr string

	st      *store.Store
	cfgPath string
	dbPath  string
	started time.Time
	log     *slog.Logger
}

// New wires a server. The logger may be nil, which silences request logs.
//
// cfgSHA is the fingerprint of the config as loaded, so the watcher and the
// dirty indicator know what "unchanged" means from the first request.
func New(cfg *config.Config, st *store.Store, cfgPath, dbPath string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{cfg: cfg, st: st, cfgPath: cfgPath, dbPath: dbPath, started: time.Now(), log: log}
	if raw, err := os.ReadFile(cfgPath); err == nil {
		if sha, err := config.Fingerprint(raw); err == nil {
			s.cfgSHA, s.diskSHA = sha, sha
		}
	}
	return s
}

// Handler returns the full route table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /api/v1/health", s.apiHealth)
	mux.HandleFunc("GET /api/v1/runs", s.apiRuns)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.apiRun)
	mux.HandleFunc("GET /api/v1/config", s.apiConfig)
	mux.HandleFunc("POST /api/v1/config", s.apiConfigSave)
	mux.HandleFunc("POST /api/v1/config/dry-run", s.apiConfigDryRun)
	mux.HandleFunc("POST /api/v1/config/adopt", s.apiConfigAdopt)
	mux.HandleFunc("POST /api/v1/config/revert/{rev}", s.apiConfigRevert)
	mux.HandleFunc("GET /api/v1/config/history", s.apiConfigHistory)
	mux.HandleFunc("GET /api/v1/config/diff", s.apiConfigDiff)
	mux.HandleFunc("GET /api/v1/config/revisions/{rev}", s.apiConfigRevision)
	mux.HandleFunc("GET /api/v1/status", s.apiStatus)
	mux.HandleFunc("GET /api/v1/live", s.apiLive)
	mux.HandleFunc("GET /api/v1/files", s.apiFiles)
	mux.HandleFunc("GET /api/v1/files/{id}", s.apiFile)
	mux.HandleFunc("GET /api/v1/queue", s.apiQueue)
	mux.HandleFunc("GET /api/v1/events", s.apiEvents)
	mux.HandleFunc("GET /api/v1/stream", s.apiStream)

	// Steering. Every one of these writes; the read side is /api/v1/control.
	mux.HandleFunc("GET /api/v1/control", s.apiControl)
	mux.HandleFunc("POST /api/v1/control/pause", s.apiPause)
	mux.HandleFunc("POST /api/v1/control/window", s.apiWindow)
	mux.HandleFunc("POST /api/v1/files/{id}/retry", s.apiFileRetry)
	mux.HandleFunc("POST /api/v1/files/{id}/skip", s.apiFileSkip)
	mux.HandleFunc("POST /api/v1/files/{id}/priority", s.apiFilePriority)
	mux.HandleFunc("POST /api/v1/queue/act", s.apiQueueAct)

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

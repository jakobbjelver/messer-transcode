// Package server is the orchestrator's HTTP surface: the read API, the
// film-slate pages, and the endpoints a node claims work through.
//
// The server never touches the media volume. Everything it knows was reported
// by a node, which is the only reason it can live in a container on a machine
// that cannot see /Volumes at all.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/planner"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/ui"
	"github.com/jakobbjelver/messer-transcode/internal/version"
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

	mux.Handle("GET /static/", http.StripPrefix("/static/", ui.Static()))

	mux.HandleFunc("GET /{$}", s.overview)
	mux.HandleFunc("GET /queue", s.queuePage)
	mux.HandleFunc("GET /files/{id}", s.filePage)
	mux.HandleFunc("GET /settings", s.settingsPage)
	mux.HandleFunc("GET /events", s.sse)
	mux.HandleFunc("GET /healthz", s.healthz)

	mux.HandleFunc("GET /api/v1/health", s.apiHealth)
	mux.HandleFunc("GET /api/v1/status", s.apiStatus)
	mux.HandleFunc("GET /api/v1/live", s.apiLive)
	mux.HandleFunc("GET /api/v1/files", s.apiFiles)
	mux.HandleFunc("GET /api/v1/files/{id}", s.apiFile)
	mux.HandleFunc("GET /api/v1/events", s.apiEvents)

	mux.HandleFunc("POST /api/v1/node/register", s.nodeGuard(s.nodeRegister))
	mux.HandleFunc("POST /api/v1/node/claim", s.nodeGuard(s.nodeClaim))
	mux.HandleFunc("POST /api/v1/node/event", s.nodeGuard(s.nodeEvent))
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/heartbeat", s.nodeGuard(s.nodeHeartbeat))
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/state", s.nodeGuard(s.nodeState))

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

// ── view models ───────────────────────────────────────────────────────────

type base struct {
	Title       string
	Page        string
	Reel        string
	Clock       string
	Window      string
	StatusWord  string
	StatusClass string
	JobID       int64
}

type jobView struct {
	Path        string
	Stage       string
	Speed       string
	Target      string
	Percent     float64
	PercentText string
}

type dashboardView struct {
	base
	Dashboard  store.Dashboard
	Events     []store.EventRow
	Strip      []string
	Job        *jobView
	Idle       string
	Done       int
	Encodable  int
	WindowNote string
}

// ── pages ─────────────────────────────────────────────────────────────────

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := s.st.Snapshot(ctx)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	events, err := s.st.RecentEvents(ctx, 14)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	live, hasLive, _ := s.st.Live(ctx)

	encodable := snap.Decisions["encode"] + snap.Decisions["remux"]
	done := snap.States["done"]

	v := dashboardView{
		base:       s.base("Overview", "overview", live, hasLive),
		Dashboard:  snap,
		Events:     events,
		Strip:      strip(done, encodable, hasLive),
		Done:       done,
		Encodable:  encodable,
		WindowNote: s.windowNote(),
	}
	if hasLive {
		v.Job = jobFrom(live)
	} else {
		v.Idle = s.idleNote()
	}
	s.render(w, "dashboard", v)
}

func (s *Server) queuePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	state, decision := q.Get("state"), q.Get("decision")
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	const perPage = 100

	// Ask for one more than a page: its presence is the answer to "is there a
	// next page", which avoids a second count query over 3,000 rows.
	rows, err := s.st.Queue(ctx, state, decision, perPage+1, (page-1)*perPage)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	hasMore := len(rows) > perPage
	if hasMore {
		rows = rows[:perPage]
	}
	snap, err := s.st.Snapshot(ctx)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	live, hasLive, _ := s.st.Live(ctx)

	type filter struct {
		Label, Href string
		Active      bool
		Count       int
	}
	filters := []filter{
		{Label: "All", Href: "/queue", Active: state == "" && decision == "", Count: snap.TotalFiles},
		{Label: "To encode", Href: "/queue?decision=encode", Active: decision == "encode", Count: snap.Decisions["encode"]},
		{Label: "To remux", Href: "/queue?decision=remux", Active: decision == "remux", Count: snap.Decisions["remux"]},
		{Label: "Left alone", Href: "/queue?decision=skip", Active: decision == "skip", Count: snap.Decisions["skip"]},
		{Label: "Failed", Href: "/queue?state=failed", Active: state == "failed", Count: snap.Failures},
	}

	type queueView struct {
		base
		Rows    []store.SummaryRow
		Filters []filter
		PageNo  int
		Prev    string
		Next    string
	}
	v := queueView{
		base:    s.base("Queue", "queue", live, hasLive),
		Rows:    rows,
		Filters: filters,
		PageNo:  page,
	}
	if state == "failed" {
		v.Page = "failed"
	}
	if page > 1 {
		v.Prev = pageURL(state, decision, page-1)
	}
	if hasMore {
		v.Next = pageURL(state, decision, page+1)
	}
	s.render(w, "queue", v)
}

func pageURL(state, decision string, page int) string {
	parts := []string{}
	if state != "" {
		parts = append(parts, "state="+state)
	}
	if decision != "" {
		parts = append(parts, "decision="+decision)
	}
	parts = append(parts, "page="+strconv.Itoa(page))
	return "/queue?" + strings.Join(parts, "&")
}

func (s *Server) filePage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_ID", "not a file id")
		return
	}
	ctx := r.Context()
	f, err := s.st.File(ctx, id)
	if err != nil {
		s.fail(w, http.StatusNotFound, "E_NOT_FOUND", "no such file")
		return
	}
	live, hasLive, _ := s.st.Live(ctx)

	type fileView struct {
		base
		File     store.FileDetail
		Expected float64
	}
	s.render(w, "file", fileView{
		base:     s.base(f.Path, "queue", live, hasLive),
		File:     f,
		Expected: planner.ExpectedVMAF(s.cfg, f.Codec, f.Height),
	})
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	live, hasLive, _ := s.st.Live(ctx(r))
	redacted := *s.cfg
	redacted.Server.Token = ""

	b, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_CONFIG", err.Error())
		return
	}

	type settingsView struct {
		base
		Version    string
		Commit     string
		BuildDate  string
		Uptime     string
		DBPath     string
		DBSize     int64
		ConfigPath string
		ConfigJSON string
	}
	var size int64
	if fi, err := osStat(s.dbPath); err == nil {
		size = fi
	}
	s.render(w, "settings", settingsView{
		base:       s.base("Settings", "settings", live, hasLive),
		Version:    version.Version,
		Commit:     version.Commit,
		BuildDate:  version.BuildDate,
		Uptime:     time.Since(s.started).Round(time.Second).String(),
		DBPath:     s.dbPath,
		DBSize:     size,
		ConfigPath: s.cfgPath,
		ConfigJSON: string(b),
	})
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ui.Render(w, page, data); err != nil {
		s.log.Error("render", "page", page, "err", err)
		s.fail(w, http.StatusInternalServerError, "E_RENDER", err.Error())
	}
}

// ── live stream ───────────────────────────────────────────────────────────

// sse pushes a small map once a second, and only when it changed. The page
// patches a few figures and ignores everything else.
func (s *Server) sse(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		s.fail(w, http.StatusInternalServerError, "E_NO_STREAM", "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	last := ""
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			b, err := json.Marshal(s.pulse(r.Context()))
			if err != nil {
				return
			}
			if string(b) == last {
				continue
			}
			last = string(b)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

func (s *Server) pulse(ctx context.Context) map[string]string {
	out := map[string]string{"job": "", "pct": "0", "pct-text": "—"}
	snap, err := s.st.Snapshot(ctx)
	if err == nil {
		out["reclaimed"] = fmt.Sprintf("%.1f", float64(snap.ReclaimedBytes)/1e9)
		out["encode"] = strconv.Itoa(snap.Decisions["encode"])
	}
	live, ok, _ := s.st.Live(ctx)
	if ok {
		out["job"] = strconv.FormatInt(live.FileID, 10)
		out["pct"] = fmt.Sprintf("%.0f", live.Fraction*100)
		out["pct-text"] = fmt.Sprintf("%.0f%%", live.Fraction*100)
		out["state"] = live.Stage
	}
	return out
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// ── helpers ───────────────────────────────────────────────────────────────

func (s *Server) base(title, page string, live store.Live, running bool) base {
	open, window, _ := s.windowState()
	word, class := "WINDOW SHUT", "st-skipped"
	if open {
		word, class = "WINDOW OPEN", "st-done"
	}
	if running {
		word, class = strings.ToUpper(live.Stage), "st-encoding"
	}
	return base{
		Title:       title,
		Page:        page,
		Reel:        fmt.Sprintf("%02d", isoWeek(time.Now())),
		Clock:       time.Now().Format("Mon 02 Jan · 15:04"),
		Window:      window,
		StatusWord:  word,
		StatusClass: class,
		JobID:       live.FileID,
	}
}

func jobFrom(l store.Live) *jobView {
	return &jobView{
		Path:        l.Path,
		Stage:       l.Stage,
		Speed:       fmt.Sprintf("%.1f×", l.Speed),
		Target:      fmt.Sprintf("%d kbps", l.Target),
		Percent:     l.Fraction * 100,
		PercentText: fmt.Sprintf("%.0f%%", l.Fraction*100),
	}
}

// strip is the queue as one tick per percent, so a glance shows how far the
// library has come without reading a number.
func strip(done, encodable int, running bool) []string {
	const ticks = 60
	if encodable <= 0 {
		return make([]string, ticks)
	}
	frac := math.Min(1, float64(done)/float64(encodable))
	filled := int(math.Round(frac * ticks))

	out := make([]string, ticks)
	for i := 0; i < ticks; i++ {
		switch {
		case i < filled:
			out[i] = "on"
		case i == filled && running:
			out[i] = "live"
		}
	}
	return out
}

func isoWeek(t time.Time) int {
	_, w := t.ISOWeek()
	return w
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

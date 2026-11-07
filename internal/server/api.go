package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/health"
	"github.com/jakobbjelver/messer-transcode/internal/report"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/version"
)

// ── read API ──────────────────────────────────────────────────────────────

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	snap, err := s.st.Snapshot(ctx)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	open, spec, next := s.windowState()
	live, hasLive, _ := s.st.Live(ctx)

	resp := map[string]any{
		"version": map[string]string{
			"version":    version.Version,
			"commit":     version.Commit,
			"build_date": version.BuildDate,
		},
		"uptime_seconds": int(time.Since(s.started).Seconds()),
		"window": map[string]any{
			"spec":    spec,
			"open":    open,
			"next_at": next,
		},
		"dashboard": snap,
	}
	if hasLive {
		resp["live"] = live
	} else {
		resp["live"] = nil
	}
	writeJSON(w, http.StatusOK, resp)
}

// apiHealth returns the same verdict object as `doctor`, plus the queue's view
// of the world. 503 when broken so a container healthcheck and a shell script
// both get it right without parsing.
// apiRuns reports what the last few runs did. "Run" means one occurrence of the
// configured window; the hours come from the config, so a window moved to the
// middle of the day reports identically.
func (s *Server) apiRuns(w http.ResponseWriter, r *http.Request) {
	last := 7
	if v := r.URL.Query().Get("last"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 90 {
			last = n
		}
	}
	runs, err := report.Collect(r.Context(), s.st, s.cfg.Schedule, time.Now(), last)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schedule": s.cfg.Schedule,
		"runs":     runs,
	})
}

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	rep := health.Collect(r.Context(), health.In{
		Config:     s.cfg,
		ConfigPath: s.cfgPath,
		Store:      s.st,
		DBPath:     s.dbPath,
		Role:       health.RoleServer,
	})
	status := http.StatusOK
	if rep.Status == health.Broken {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, rep)
}

func (s *Server) apiLive(w http.ResponseWriter, r *http.Request) {
	live, ok, err := s.st.Live(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"live": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"live": live})
}

func (s *Server) apiFiles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	rows, err := s.st.Queue(r.Context(), q.Get("state"), q.Get("decision"), limit, offset)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if rows == nil {
		rows = []store.SummaryRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"files":  rows,
		"limit":  limit,
		"offset": offset,
		"count":  len(rows),
	})
}

func (s *Server) apiFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_ID", "not a file id")
		return
	}
	f, err := s.st.File(r.Context(), id)
	if err != nil {
		s.fail(w, http.StatusNotFound, "E_NOT_FOUND", "no such file")
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	events, err := s.st.RecentEvents(r.Context(), limit)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if events == nil {
		events = []store.EventRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// ── node API ──────────────────────────────────────────────────────────────
//
// Provisional until M4: the node contract here covers claim, heartbeat and
// result, which is enough to move a job end to end. Measurements (VMAF, byte
// counts, verdict detail) join the result body when the node agent exists, and
// that is a breaking change to a private endpoint rather than a migration.

func (s *Server) nodeGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Server.Token == "" {
			s.fail(w, http.StatusServiceUnavailable, "E_NO_TOKEN",
				"server.token is unset, so the node API is disabled")
			return
		}
		got := r.Header.Get("X-Node-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Server.Token)) != 1 {
			s.fail(w, http.StatusUnauthorized, "E_NODE_AUTH", "bad or missing X-Node-Token")
			return
		}
		next(w, r)
	}
}

func (s *Server) nodeRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Node string `json:"node"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	writeJSON(w, http.StatusOK, map[string]any{
		"server_time":    time.Now().Unix(),
		"config_version": s.cfg.Version,
		"node":           req.Node,
	})
}

func (s *Server) nodeClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Node string `json:"node"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if ok, reason := s.claimsAllowed(time.Now()); !ok {
		w.Header().Set("X-Claim-Status", reason)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Give back anything whose node stopped reporting. This is the only place
	// it matters, so it costs nothing when nobody is asking.
	if n, err := s.st.ReapExpired(r.Context(), leaseDuration); err == nil && n > 0 {
		s.log.Warn("requeued abandoned jobs", "count", n)
	}

	work, err := s.st.ClaimWork(r.Context(), 1)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	w.Header().Set("X-Claim-Status", RefusedEmpty)
	if len(work) == 0 {
		w.WriteHeader(http.StatusNoContent) // nothing to do, try again later
		return
	}
	w.Header().Set("X-Claim-Status", "claimed")
	writeJSON(w, http.StatusOK, map[string]any{
		"job":           work[0],
		"lease_seconds": int(leaseDuration.Seconds()),
		"node":          req.Node,
	})
}

// leaseDuration is how long a claim survives without a heartbeat. Longer than
// any single progress gap (two seconds) by two orders of magnitude, shorter
// than a night.
const leaseDuration = 5 * time.Minute

func (s *Server) nodeHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_ID", "not a file id")
		return
	}
	var req struct {
		Fraction float64 `json:"fraction"`
		Speed    float64 `json:"speed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}
	if err := s.st.SetProgress(r.Context(), id, req.Fraction, req.Speed); err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nodeState is one endpoint for every transition a job makes. Separate verbs
// for encoding, verifying and done would be three ways to be wrong; the state
// name is the message.
func (s *Server) nodeState(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_ID", "not a file id")
		return
	}
	var req struct {
		State       string  `json:"state"`
		Code        string  `json:"code"`
		Detail      string  `json:"detail"`
		BytesBefore int64   `json:"bytes_before"`
		BytesAfter  int64   `json:"bytes_after"`
		VMAF        float64 `json:"vmaf"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}

	ctx := r.Context()
	applied := req.State
	switch req.State {
	case "planned", "encoding", "verifying", "replacing":
		err = s.st.SetState(ctx, id, req.State)
	case "done":
		err = s.st.Finish(ctx, id, req.BytesBefore, req.BytesAfter, req.VMAF)
	case "skipped":
		err = s.st.Skip(ctx, id, req.Code, req.Detail)
	case "failed":
		// The store decides whether this was the last attempt.
		applied, err = s.st.Fail(ctx, id, req.Code, req.Detail, s.maxAttempts())
	default:
		s.fail(w, http.StatusBadRequest, "E_BAD_STATE",
			"state must be planned, encoding, verifying, replacing, done, skipped or failed")
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	// The node needs to know whether the file will be retried or is parked.
	writeJSON(w, http.StatusOK, map[string]string{"state": applied})
}

func (s *Server) nodeEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FileID  int64  `json:"file_id"`
		Level   string `json:"level"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}
	if req.Level == "" {
		req.Level = "info"
	}
	if err := s.st.Event(r.Context(), req.Level, req.Code, req.FileID, req.Message, req.Data); err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// maxAttempts is how many times a file may fail before it is parked rather
// than requeued.
func (s *Server) maxAttempts() int {
	if s.cfg.Safety.MaxAttempts > 0 {
		return s.cfg.Safety.MaxAttempts
	}
	return 3
}

// ── small helpers ─────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ctx(r *http.Request) context.Context { return r.Context() }

func osStat(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

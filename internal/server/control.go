package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// ── steering ──────────────────────────────────────────────────────────────
//
// The read API answered "what is happening". These endpoints answer "make it
// do something", and they are deliberately boring: one verb per intent, state
// in the database, an event line for every action so the feed explains a queue
// that moved without the node.

// apiControl is what the control strip reads: the pause, the override, and
// whether a claim would be accepted right now.
func (s *Server) apiControl(w http.ResponseWriter, r *http.Request) {
	ctl, err := s.st.Control(r.Context())
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	open, spec, next := s.windowState()
	allowed, reason := s.claimsAllowed(r.Context(), time.Now())
	writeJSON(w, http.StatusOK, map[string]any{
		"control": ctl,
		"window": map[string]any{
			"open":    open,
			"spec":    spec,
			"next_at": next,
		},
		"claiming": map[string]any{"allowed": allowed, "reason": reason},
		"now":      time.Now().Unix(),
	})
}

// apiPause pauses or resumes claims. It does not touch the file that is
// already encoding: the node's contract is finish-then-stop, and killing a job
// mid-swap is how data is lost.
func (s *Server) apiPause(w http.ResponseWriter, r *http.Request) {
	if !guardJSON(w, r) {
		return
	}
	var req struct {
		Paused bool   `json:"paused"`
		Reason string `json:"reason"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}
	if err := s.st.SetPaused(r.Context(), req.Paused, "web"); err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	code, message := "M_RESUME", "queue resumed by hand"
	if req.Paused {
		code, message = "M_PAUSE", "queue paused by hand"
		if req.Reason != "" {
			message += ": " + req.Reason
		}
	}
	if err := s.st.Event(r.Context(), "warn", code, 0, message, ""); err != nil {
		s.log.Warn("event write failed", "code", code, "err", err)
	}
	s.apiControl(w, r)
}

// apiWindow forces the window open or shut, or hands it back to the schedule.
//
//	open  minutes>0 → for that long; minutes==0 → until somebody says otherwise
//	close → refuse new claims now, no expiry ("stop after this file")
//	auto  → the schedule is in charge again
func (s *Server) apiWindow(w http.ResponseWriter, r *http.Request) {
	if !guardJSON(w, r) {
		return
	}
	var req struct {
		Action  string `json:"action"`
		Minutes int    `json:"minutes"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}

	var err error
	var message string
	switch req.Action {
	case "open":
		until := int64(0)
		if req.Minutes > 0 {
			until = time.Now().Add(time.Duration(req.Minutes) * time.Minute).Unix()
		}
		err = s.st.SetOverride(r.Context(), store.OverrideOpen, until, "web")
		message = "window forced open by hand"
		if until > 0 {
			message = "window forced open by hand until " + time.Unix(until, 0).Format("15:04")
		}
	case "close":
		err = s.st.SetOverride(r.Context(), store.OverrideClose, 0, "web")
		message = "window forced shut by hand; no new claims"
	case "auto":
		err = s.st.ClearOverride(r.Context())
		message = "window handed back to the schedule"
	default:
		s.fail(w, http.StatusBadRequest, "E_BAD_ACTION", "action must be open, close or auto")
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if req.Action != "open" {
		// The forced session is over: its run ends with it, so the ledger does
		// not leave a manual row open for days.
		_, _ = s.st.CloseManualRuns(r.Context(), time.Now().Unix())
	}
	if err := s.st.Event(r.Context(), "warn", "M_WINDOW", 0, message, ""); err != nil {
		s.log.Warn("event write failed", "code", "M_WINDOW", "err", err)
	}
	s.apiControl(w, r)
}

// ── queue actions ─────────────────────────────────────────────────────────

func (s *Server) apiFileRetry(w http.ResponseWriter, r *http.Request) {
	s.fileAction(w, r, store.ActRetry)
}
func (s *Server) apiFileSkip(w http.ResponseWriter, r *http.Request) {
	s.fileAction(w, r, store.ActSkip)
}
func (s *Server) apiFilePriority(w http.ResponseWriter, r *http.Request) {
	s.fileAction(w, r, store.ActPriority)
}

func (s *Server) fileAction(w http.ResponseWriter, r *http.Request, action string) {
	if !guardJSON(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_ID", "not a file id")
		return
	}
	var req struct {
		Priority int `json:"priority"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}
	res, err := s.st.Act(r.Context(), action, []int64{id}, clampPriority(req.Priority))
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	if res.Applied == 0 {
		s.refuseAction(w, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// apiQueueAct applies one action to a selection, in one transaction. A bulk
// action is not a loop of HTTP calls: forty rows must be forty row updates or
// none, and the response says which ones were refused and why.
func (s *Server) apiQueueAct(w http.ResponseWriter, r *http.Request) {
	if !guardJSON(w, r) {
		return
	}
	var req struct {
		Action   string  `json:"action"`
		IDs      []int64 `json:"ids"`
		Priority int     `json:"priority"`
	}
	if err := decodeBody(r, &req); err != nil {
		s.fail(w, http.StatusBadRequest, "E_BAD_BODY", err.Error())
		return
	}
	switch req.Action {
	case store.ActRetry, store.ActSkip, store.ActPriority:
	default:
		s.fail(w, http.StatusBadRequest, "E_BAD_ACTION", "action must be retry, skip or priority")
		return
	}
	res, err := s.st.Act(r.Context(), req.Action, req.IDs, clampPriority(req.Priority))
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) refuseAction(w http.ResponseWriter, res store.ActResult) {
	if len(res.Refused) == 0 {
		s.fail(w, http.StatusConflict, "E_STATE", "that action does not apply to this file")
		return
	}
	ref := res.Refused[0]
	if ref.Reason == "missing" {
		s.fail(w, http.StatusNotFound, "E_NOT_FOUND", "no such file")
		return
	}
	s.fail(w, http.StatusConflict, "E_STATE", "the file is in state "+ref.State)
}

// clampPriority keeps the ordering explainable: next, normal, last — anything
// else would be a second sort key nobody can reason about.
func clampPriority(p int) int {
	switch {
	case p > 0:
		return 100
	case p < 0:
		return -100
	}
	return 0
}

// guardJSON refuses a mutation that did not come from this app's own client.
// A cross-site form post cannot set application/json without a preflight, and
// the only credential in play is the Access session cookie, so this is the
// cheap half of CSRF defence that this surface actually needs.
func guardJSON(w http.ResponseWriter, r *http.Request) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{
			"error": map[string]string{
				"code":    "E_CONTENT_TYPE",
				"message": "send Content-Type: application/json",
			},
		})
		return false
	}
	return true
}

// decodeBody tolerates an empty body: `{"action":"close"}` needs no fields, and
// a client sending nothing should not have to send `{}`.
func decodeBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

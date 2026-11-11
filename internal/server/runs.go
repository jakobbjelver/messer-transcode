package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/report"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// currentRunID is the ledger row a claim belongs to.
//
// A scheduled claim joins the window containing it. A claim outside the window
// (only possible because an operator forced it open) joins the manual run that
// is already open, or starts one — so a forced evening is one row in history
// rather than one per file.
func (s *Server) currentRunID(ctx context.Context) int64 {
	now := time.Now()
	if occ, ok := s.config().Schedule.OccurrenceAt(now); ok {
		// A scheduled claim ends a manual run: the forced session is over.
		if n, err := s.st.CloseManualRuns(ctx, now.Unix()); err == nil && n > 0 {
			s.log.Info("closed manual run", "count", n)
		}
		id, err := s.st.EnsureRun(ctx, store.RunRow{
			ID:      occ.Start.Unix(),
			StartAt: occ.Start.Unix(),
			EndAt:   occ.End.Unix(),
			Spec:    occ.Spec,
			Kind:    occ.Kind,
		})
		if err != nil {
			s.log.Warn("ensure run", "err", err)
			return 0
		}
		return id
	}

	if id, ok := s.st.OpenManualRun(ctx); ok {
		return id
	}
	id, err := s.st.EnsureRun(ctx, store.RunRow{
		ID:      now.Unix(),
		StartAt: now.Unix(),
		Spec:    "manual",
		Kind:    store.KindManual,
	})
	if err != nil {
		s.log.Warn("ensure manual run", "err", err)
		return 0
	}
	return id
}

// apiRun is one run: its figures and the files it handled.
//
// The files are grouped the way a run is read — what it placed, what it could
// not, and what it left alone — because a list of 20 rows sorted by id says
// nothing about a night.
func (s *Server) apiRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.fail(w, http.StatusBadRequest, "E_BAD_ID", "not a run id")
		return
	}
	ctx := r.Context()
	row, err := s.st.RunRowByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		s.fail(w, http.StatusNotFound, "E_NOT_FOUND", "no such run")
		return
	}
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}

	aggs, err := s.st.RunAggregates(ctx, []int64{id})
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}
	page, err := s.st.Queue(ctx, store.QueueFilter{RunID: id, Limit: 500})
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "E_STORE", err.Error())
		return
	}

	placed := []store.SummaryRow{}
	failed := []store.SummaryRow{}
	skipped := []store.SummaryRow{}
	other := []store.SummaryRow{}
	for _, f := range page.Rows {
		switch f.State {
		case "done":
			placed = append(placed, f)
		case "failed":
			failed = append(failed, f)
		case "skipped":
			skipped = append(skipped, f)
		default:
			other = append(other, f)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"run":     report.FromRow(row, aggs[id], time.Now()),
		"placed":  placed,
		"failed":  failed,
		"skipped": skipped,
		"open":    other,
	})
}

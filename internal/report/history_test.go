package report

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// TestHistorySurvivesAStateChange is the regression test for a replan that
// rewrote the past: the run summary used to read `state='done'`, so re-deciding
// a replaced file made earlier runs report ten files instead of sixty-five.
// A replacement is a fact (finished_at), not a state a later pass can overwrite.
func TestHistorySurvivesAStateChange(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	if _, _, err := st.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 1000, MtimeNS: 1},
		{Path: "/lib/other.mkv", Size: 2000, MtimeNS: 2},
	}); err != nil {
		t.Fatal(err)
	}
	// Claiming only takes rows that a plan has decided.
	rows, err := st.DB().QueryContext(ctx, `SELECT id FROM files ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	for _, id := range ids {
		if err := st.SetDecision(ctx, id, store.Decision{
			Decision: "encode", Code: "R_H264_ENCODE", Reason: "test", KeyKbps: 3000, State: "planned",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The run this work belongs to, recorded before the claim — that is the
	// contract the ledger added.
	start := time.Now().Add(-2 * time.Hour)
	runID := start.Unix()
	if _, err := st.EnsureRun(ctx, store.RunRow{
		ID:      runID,
		StartAt: runID,
		EndAt:   time.Now().Add(2 * time.Hour).Unix(),
		Spec:    "test",
		Kind:    "weekday",
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := st.ClaimWork(ctx, 2, runID)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("claim: %v (%d jobs)", err, len(jobs))
	}
	for _, j := range jobs {
		if err := st.Finish(ctx, j.ID, j.SizeBytes, j.SizeBytes/2, 95, nil); err != nil {
			t.Fatal(err)
		}
	}

	// A later plan re-decides both rows — the exact thing that used to erase
	// the run.
	for _, j := range jobs {
		if err := st.Skip(ctx, j.ID, "R_NO_RULE", "nothing to do"); err != nil {
			t.Fatal(err)
		}
	}

	row, err := st.RunRowByID(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	aggs, err := st.RunAggregates(ctx, []int64{runID})
	if err != nil {
		t.Fatal(err)
	}
	got := FromRow(row, aggs[runID], time.Now())
	if got.Done != 2 {
		t.Fatalf("done = %d, want 2 — a re-decide erased the run", got.Done)
	}
	if got.Before != 3000 || got.After != 1500 {
		t.Fatalf("bytes = %d/%d, want 3000/1500", got.Before, got.After)
	}
}

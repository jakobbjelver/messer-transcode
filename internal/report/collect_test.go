package report

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// Collect reads the ledger, not the schedule: an empty window is still a run,
// and a run's figures survive a re-decide (see history_test.go).
func TestCollectReadsTheLedger(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	sched := config.Schedule{Weekday: "00:30-05:30", Weekend: "00:30-05:30", Timezone: "UTC"}
	// 02:00 on a day with a window, so the occurrence is deterministic.
	now := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	occ, ok := sched.OccurrenceAt(now)
	if !ok {
		t.Fatal("test schedule should be open at 02:00")
	}

	if _, _, err := st.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 1_000_000_000, MtimeNS: 1},
	}); err != nil {
		t.Fatal(err)
	}
	page, err := st.Queue(ctx, store.QueueFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	id := page.Rows[0].ID
	if err := st.SetDecision(ctx, id, store.Decision{
		Decision: "encode", Code: "R_H264_ENCODE", Reason: "test", KeyKbps: 3000, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}

	runID, err := st.EnsureRun(ctx, store.RunRow{
		ID: occ.Start.Unix(), StartAt: occ.Start.Unix(), EndAt: occ.End.Unix(), Spec: occ.Spec, Kind: occ.Kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimWork(ctx, 1, runID); err != nil {
		t.Fatal(err)
	}
	if err := st.Finish(ctx, id, 1_000_000_000, 500_000_000, 95, nil); err != nil {
		t.Fatal(err)
	}

	runs, err := Collect(ctx, st, sched, now, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 3 {
		t.Fatalf("got %d runs, want 3 (an empty window is still a run)", len(runs))
	}
	if runs[0].ID != runID {
		t.Fatalf("newest run id = %d, want %d", runs[0].ID, runID)
	}
	if runs[0].Done != 1 || runs[0].Before != 1_000_000_000 || runs[0].After != 500_000_000 {
		t.Fatalf("run figures = %+v", runs[0])
	}
	if runs[0].VmafN != 1 || runs[0].VmafMean != 95 {
		t.Fatalf("vmaf = n%d mean%.1f", runs[0].VmafN, runs[0].VmafMean)
	}
	// The older windows exist and are empty.
	if runs[1].Done != 0 || runs[2].Done != 0 {
		t.Fatalf("older runs should be empty: %+v %+v", runs[1], runs[2])
	}
}

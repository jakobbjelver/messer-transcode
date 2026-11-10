package store

import (
	"context"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// TestFinishRecordsTheOutputFacts is the regression test for the replay that
// ate the ledger: after a replacement the row kept the source's size and mtime,
// so the next scan saw a changed file, reset it to "new" and re-decided work it
// had already done.
func TestFinishRecordsTheOutputFacts(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	if _, _, err := s.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 1000, MtimeNS: 100},
	}); err != nil {
		t.Fatal(err)
	}
	want := planned(t, s)

	p := media.Probe{
		Container: "matroska", VideoCodec: "hevc", Width: 1920, Height: 1080,
		BitrateKbps: 3000, DurationS: 6000, PixFmt: "yuv420p10le",
		SizeBytes: 400,
		Audio:     []media.AudioTrack{{Index: 1, Codec: "eac3", Lang: "eng"}},
	}
	out := &media.OutputFacts{Probe: p, MtimeNS: 555}
	if err := s.Finish(ctx, want.ID, 1000, 400, 95, out); err != nil {
		t.Fatalf("finish: %v", err)
	}

	// The scan now sees exactly what the finish recorded: it must not re-queue.
	inserted, changed, err := s.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 400, MtimeNS: 555},
	})
	if err != nil {
		t.Fatal(err)
	}
	if inserted != 0 || changed != 0 {
		t.Fatalf("rescan reported inserted=%d changed=%d, want 0/0", inserted, changed)
	}

	var state, codec, key string
	var size, mtime int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT state, video_codec, probe_key, size_bytes, mtime_ns FROM files WHERE id=?`, want.ID).
		Scan(&state, &codec, &key, &size, &mtime); err != nil {
		t.Fatal(err)
	}
	if state != "done" {
		t.Fatalf("state = %q, want done — the rescan re-queued a finished file", state)
	}
	if size != 400 || mtime != 555 {
		t.Fatalf("row still describes size=%d mtime=%d, want the output's 400/555", size, mtime)
	}
	if codec != "hevc" {
		t.Fatalf("codec = %q, want the output's hevc", codec)
	}
	if key != ProbeKey(400, 555) {
		t.Fatalf("probe_key = %q, want the output's key", key)
	}
}

// TestFinishWithoutFactsStillFinishes keeps the older contract working: a node
// that sends only bytes must not fail.
func TestFinishWithoutFactsStillFinishes(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	if _, _, err := s.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 1000, MtimeNS: 100},
	}); err != nil {
		t.Fatal(err)
	}
	job := planned(t, s)
	if err := s.Finish(ctx, job.ID, 1000, 400, 0, nil); err != nil {
		t.Fatalf("finish: %v", err)
	}
	var state string
	if err := s.db.QueryRowContext(ctx, `SELECT state FROM files WHERE id=?`, job.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "done" {
		t.Fatalf("state = %q, want done", state)
	}
}

// TestReclaimedSurvivesAStateChange is the dashboard half of the same story:
// the ledger is a fact about a replacement, not about the row's current state.
func TestReclaimedSurvivesAStateChange(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	if _, _, err := s.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 1000, MtimeNS: 100},
	}); err != nil {
		t.Fatal(err)
	}
	job := planned(t, s)
	if err := s.Finish(ctx, job.ID, 1000, 400, 95, nil); err != nil {
		t.Fatal(err)
	}
	// A later plan flips the row to skipped.
	if err := s.Skip(ctx, job.ID, "R_NO_RULE", "nothing to do"); err != nil {
		t.Fatal(err)
	}

	d, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.ReclaimedBytes != 600 {
		t.Fatalf("reclaimed = %d, want 600 — the ledger lost a finished replacement", d.ReclaimedBytes)
	}
}

// planned inserts one row and walks it to a claimable job.
func planned(t *testing.T, s *Store) Work {
	t.Helper()
	ctx := context.Background()
	var id int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM files LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDecision(ctx, id, Decision{
		Decision: "encode", Code: "R_H264_ENCODE", Reason: "test", KeyKbps: 3000, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ClaimWork(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(jobs))
	}
	return jobs[0]
}

// TestFailAfterFinishDoesNotRequeue is the stale-report guard: a node that
// retries an old attempt must not turn a replaced file back into work.
func TestFailAfterFinishDoesNotRequeue(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	if _, _, err := s.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/lib/film.mkv", Size: 1000, MtimeNS: 100},
	}); err != nil {
		t.Fatal(err)
	}
	job := planned(t, s)
	if err := s.Finish(ctx, job.ID, 1000, 400, 95, nil); err != nil {
		t.Fatal(err)
	}

	state, err := s.Fail(ctx, job.ID, "E_NODE_INTERRUPTED", "stale report", 3)
	if err != nil {
		t.Fatal(err)
	}
	if state != "done" {
		t.Fatalf("Fail returned %q, want done", state)
	}
	var got string
	var attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT state, attempts FROM files WHERE id=?`, job.ID).
		Scan(&got, &attempts); err != nil {
		t.Fatal(err)
	}
	if got != "done" {
		t.Fatalf("state = %q, want done — a stale failure re-queued a replaced file", got)
	}
	if attempts != 0 {
		t.Fatalf("attempts = %d, want 0", attempts)
	}
}

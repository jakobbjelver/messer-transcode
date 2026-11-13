package store

import (
	"context"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// The catalogue has no tombstone: a scan upserts what it sees and nothing else.
// A file removed by Sonarr or a manual delete therefore left a row behind that
// stayed counted, quoted in the projection and still claimable — so the queue
// spent a claim on it and failed with E_SOURCE_MISSING. These tests pin the
// state that fixes that: the row keeps its history and stops being work.
//
// Every stamp here is relative to the wall clock, because the store stamps the
// rows it writes with time.Now(): a scan stamp in the epoch would look older
// than the probe that followed it, and the test would be measuring the fixture.
type stamps struct{ seed, scan, later int64 }

func newStamps() stamps {
	now := time.Now().Unix()
	return stamps{seed: now - 3600, scan: now + 60, later: now + 120}
}

func TestMarkGoneMarksWhatTheScanDidNotSee(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/kept.mkv", Size: 1, MtimeNS: 1},
		{Path: "/lib/vanished.mkv", Size: 2, MtimeNS: 2},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}

	// The next scan sees only the survivor and carries the newer stamp.
	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/kept.mkv", Size: 1, MtimeNS: 1},
	}, ts.scan); err != nil {
		t.Fatal(err)
	}

	n, err := s.MarkGone(ctx, "movies", ts.scan)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("gone = %d, want 1", n)
	}

	if got := stateOf(t, s, "/lib/vanished.mkv"); got != "gone" {
		t.Errorf("vanished row state = %q, want gone", got)
	}
	if got := stateOf(t, s, "/lib/kept.mkv"); got != "new" {
		t.Errorf("seen row state = %q, want new — the prune took a file the scan found", got)
	}
}

// A scan can run while a job is in flight, and the two disagree for a second:
// the encoder has the file open under a temporary name and the walk may not see
// it. The encoder is right — never retire a row that is mid-job.
func TestMarkGoneLeavesInFlightFiles(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/encoding.mkv", Size: 1, MtimeNS: 1},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET state='encoding'`); err != nil {
		t.Fatal(err)
	}

	n, err := s.MarkGone(ctx, "movies", ts.later)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("gone = %d, want 0 — an in-flight row was retired", n)
	}
	if got := stateOf(t, s, "/lib/encoding.mkv"); got != "encoding" {
		t.Errorf("state = %q, want encoding", got)
	}
}

func TestMarkGoneIsScopedToOneLibrary(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/movies/a.mkv", Size: 1, MtimeNS: 1},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.UpsertScannedAt(ctx, "shows", []media.Scanned{
		{Path: "/shows/b.mkv", Size: 1, MtimeNS: 1},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}

	if _, err := s.MarkGone(ctx, "movies", ts.later); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, s, "/movies/a.mkv"); got != "gone" {
		t.Errorf("movies row = %q, want gone", got)
	}
	if got := stateOf(t, s, "/shows/b.mkv"); got != "new" {
		t.Errorf("shows row = %q, want new — the prune crossed library lines", got)
	}
}

// A file that comes back is a file nobody has facts about any more: its probe
// is dropped and it re-enters the probe queue. Keeping the old probe would let
// the planner decide from measurements of a file that no longer exists.
func TestGoneRowRevivesWhenTheFileReturns(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/returns.mkv", Size: 10, MtimeNS: 10},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}
	id := idOf(t, s, "/lib/returns.mkv")
	if err := s.SetProbe(ctx, id, media.Probe{VideoCodec: "h264", Width: 1920, Height: 1080, BitrateKbps: 8000}, ProbeKey(10, 10), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkGone(ctx, "movies", ts.later); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, s, "/lib/returns.mkv"); got != "gone" {
		t.Fatalf("state after the prune = %q, want gone", got)
	}

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/returns.mkv", Size: 10, MtimeNS: 10},
	}, ts.later); err != nil {
		t.Fatal(err)
	}

	if got := stateOf(t, s, "/lib/returns.mkv"); got != "new" {
		t.Fatalf("revived row state = %q, want new", got)
	}
	var key string
	if err := s.DB().QueryRowContext(ctx, `SELECT COALESCE(probe_key,'') FROM files WHERE path=?`,
		"/lib/returns.mkv").Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key != "" {
		t.Errorf("probe_key = %q after revival, want empty so the file is probed again", key)
	}
	pending, err := s.FilesNeedingProbe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Path != "/lib/returns.mkv" {
		t.Errorf("pending = %+v, want the revived file", pending)
	}
}

// A gone row is not work: it must not be probed, decided, summarised, or photo-
// graphed in the decision counts. What it must keep is its history.
func TestGoneIsNotCountedAsCatalogue(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/done.mkv", Size: 1000, MtimeNS: 1},
		{Path: "/lib/planned.mkv", Size: 2000, MtimeNS: 2},
		{Path: "/lib/deleted-planned.mkv", Size: 2000, MtimeNS: 3},
		{Path: "/lib/deleted-failed.mkv", Size: 500, MtimeNS: 4},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}

	// Give the two keepers a probe and a verdict.
	for _, path := range []string{"/lib/done.mkv", "/lib/planned.mkv"} {
		id := idOf(t, s, path)
		if err := s.SetProbe(ctx, id, media.Probe{
			VideoCodec: "h264", Width: 1920, Height: 1080, BitrateKbps: 8000, DurationS: 6000,
		}, ProbeKey(1, 1), nil); err != nil {
			t.Fatal(err)
		}
		if err := s.SetDecision(ctx, id, Decision{
			Decision: "encode", Code: "R_H264_ENCODE", Reason: "test", KeyKbps: 3000, State: "planned",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The done row carries a real finish: 1000 in, 400 out.
	if err := s.Finish(ctx, idOf(t, s, "/lib/done.mkv"), 1000, 400, 95,
		&media.OutputFacts{Probe: media.Probe{VideoCodec: "hevc"}, MtimeNS: 9}); err != nil {
		t.Fatal(err)
	}

	// The two deleted rows had verdicts too, one of them a failure.
	for _, path := range []string{"/lib/deleted-planned.mkv", "/lib/deleted-failed.mkv"} {
		if err := s.SetDecision(ctx, idOf(t, s, path), Decision{
			Decision: "encode", Code: "R_H264_ENCODE", Reason: "test", KeyKbps: 3000, State: "planned",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE files SET state='failed' WHERE path='/lib/deleted-failed.mkv'`); err != nil {
		t.Fatal(err)
	}

	// The next scan sees the keepers exactly as they now are, and neither of
	// the deleted files.
	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/done.mkv", Size: 400, MtimeNS: 9},
		{Path: "/lib/planned.mkv", Size: 2000, MtimeNS: 2},
	}, ts.scan); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkGone(ctx, "movies", ts.scan); err != nil {
		t.Fatal(err)
	}

	d, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalFiles != 2 {
		t.Errorf("total_files = %d, want 2 — the dashboard is still counting deleted files", d.TotalFiles)
	}
	if d.TotalBytes != 2400 {
		t.Errorf("total_bytes = %d, want 2400 (the replaced file's 400 + the queued 2000)", d.TotalBytes)
	}
	if d.ReclaimedBytes != 600 {
		t.Errorf("reclaimed_bytes = %d, want 600 — history survives a delete", d.ReclaimedBytes)
	}
	if d.DoneBytes != 400 {
		t.Errorf("done_bytes = %d, want 400", d.DoneBytes)
	}
	if d.Failures != 0 {
		t.Errorf("failures = %d, want 0 — a deleted file's failure is not a live problem", d.Failures)
	}
	if got := d.Decisions["encode"]; got != 2 {
		t.Errorf("decisions[encode] = %d, want 2", got)
	}
	if got := d.States["gone"]; got != 2 {
		t.Errorf("states[gone] = %d, want 2 — the page has to be able to show them", got)
	}
	// Only the planned keeper projects bytes: 3000 kbps over 6000 s = 2.25 GB.
	if d.ProjectedBytes != 2_250_000_000 {
		t.Errorf("projected_bytes = %d, want 2250000000 — a deleted row is still being quoted", d.ProjectedBytes)
	}
}

func TestGoneRowsAreNeitherProbedNorDecided(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	// Unprobed, and probed-but-undecided.
	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/unprobed.mkv", Size: 1, MtimeNS: 1},
		{Path: "/lib/undecided.mkv", Size: 2, MtimeNS: 2},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProbe(ctx, idOf(t, s, "/lib/undecided.mkv"),
		media.Probe{VideoCodec: "h264"}, ProbeKey(2, 2), nil); err != nil {
		t.Fatal(err)
	}
	// Nothing sees either of them again.
	if _, err := s.MarkGone(ctx, "movies", ts.later); err != nil {
		t.Fatal(err)
	}

	pending, err := s.FilesNeedingProbe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("FilesNeedingProbe = %+v, want nothing — a gone file cannot be probed", pending)
	}
	undecided, err := s.FilesNeedingDecision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(undecided) != 0 {
		t.Errorf("FilesNeedingDecision = %+v, want nothing", undecided)
	}
	buckets, err := s.Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range buckets {
		if b.Files != 0 {
			t.Errorf("summary still counts %d file(s) in %q", b.Files, b.ReasonCode)
		}
	}
}

// A retirement must not rewrite a night that already happened. The run ledger
// counts what a file did in a run, so a row retired afterwards still answers
// with the failure or the skip it recorded — otherwise a Sonarr upgrade would
// silently improve last night's numbers.
func TestRetiringARowKeepsItsRunHistory(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	ts := newStamps()

	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: "/lib/failed.mkv", Size: 10, MtimeNS: 1},
		{Path: "/lib/skipped.mkv", Size: 20, MtimeNS: 2},
	}, ts.seed); err != nil {
		t.Fatal(err)
	}
	failedID := idOf(t, s, "/lib/failed.mkv")
	skippedID := idOf(t, s, "/lib/skipped.mkv")
	for _, id := range []int64{failedID, skippedID} {
		if err := s.SetDecision(ctx, id, Decision{
			Decision: "encode", Code: "R_H264_ENCODE", Reason: "test", KeyKbps: 3000, State: "planned",
		}); err != nil {
			t.Fatal(err)
		}
	}

	const runID = int64(1794526200)
	if _, err := s.EnsureRun(ctx, RunRow{
		ID: runID, StartAt: runID, EndAt: runID + 18000, Spec: "test", Kind: "weekday",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimWork(ctx, 2, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fail(ctx, failedID, "E_SOURCE_MISSING", "no such file", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Skip(ctx, skippedID, "R_QUALITY_FLOOR", "cannot reach the target"); err != nil {
		t.Fatal(err)
	}

	before, err := s.RunAggregates(ctx, []int64{runID})
	if err != nil {
		t.Fatal(err)
	}
	if before[runID].FailedCodes["E_SOURCE_MISSING"] != 1 || before[runID].SkipCodes["R_QUALITY_FLOOR"] != 1 {
		t.Fatalf("ledger before the prune = %+v, want one failure and one skip", before[runID])
	}

	// The files are deleted afterwards, and the next scan retires them.
	if _, err := s.MarkGone(ctx, "movies", ts.later); err != nil {
		t.Fatal(err)
	}
	after, err := s.RunAggregates(ctx, []int64{runID})
	if err != nil {
		t.Fatal(err)
	}
	if after[runID].FailedCodes["E_SOURCE_MISSING"] != 1 {
		t.Errorf("the run lost its failure: %+v", after[runID].FailedCodes)
	}
	if after[runID].SkipCodes["R_QUALITY_FLOOR"] != 1 {
		t.Errorf("the run lost its skip: %+v", after[runID].SkipCodes)
	}
}

func stateOf(t *testing.T, s *Store, path string) string {
	t.Helper()
	var state string
	if err := s.DB().QueryRow(`SELECT state FROM files WHERE path = ?`, path).Scan(&state); err != nil {
		t.Fatalf("state of %s: %v", path, err)
	}
	return state
}

func idOf(t *testing.T, s *Store, path string) int64 {
	t.Helper()
	var id int64
	if err := s.DB().QueryRow(`SELECT id FROM files WHERE path = ?`, path).Scan(&id); err != nil {
		t.Fatalf("id of %s: %v", path, err)
	}
	return id
}

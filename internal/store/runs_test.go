package store

import (
	"context"
	"testing"
)

// A run's busy figure counted only the rows that finished, so a night spent
// re-encoding files that then parked read as idle. Productive and wasted time
// are now separate numbers: the encoder occupancy is their sum, and only the
// first one bought anything.
func TestRunAggregateSeparatesProductiveFromWastedTime(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	const run = int64(1794785400)

	if _, err := s.DB().Exec(`
INSERT INTO runs (id, start_at, end_at, spec, kind, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		run, run, run+18000, "weekdays, 00:30–05:30", "weekday", run); err != nil {
		t.Fatal(err)
	}

	ins := func(path, state, decision, code string, started, end int64, finished bool) {
		t.Helper()
		var fin any
		if finished {
			fin = end
		}
		if _, err := s.DB().Exec(`
INSERT INTO files (path, library, size_bytes, mtime_ns, state, decision, reason_code,
                   started_at, finished_at, updated_at, run_id)
VALUES (?, 'movies', 4000000000, 1, ?, ?, ?, ?, ?, ?, ?)`,
			path, state, decision, code, started, fin, end, run); err != nil {
			t.Fatal(err)
		}
	}

	// One placement: 600 s of work that bought a file.
	ins("/media/A/Film A.mkv", "done", "encode", "R_H264_ENCODE", run+1000, run+1600, true)
	// One failure after 300 s of encoding that bought nothing.
	ins("/media/B/Film B.mkv", "failed", "encode", "E_VERIFY_SIZE_RATIO", run+2000, run+2300, false)
	// One quality-floor park after 200 s.
	ins("/media/C/Film C.mkv", "skipped", "skip", "R_QUALITY_FLOOR", run+3000, run+3200, false)
	// A plan-time skip that was never claimed has no time to report.
	ins("/media/D/Film D.mkv", "skipped", "skip", "R_NO_RULE", 0, run+4000, false)

	aggs, err := s.RunAggregates(ctx, []int64{run})
	if err != nil {
		t.Fatal(err)
	}
	a := aggs[run]
	if a.BusySeconds != 600 {
		t.Errorf("busy = %ds, want 600 (finished rows only)", a.BusySeconds)
	}
	if a.WastedSeconds != 500 {
		t.Errorf("wasted = %ds, want 500 (300 failed + 200 parked)", a.WastedSeconds)
	}
	if a.Done != 1 || len(a.FailedCodes) != 1 {
		t.Errorf("counts = %d done, %d failure codes, want 1 and 1", a.Done, len(a.FailedCodes))
	}
}

// A verdict can be rewritten days after the run that claimed it — a re-decide
// does exactly that — so updated_at is not a reliable end for the row's work.
// The 17 Nov run read 1,212 wasted minutes and 16 Nov read sixty hours, purely
// because rows skipped during them were re-judged the next morning.
//
// Waste is only counted when the refusal was recorded in the run (or in the
// two hours after it, since a job claimed at 05:29 can finish at 06:00). A row
// whose verdict moved days later contributed nothing that night.
func TestWastedTimeCountsOnlyRefusalsRecordedInTheRun(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	const run = int64(1794958200)
	const end = run + 18000

	if _, err := s.DB().Exec(`
INSERT INTO runs (id, start_at, end_at, spec, kind, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, run, run, end, "weekdays, 00:30–05:30", "weekday", run); err != nil {
		t.Fatal(err)
	}
	ins := func(path string, started, updated int64) {
		t.Helper()
		if _, err := s.DB().Exec(`
INSERT INTO files (path, library, size_bytes, mtime_ns, state, decision, reason_code,
                   started_at, updated_at, run_id)
VALUES (?, 'movies', 4000000000, 1, 'skipped', 'skip', 'R_QUALITY_FLOOR', ?, ?, ?)`,
			path, started, updated, run); err != nil {
			t.Fatal(err)
		}
	}
	// Parked inside the run: an hour of encoder time that bought nothing.
	ins("/media/A/Parked.mkv", run+3600, run+7200)
	// Re-decided three days later: its verdict belongs to no run in this window.
	ins("/media/B/Rewritten.mkv", run+3600, run+3*86400)

	aggs, err := s.RunAggregates(ctx, []int64{run})
	if err != nil {
		t.Fatal(err)
	}
	if got := aggs[run].WastedSeconds; got != 3600 {
		t.Errorf("wasted = %ds, want 3600 (only the refusal recorded that night)", got)
	}
}

// Once a failed job reports its stage timings, waste is a measurement rather
// than an estimate. The claim window includes waiting for the encoder to finish
// the previous film, which made a night of 50-second samples read as 228
// minutes.
func TestWastedTimePrefersTheMeasuredEncodeSeconds(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)
	const run = int64(1795044600)
	const end = run + 18000

	if _, err := s.DB().Exec(`
INSERT INTO runs (id, start_at, end_at, spec, kind, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, run, run, end, "weekdays, 00:30–05:30", "weekday", run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
INSERT INTO files (id, path, library, size_bytes, mtime_ns, state, decision, reason_code,
                   started_at, updated_at, run_id)
VALUES (1, '/media/A/Parked.mkv', 'movies', 4000000000, 1, 'skipped', 'skip',
        'R_QUALITY_FLOOR', ?, ?, ?)`, run+1000, run+2000, run); err != nil {
		t.Fatal(err)
	}
	// The node's own measurement for that job: 52 seconds of encoder time.
	if _, err := s.DB().Exec(`
INSERT INTO events (at, level, code, file_id, message, data)
VALUES (?, 'info', 'METRICS', 1, 'timings', '{"encode_s":52,"egress_s":0,"wait_s":900}')`,
		run+2000); err != nil {
		t.Fatal(err)
	}

	aggs, err := s.RunAggregates(ctx, []int64{run})
	if err != nil {
		t.Fatal(err)
	}
	if got := aggs[run].WastedSeconds; got != 52 {
		t.Errorf("wasted = %ds, want 52 (the measured encode, not the 1,000 s claim window)", got)
	}
}

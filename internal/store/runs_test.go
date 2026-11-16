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
	ins("/media/A/Film A.mkv", "done", "encode", "R_H264_ENCODE", 1000, 1600, true)
	// One failure after 300 s of encoding that bought nothing.
	ins("/media/B/Film B.mkv", "failed", "encode", "E_VERIFY_SIZE_RATIO", 2000, 2300, false)
	// One quality-floor park after 200 s.
	ins("/media/C/Film C.mkv", "skipped", "skip", "R_QUALITY_FLOOR", 3000, 3200, false)
	// A plan-time skip that was never claimed has no time to report.
	ins("/media/D/Film D.mkv", "skipped", "skip", "R_NO_RULE", 0, 4000, false)

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

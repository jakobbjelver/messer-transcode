package store

import (
	"context"
	"testing"
)

// A ladder change must be able to reach the queue, but only where the planner
// may still judge: queued or refused rows. A finished file must never be
// re-decided into work, a retired row must stay retired, and an in-flight row
// belongs to the encoder.
func TestRedecidableTouchesOnlyUnsettledRows(t *testing.T) {
	ctx := context.Background()
	s := tempStore(t)

	ins := func(id int64, path, state string) {
		t.Helper()
		if _, err := s.DB().Exec(`
INSERT INTO files (id, path, library, size_bytes, mtime_ns, state, decision, probe_key, updated_at)
VALUES (?, ?, 'movies', 4000000000, 1, ?, 'encode', 'probe', 1)`,
			id, path, state); err != nil {
			t.Fatal(err)
		}
	}
	ins(1, "/media/A/planned.mkv", "planned")
	ins(2, "/media/B/skipped.mkv", "skipped")
	ins(3, "/media/C/failed.mkv", "failed")
	ins(4, "/media/D/probed.mkv", "probed")
	ins(5, "/media/E/done.mkv", "done")
	ins(6, "/media/F/gone.mkv", "gone")
	ins(7, "/media/G/encoding.mkv", "encoding")

	all, err := s.Redecidable(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]bool{}
	for _, u := range all {
		got[u.ID] = true
	}
	for _, id := range []int64{1, 2, 3, 4} {
		if !got[id] {
			t.Errorf("row %d should be re-decidable", id)
		}
	}
	for _, id := range []int64{5, 6, 7} {
		if got[id] {
			t.Errorf("row %d must not be re-decided (done, gone and in-flight are settled)", id)
		}
	}

	// The id list narrows it, which is how one ladder change is applied to the
	// handful of rows it actually affects.
	narrow, err := s.Redecidable(ctx, []int64{2, 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(narrow) != 1 || narrow[0].ID != 2 {
		t.Fatalf("narrowed list = %v, want just row 2 (5 is done)", narrow)
	}
}

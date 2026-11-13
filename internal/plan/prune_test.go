package plan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// The prune rides on the scan: the only place that knows both what is on disk
// and what the database believes. A plan run must retire the rows whose files
// the walk did not find, or the queue keeps offering deleted files as work.
func TestPlanRetiresFilesTheScanDidNotSee(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	kept := filepath.Join(root, "kept.mkv")
	if err := os.WriteFile(kept, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := tempStore(t)
	vanished := filepath.Join(root, "vanished.mkv")
	// Seeded an hour ago: this walk is the scan that has to notice it is gone.
	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: kept, Size: 17, MtimeNS: 1},
		{Path: vanished, Size: 99, MtimeNS: 2},
	}, time.Now().Unix()-3600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Libraries: []config.Library{{
		Name: "movies", Root: root, Extensions: []string{".mkv"},
	}}}
	rep, err := Run(ctx, Options{Config: cfg, Store: s, SkipProbe: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if rep.Gone != 1 {
		t.Errorf("gone = %d, want 1", rep.Gone)
	}

	var state string
	if err := s.DB().QueryRow(`SELECT state FROM files WHERE path = ?`, vanished).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "gone" {
		t.Errorf("vanished row = %q, want gone", state)
	}
	if err := s.DB().QueryRow(`SELECT state FROM files WHERE path = ?`, kept).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "gone" {
		t.Errorf("the file the scan found was retired")
	}
}

// An unmounted volume reads as an empty library, and a prune taken from that
// walk would retire the whole catalogue. The root has to exist before a scan's
// silence means anything.
func TestPlanDoesNotPruneAnUnmountedLibrary(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "not-mounted")

	s := tempStore(t)
	row := filepath.Join(root, "film.mkv")
	if _, _, err := s.UpsertScannedAt(ctx, "movies", []media.Scanned{
		{Path: row, Size: 42, MtimeNS: 1},
	}, time.Now().Unix()-3600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Libraries: []config.Library{{
		Name: "movies", Root: root, Extensions: []string{".mkv"},
	}}}
	rep, err := Run(ctx, Options{Config: cfg, Store: s, SkipProbe: true})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if rep.Gone != 0 {
		t.Errorf("gone = %d, want 0 — the prune ran against a missing root", rep.Gone)
	}
	if len(rep.Warnings) == 0 {
		t.Errorf("no warning for a library whose root is missing")
	}

	var state string
	if err := s.DB().QueryRow(`SELECT state FROM files WHERE path = ?`, row).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state == "gone" {
		t.Errorf("a missing root retired the catalogue")
	}
}

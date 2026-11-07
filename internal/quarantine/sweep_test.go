package quarantine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mustWrite(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func tree(t *testing.T, days map[string]int) string {
	t.Helper()
	root := t.TempDir()
	for day, files := range days {
		for i := 0; i < files; i++ {
			// Distinct names: writing the same path twice is one file, which
			// made "2 files" tests assert against a count of 1.
			mustWrite(t, filepath.Join(root, day, "Movies", "A", fmt.Sprintf("film%d.mkv", i)), 1000)
		}
	}
	return root
}

// The boundary is exact: a directory exactly keepDays old goes, the day after
// stays. An off-by-one here either deletes a file a day early or leaves one for
// an extra fortnight.
func TestBoundaryIsExact(t *testing.T) {
	root := tree(t, map[string]int{"2026-10-24": 1, "2026-10-25": 1})
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)

	res, err := Sweep(context.Background(), root, 14, now, true)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, d := range res.Days {
		got[d.Date.Format("2006-01-02")] = d.Expired
	}
	if !got["2026-10-24"] {
		t.Error("a directory exactly keepDays old was not expired")
	}
	if got["2026-10-25"] {
		t.Error("a directory one day younger was expired")
	}
}

// Nothing is deleted in a dry run, and the report still says what it would do.
func TestDryRunTouchesNothing(t *testing.T) {
	root := tree(t, map[string]int{"2026-01-01": 2})
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)

	res, err := Sweep(context.Background(), root, 14, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Freed != 2000 {
		t.Errorf("dry run reported removed=%d freed=%d, want 2/2000", res.Removed, res.Freed)
	}
	if _, err := os.Stat(filepath.Join(root, "2026-01-01")); err != nil {
		t.Error("dry run deleted the directory")
	}
}

func TestSweepRemovesOnlyExpired(t *testing.T) {
	root := tree(t, map[string]int{"2026-01-01": 2, "2026-11-07": 3})
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)

	res, err := Sweep(context.Background(), root, 14, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 2 || res.Kept != 3 {
		t.Errorf("removed=%d kept=%d, want 2/3", res.Removed, res.Kept)
	}
	if _, err := os.Stat(filepath.Join(root, "2026-01-01")); !os.IsNotExist(err) {
		t.Error("the expired directory survived")
	}
	if _, err := os.Stat(filepath.Join(root, "2026-11-07")); err != nil {
		t.Error("the current directory was removed")
	}
	if res.Oldest.Format("2006-01-02") != "2026-11-07" {
		t.Errorf("oldest kept = %s, want today", res.Oldest.Format("2006-01-02"))
	}
}

// Only directories this package named are ever removed. A stray folder, a file
// called like a date, and a symlink are all left alone.
func TestOnlyDatedDirectoriesAreTouched(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "2026-01-01", "x.mkv"), 10)
	mustWrite(t, filepath.Join(root, "notes.txt"), 5)
	mustWrite(t, filepath.Join(root, "2026-02-30", "y.mkv"), 10) // not a real date
	if err := os.MkdirAll(filepath.Join(root, "keep-me"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/Volumes", filepath.Join(root, "2026-03-01")); err != nil {
		t.Skip("cannot make symlinks here")
	}

	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local)
	res, err := Sweep(context.Background(), root, 14, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 1 {
		t.Errorf("removed %d files, want only the dated directory's 1", res.Removed)
	}
	for _, name := range []string{"notes.txt", "keep-me", "2026-02-30", "2026-03-01"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Errorf("%s was removed; only YYYY-MM-DD directories may go", name)
		}
	}
	if len(res.Ignored) != 4 {
		t.Errorf("ignored %v, want 4 entries reported", res.Ignored)
	}
}

// A negative window is a configuration mistake, not an instruction to empty the
// quarantine.
func TestNegativeWindowRefuses(t *testing.T) {
	if _, err := Sweep(context.Background(), t.TempDir(), -1, time.Now(), false); err == nil {
		t.Fatal("a negative keep window was accepted")
	}
}

// A missing root is not an error: nothing has been quarantined yet.
func TestMissingRootIsFine(t *testing.T) {
	res, err := Sweep(context.Background(), filepath.Join(t.TempDir(), "nope"), 14, time.Now(), false)
	if err != nil || res.Removed != 0 {
		t.Fatalf("err=%v removed=%d, want no error and nothing removed", err, res.Removed)
	}
}

func TestKeepsMeanNothingExpires(t *testing.T) {
	root := tree(t, map[string]int{"2026-11-06": 4})
	res, err := Sweep(context.Background(), root, 30, time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Removed != 0 || res.Kept != 4 {
		t.Errorf("removed=%d kept=%d, want 0/4", res.Removed, res.Kept)
	}
}

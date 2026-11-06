package transcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReplaceSwapsAtomically(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "film.mkv")
	tmp := filepath.Join(dir, ".messer-1.tmp")

	write(t, final, "original")
	write(t, tmp, "encoded")

	if err := Replace(tmp, final); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(final)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "encoded" {
		t.Errorf("final = %q, want the encoded bytes", got)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("the temp file should be gone after the rename")
	}
}

func TestReplaceLeavesTheOriginalWhenTheTempIsMissing(t *testing.T) {
	dir := t.TempDir()
	final := filepath.Join(dir, "film.mkv")
	write(t, final, "original")

	err := Replace(filepath.Join(dir, "missing.tmp"), final)
	if err == nil {
		t.Fatal("expected an error")
	}
	got, _ := os.ReadFile(final)
	if string(got) != "original" {
		t.Errorf("a failed replace must not touch the original, got %q", got)
	}
}

func TestQuarantineHardlinksWhenPossible(t *testing.T) {
	lib := t.TempDir()
	quar := t.TempDir()
	original := filepath.Join(lib, "Movies", "Film (2020)", "film.mkv")
	write(t, original, "the original bytes")

	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	q, err := Quarantine(original, lib, quar, at)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(q, filepath.Join("2026-11-06", "Movies", "Film (2020)", "film.mkv")) {
		t.Errorf("quarantine path = %q, want a dated mirror of the library path", q)
	}
	got, err := os.ReadFile(q)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "the original bytes" {
		t.Errorf("quarantined copy = %q", got)
	}

	// The original is still there: quarantine never moves or deletes it.
	if _, err := os.Stat(original); err != nil {
		t.Errorf("quarantine must leave the original in place: %v", err)
	}
}

func TestQuarantineDoesNotOverwriteSameDay(t *testing.T) {
	lib := t.TempDir()
	quar := t.TempDir()
	original := filepath.Join(lib, "film.mkv")
	write(t, original, "v1")

	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	first, err := Quarantine(original, lib, quar, at)
	if err != nil {
		t.Fatal(err)
	}

	// Replace the original the way the pipeline does: a rename, which gives the
	// path a new inode. An in-place write would follow the hardlink instead.
	tmp := filepath.Join(lib, ".messer-1.tmp")
	write(t, tmp, "v2")
	if err := Replace(tmp, original); err != nil {
		t.Fatal(err)
	}

	second, err := Quarantine(original, lib, quar, at)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("the second quarantine overwrote the first")
	}
	if got, _ := os.ReadFile(first); string(got) != "v1" {
		t.Errorf("the first quarantine was clobbered: %q", got)
	}
	if got, _ := os.ReadFile(second); string(got) != "v2" {
		t.Errorf("the second quarantine = %q, want v2", got)
	}
}

func TestLockFileIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.lock")

	release, err := LockFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockFile(path); err == nil {
		t.Fatal("a second lock on the same file should fail")
	}
	release()

	release2, err := LockFile(path)
	if err != nil {
		t.Fatalf("after releasing, locking should work: %v", err)
	}
	release2()
}

func TestSameSize(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	c := filepath.Join(dir, "c")
	write(t, a, "12345")
	write(t, b, "12345")
	write(t, c, "1234")

	same, err := SameSize(a, b)
	if err != nil || !same {
		t.Errorf("SameSize(a,b) = %v, %v", same, err)
	}
	same, err = SameSize(a, c)
	if err != nil || same {
		t.Errorf("SameSize(a,c) = %v, %v", same, err)
	}
}

func TestCopyFileFsyncs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	write(t, src, "payload")

	var progress int64
	if err := CopyFile(src, dst, func(n int64) { progress = n }); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "payload" {
		t.Errorf("copy = %q", got)
	}
	if progress != int64(len("payload")) {
		t.Errorf("progress = %d, want %d", progress, len("payload"))
	}
}

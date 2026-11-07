package transcode

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The deployment puts scratch and quarantine on the fast disk and the library on
// the slow one, so this is the normal path: os.Link fails with EXDEV and the
// full-copy fallback has to carry the quarantine. The first real encode of a
// night is a terrible place to discover that fallback is broken, so it is tested
// where both disks exist.
//
// It also pins the safety property that makes cross-device safe: Quarantine
// leaves the original in place, so the bytes exist twice before Replace swaps
// them.
func TestQuarantineAcrossVolumes(t *testing.T) {
	const media, active = "/Volumes/Media", "/Volumes/Active"
	for _, root := range []string{media, active} {
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			t.Skipf("%s is not mounted; the cross-volume path needs both disks", root)
		}
	}

	base := ".messer-transcode-qtest-" + strings.Repeat("x", 0) + t.Name()
	libRoot := filepath.Join(media, base)
	quarRoot := filepath.Join(active, base, "quarantine")
	t.Cleanup(func() {
		_ = os.RemoveAll(libRoot)
		_ = os.RemoveAll(filepath.Join(active, base))
	})

	src := filepath.Join(libRoot, "Movies", "Some Film (1999)", "film.mkv")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	original := bytes.Repeat([]byte("old"), 1<<17) // 384 KiB
	if err := os.WriteFile(src, original, 0o644); err != nil {
		t.Fatal(err)
	}

	// The pipeline writes the new encode beside the target so the rename is
	// atomic within the directory.
	tmp := filepath.Join(filepath.Dir(src), ".messer-1.tmp")
	replacement := bytes.Repeat([]byte("new"), 1<<16)
	if err := os.WriteFile(tmp, replacement, 0o644); err != nil {
		t.Fatal(err)
	}

	q, err := Quarantine(src, libRoot, quarRoot, time.Date(2026, 11, 7, 1, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("cross-volume quarantine failed: %v", err)
	}
	if !strings.Contains(q, string(filepath.Separator)+"2026-11-07"+string(filepath.Separator)) {
		t.Errorf("quarantine path is not in a dated directory: %q", q)
	}
	if !strings.HasPrefix(q, quarRoot) {
		t.Errorf("quarantined to %q, want a path under %q", q, quarRoot)
	}

	// Before the swap the original must still be there and the copy must be
	// complete: that redundancy is the whole point of quarantining first.
	if got, err := os.ReadFile(src); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("the original is not intact after quarantining (err=%v)", err)
	}
	if got, err := os.ReadFile(q); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("the quarantined copy does not match the original (err=%v)", err)
	}

	if err := Replace(tmp, src); err != nil {
		t.Fatalf("replace failed: %v", err)
	}

	if got, err := os.ReadFile(src); err != nil || !bytes.Equal(got, replacement) {
		t.Errorf("the library file was not replaced by the new encode (err=%v)", err)
	}
	if got, err := os.ReadFile(q); err != nil || !bytes.Equal(got, original) {
		t.Errorf("the quarantine did not survive the swap (err=%v)", err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("the temporary file is still present")
	}
}

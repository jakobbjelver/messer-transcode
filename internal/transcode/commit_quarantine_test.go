package transcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

// fakeReporter satisfies Reporter without a database.
type fakeReporter struct {
	finished bool
	failed   string
}

func (f *fakeReporter) SetState(context.Context, int64, string) error              { return nil }
func (f *fakeReporter) SetProgress(context.Context, int64, float64, float64) error { return nil }
func (f *fakeReporter) Event(context.Context, string, string, int64, string, string) error {
	return nil
}
func (f *fakeReporter) Skip(context.Context, int64, string, string) error { return nil }
func (f *fakeReporter) Finish(context.Context, int64, int64, int64, float64) error {
	f.finished = true
	return nil
}
func (f *fakeReporter) Fail(_ context.Context, _ int64, code, _ string, _ int) (string, error) {
	f.failed = code
	return "failed", nil
}

// commitFixture lays out a library file, its encoded output in scratch, and a
// quarantine root on another path.
func commitFixture(t *testing.T, quarantine string) (config.Config, Task, *fakeReporter) {
	t.Helper()
	root := t.TempDir()
	lib := filepath.Join(root, "library")
	scratch := filepath.Join(root, "scratch")
	q := filepath.Join(root, "quarantine")
	for _, d := range []string{lib, scratch, q} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	film := filepath.Join(lib, "film.mkv")
	write(t, film, "original")
	write(t, filepath.Join(scratch, "7.out.mkv"), "encoded")

	cfg := config.Config{
		Paths:  config.Paths{Scratch: scratch, Quarantine: q},
		Safety: config.Safety{Quarantine: quarantine},
	}
	return cfg, Task{ID: 7, Path: film, LibraryRoot: lib, Decision: "encode"}, &fakeReporter{}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCommitQuarantinesWhenEnabled(t *testing.T) {
	cfg, task, rep := commitFixture(t, "")
	opts := Options{Config: &cfg, Reporter: rep}
	var res Result

	if err := Commit(context.Background(), opts, task, &res, 7); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res.Quarantine == "" {
		t.Fatal("the default config must quarantine the original")
	}
	if got := readFile(t, res.Quarantine); got != "original" {
		t.Fatalf("quarantined copy = %q, want the original bytes", got)
	}
	if got := readFile(t, task.Path); got != "encoded" {
		t.Fatalf("library file = %q, want the encoded bytes", got)
	}
	if !rep.finished {
		t.Error("a successful commit must report Finish")
	}
}

func TestCommitSkipsQuarantineWhenOff(t *testing.T) {
	cfg, task, rep := commitFixture(t, "off")
	opts := Options{Config: &cfg, Reporter: rep}
	var res Result

	if err := Commit(context.Background(), opts, task, &res, 7); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res.Quarantine != "" {
		t.Fatalf("quarantine off must not name a copy, got %q", res.Quarantine)
	}
	entries, err := os.ReadDir(cfg.Paths.Quarantine)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quarantine off wrote %d entries, want none", len(entries))
	}
	if got := readFile(t, task.Path); got != "encoded" {
		t.Fatalf("library file = %q, want the encoded bytes", got)
	}
	if rep.failed != "" {
		t.Fatalf("commit failed with %s", rep.failed)
	}
	if !rep.finished {
		t.Error("a successful commit must report Finish")
	}
}

func TestCommitQuarantineOffStillLeavesOriginalOnRenameFailure(t *testing.T) {
	cfg, task, _ := commitFixture(t, "off")
	opts := Options{Config: &cfg, Reporter: &fakeReporter{}}
	var res Result

	// Make the swap impossible: a directory where the temp file must go.
	if err := os.Mkdir(filepath.Join(filepath.Dir(task.Path), ".messer-7.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := Commit(context.Background(), opts, task, &res, 7)
	if err == nil {
		t.Fatal("commit should fail when the temp path cannot be created")
	}
	if got := readFile(t, task.Path); got != "original" {
		t.Fatalf("library file = %q, want the untouched original", got)
	}
}

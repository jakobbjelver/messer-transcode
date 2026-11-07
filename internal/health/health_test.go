package health

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	scratch := filepath.Join(dir, "scratch")
	quarantine := filepath.Join(dir, "quarantine")
	lib := filepath.Join(dir, "library")
	for _, p := range []string{scratch, quarantine, lib} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Config{
		Version:   1,
		Libraries: []config.Library{{Name: "movies", Root: lib, Extensions: []string{".mkv"}}},
		Schedule: config.Schedule{
			Weekday: "00:30-05:30", Weekend: "00:30-07:30",
			DrainBeforeMinutes: 15, Timezone: "Europe/Copenhagen",
		},
		Paths:  config.Paths{Scratch: scratch, Quarantine: quarantine},
		Safety: config.Safety{MinFreeGB: 1},
	}
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// A system with a valid config, a readable database and nothing queued is ok.
func TestHealthyReportsOK(t *testing.T) {
	cfg := testConfig(t)
	st := testStore(t)
	rep := Collect(context.Background(), In{
		Config: cfg, Store: st, Role: RoleNode, SkipHostChecks: true, Now: time.Now(),
	})
	if rep.Status != OK {
		t.Fatalf("status = %s, want ok; checks: %+v", rep.Status, rep.Checks)
	}
	if rep.Version["version"] == "" {
		t.Error("version block is empty")
	}
}

// The failure that started this project: work queued, nobody running, and the
// old health check reporting green.
func TestQueuedWorkWithSilentNodeIsBroken(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	st := testStore(t)

	if _, _, err := st.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/media/A.mkv", Size: 1_000_000_000, MtimeNS: 1},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Queue(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetDecision(ctx, rows[0].ID, store.Decision{
		Decision: "encode", Code: "R_H264_ENCODE", KeyKbps: 6000, State: "planned",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`DELETE FROM events`); err != nil {
		t.Fatal(err)
	}

	rep := Collect(ctx, In{Config: cfg, Store: st, Role: RoleServer, Now: time.Now()})
	if rep.Status != Broken {
		t.Fatalf("status = %s, want broken", rep.Status)
	}
	if code := firstCode(rep); code != "E_NODE_SILENT" {
		t.Errorf("first failing code = %q, want E_NODE_SILENT", code)
	}
}

// A broken config reports one cause, not a cascade of dependent failures.
func TestBrokenConfigReportsOneCause(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, loadErr := config.Load(path)
	rep := Collect(context.Background(), In{
		ConfigPath: path, OpenError: loadErr, Store: testStore(t), Role: RoleNode,
		SkipHostChecks: true, Now: time.Now(),
	})
	if rep.Status != Broken {
		t.Fatalf("status = %s, want broken", rep.Status)
	}
	if code := firstCode(rep); code != "E_CONFIG_INVALID" {
		t.Errorf("first code = %q, want E_CONFIG_INVALID", code)
	}
	var nonOK int
	for _, c := range rep.Checks {
		if c.Status != OK {
			nonOK++
		}
	}
	if nonOK != 1 {
		t.Errorf("%d failing checks, want exactly 1: %+v", nonOK, rep.Checks)
	}
}

// Missing tools are one finding, not two.
func TestMissingBinariesIsOneFinding(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	rep := Collect(context.Background(), In{
		Config: testConfig(t), Store: testStore(t), Role: RoleNode, Now: time.Now(),
	})
	if rep.Status != Broken {
		t.Fatalf("status = %s, want broken", rep.Status)
	}
	if code := firstCode(rep); code != "E_BINARY_MISSING" {
		t.Errorf("first code = %q, want E_BINARY_MISSING", code)
	}
	for _, c := range rep.Checks {
		if c.Name == "encoder" {
			t.Error("the encoder check ran without a binary; it can only repeat the same failure")
		}
	}
}

// The dependency sweep must not flag the binary as its own dependency, and
// must not flag the dyld shared cache, which has no files on disk.
func TestMissingDepsIgnoresHeaderAndSystemLibs(t *testing.T) {
	if _, err := os.Stat("/usr/bin/true"); err != nil {
		t.Skip("no /usr/bin/true to inspect")
	}
	if missing := missingDeps("/usr/bin/true"); len(missing) > 0 {
		t.Errorf("system binary reported missing deps: %v", missing)
	}
}

// A stale config on disk is caught even when the process loaded a good one at
// startup: this is the "edited the file and forgot to say so" case.
func TestStaleConfigOnDiskIsCaught(t *testing.T) {
	cfg := testConfig(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version": 99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := Collect(context.Background(), In{
		Config: cfg, ConfigPath: path, Store: testStore(t), Role: RoleServer, Now: time.Now(),
	})
	if code := firstCode(rep); code != "E_CONFIG_INVALID" {
		t.Errorf("first code = %q, want E_CONFIG_INVALID", code)
	}
}

func firstCode(rep Report) string {
	for _, c := range rep.Checks {
		if c.Status != OK {
			return c.Code
		}
	}
	return ""
}

func TestFold(t *testing.T) {
	ok := Check{Name: "a", Status: OK}
	warn := Check{Name: "b", Status: Degraded}
	bad := Check{Name: "c", Status: Broken}
	cases := []struct {
		in   []Check
		want Status
	}{
		{[]Check{ok}, OK},
		{[]Check{ok, warn}, Degraded},
		{[]Check{warn, bad, ok}, Broken},
		{[]Check{}, OK},
	}
	for _, c := range cases {
		if got := fold(c.in); got != c.want {
			t.Errorf("fold(%v) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestReportCarriesQueueDepth(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	st := testStore(t)
	if _, _, err := st.UpsertScanned(ctx, "movies", []media.Scanned{
		{Path: "/media/B.mkv", Size: 1_000_000_000, MtimeNS: 1},
	}); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.Queue(ctx, "", "", 10, 0)
	_ = st.SetDecision(ctx, rows[0].ID, store.Decision{
		Decision: "encode", Code: "R_H264_ENCODE", KeyKbps: 6000, State: "planned",
	})

	rep := Collect(ctx, In{Config: cfg, Store: st, Role: RoleNode, SkipHostChecks: true, Now: time.Now()})
	if rep.Queued != 1 {
		t.Errorf("queued = %d, want 1", rep.Queued)
	}
	if !strings.Contains(strings.Join(codes(rep), ","), "E_NODE_SILENT") {
		t.Errorf("expected the silent-node finding, got %v", codes(rep))
	}
}

func codes(rep Report) []string {
	var out []string
	for _, c := range rep.Checks {
		if c.Code != "" {
			out = append(out, c.Code)
		}
	}
	return out
}

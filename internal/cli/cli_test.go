package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

func writeFile(path string, data []byte) error { return os.WriteFile(path, data, 0o644) }

func run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestValidateExample(t *testing.T) {
	path, err := filepath.Abs("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	code, out, stderr := run("validate", path)
	if code != ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("stdout = %q", out)
	}
}

func TestValidateJSONOutput(t *testing.T) {
	path, err := filepath.Abs("../../config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("validate", path, "--json")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, `"ok": true`) {
		t.Errorf("stdout = %q", out)
	}
}

func TestValidateMissingFile(t *testing.T) {
	code, _, stderr := run("validate", "/nonexistent/config.json")
	if code != ExitRuntime {
		t.Fatalf("exit %d, want %d", code, ExitRuntime)
	}
	if stderr == "" {
		t.Error("expected an error message")
	}
}

func TestValidateInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := writeFile(path, []byte(`{"version": 1}`)); err != nil {
		t.Fatal(err)
	}
	code, out, _ := run("validate", path, "--json")
	if code != ExitInvalid {
		t.Fatalf("exit %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(out, `"problems"`) {
		t.Errorf("expected problems in %q", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run("frobnicate")
	if code != ExitUsage {
		t.Fatalf("exit %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestNoArgs(t *testing.T) {
	code, _, stderr := run()
	if code != ExitUsage {
		t.Fatalf("exit %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := run("version", "--json")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, `"version"`) {
		t.Errorf("stdout = %q", out)
	}
}

func TestSchemaIsJSON(t *testing.T) {
	code, out, _ := run("schema")
	if code != ExitOK {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("schema output does not look like JSON")
	}
}

// The node swaps its config without a restart. What matters is that an
// identical reload is a no-op (no event storm) and a real one lands.
func TestLiveConfigSwap(t *testing.T) {
	first := &config.Config{Version: 1, Paths: config.Paths{Scratch: "/a", Quarantine: "/b"}}
	live := &liveConfig{cur: first}
	if got := live.get(); got != first {
		t.Fatal("get did not return the config it was built with")
	}

	same := &config.Config{Version: 1, Paths: config.Paths{Scratch: "/a", Quarantine: "/b"}}
	if live.set(same) {
		t.Fatal("an identical config reported a change")
	}
	if live.get() != first {
		t.Fatal("an identical config replaced the pointer")
	}

	changed := &config.Config{Version: 1, Paths: config.Paths{Scratch: "/c", Quarantine: "/b"}}
	if !live.set(changed) {
		t.Fatal("a real change reported no change")
	}
	if live.get().Paths.Scratch != "/c" {
		t.Fatalf("scratch = %q, want /c", live.get().Paths.Scratch)
	}
}

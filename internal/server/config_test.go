package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

const baseConfigJSON = `{
  "version": 1,
  "libraries": [{"name": "movies", "root": "/media", "extensions": [".mkv"]}],
  "schedule": {"weekday": "00:00-23:59", "weekend": "00:00-23:59", "drain_before_minutes": 0, "timezone": "UTC"},
  "workers": {"encode": 1, "probe": 2, "ingest_buffer": 2},
  "paths": {"scratch": "/tmp/scratch", "quarantine": "/tmp/q"},
  "quality": {
    "mode": "table",
    "vmaf_target": 95,
    "vmaf_tolerance": 1,
    "table": [
      {"codec": "h264", "height_gte": 1080, "kbps": 6000, "expected_vmaf": 95},
      {"codec": "h264", "height_gte": 720, "kbps": 2700, "expected_vmaf": 92.9}
    ]
  },
  "rules": [
    {"when": {"codec": "h264", "bitrate_kbps_gte": 5000}, "do": "encode", "code": "R_H264_HIGH"},
    {"when": {"default": true}, "do": "skip", "code": "R_LEAVE_ALONE"}
  ],
  "safety": {
    "min_free_gb": 20,
    "size_ratio_min": 0.5,
    "size_ratio_max": 0.95,
    "duration_tolerance_s": 2,
    "keep_quarantine_days": 3,
    "verify_sample_seconds": 60,
    "verify_vmaf": true,
    "max_attempts": 3
  },
  "server": {"listen": ":8099"}
}`

// newConfigServer is a server with a real config file, a seeded baseline
// revision, and nothing else: the config surface has no use for queued files.
func newConfigServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(baseConfigJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Decode([]byte(baseConfigJSON))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Token = testToken
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := New(cfg, st, path, path, nil)
	if err := s.SeedConfigRevision(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func post(t *testing.T, s *Server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)))
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("body is not JSON (%d): %s", rec.Code, rec.Body.String())
	}
}

// A first run has a revision, so the first save has something to diff against.
func TestConfigSeedsARevision(t *testing.T) {
	s, _ := newConfigServer(t)
	rec := get(t, s, "/api/v1/config/history")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Revisions []store.Revision `json:"revisions"`
		State     configState      `json:"state"`
	}
	decode(t, rec, &out)
	if len(out.Revisions) != 1 || out.Revisions[0].Actor != "boot" {
		t.Fatalf("revisions = %+v", out.Revisions)
	}
	if out.State.Dirty != "" {
		t.Fatalf("dirty = %q after seeding, want clean", out.State.Dirty)
	}
	if out.State.Applies != "live" {
		t.Fatalf("applies = %q, want live", out.State.Applies)
	}
}

// The dry run is the same validation as the write: it must refuse a bad ladder
// and name the leaf, and it must not touch the file.
func TestConfigDryRunRejectsAndDoesNotWrite(t *testing.T) {
	s, path := newConfigServer(t)
	before, _ := os.ReadFile(path)

	bad := strings.Replace(baseConfigJSON, `"kbps": 6000`, `"kbps": 0`, 1)
	rec := post(t, s, "/api/v1/config/dry-run", map[string]any{"config": json.RawMessage(bad)})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK       bool             `json:"ok"`
		Problems []config.Problem `json:"problems"`
	}
	decode(t, rec, &out)
	if out.OK || len(out.Problems) == 0 {
		t.Fatalf("a zero bitrate should be refused: %+v", out)
	}
	if !strings.Contains(out.Problems[0].Path, "quality/table/0/kbps") {
		t.Fatalf("problem path = %q, want the leaf", out.Problems[0].Path)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("a dry run wrote the config file")
	}
}

func TestConfigSaveWritesRevisionAndFile(t *testing.T) {
	s, path := newConfigServer(t)
	next := strings.Replace(baseConfigJSON, `"kbps": 6000`, `"kbps": 6200`, 1)

	rec := post(t, s, "/api/v1/config", map[string]any{
		"config": json.RawMessage(next), "base_rev": 1, "message": "raise the 1080p tier a little",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Rev    int64  `json:"rev"`
		SHA    string `json:"sha"`
		Config struct {
			Quality struct{ Table []struct{ Kbps int } } `json:"quality"`
		} `json:"config"`
	}
	decode(t, rec, &out)
	if out.Rev != 2 {
		t.Fatalf("rev = %d, want 2", out.Rev)
	}
	if out.Config.Quality.Table[0].Kbps != 6200 {
		t.Fatal("the response does not carry the saved config")
	}

	// The file is the saved document, and the running config is it too.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "6200") {
		t.Fatal("the config file was not written")
	}
	if s.config().Quality.Table[0].Kbps != 6200 {
		t.Fatal("the running config was not swapped")
	}
	if s.config().Server.Token != testToken {
		t.Fatal("the node token was dropped by the reload")
	}

	// A second save from the stale revision is a conflict, not an overwrite.
	conflict := post(t, s, "/api/v1/config", map[string]any{
		"config": json.RawMessage(next), "base_rev": 1, "message": "again",
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", conflict.Code, conflict.Body.String())
	}
	var c struct {
		Error struct{ Code string } `json:"error"`
		Diff  struct {
			Summary struct{ Changed int } `json:"summary"`
		} `json:"diff"`
	}
	decode(t, conflict, &c)
	if c.Error.Code != "E_CONFLICT" {
		t.Fatalf("code = %q", c.Error.Code)
	}
}

// A hand edit is visible as dirty, and adopting it records it.
func TestConfigHandEditIsDirtyUntilAdopted(t *testing.T) {
	s, path := newConfigServer(t)
	edited := strings.Replace(baseConfigJSON, `"encode": 1`, `"encode": 2`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	s.pollConfig()
	if got := s.config().Workers.Encode; got != 2 {
		t.Fatalf("the running config did not follow the file: encode=%d", got)
	}
	state := s.configState(context.Background())
	if state.Dirty != "edited" {
		t.Fatalf("dirty = %q, want edited", state.Dirty)
	}

	rec := post(t, s, "/api/v1/config/adopt", map[string]any{"message": "raised the encode workers by hand"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	after := s.configState(context.Background())
	if after.Dirty != "" || after.HeadRev != 2 {
		t.Fatalf("state after adopt = %+v", after)
	}

	// A file that does not validate is dirty *and* not adopted.
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s.pollConfig()
	if got := s.config().Workers.Encode; got != 2 {
		t.Fatalf("an invalid file replaced the running config: encode=%d", got)
	}
	if st := s.configState(context.Background()); st.Dirty != "invalid" || st.DiskError == "" {
		t.Fatalf("state with an invalid file = %+v", st)
	}
}

func TestConfigRevertIsANewRevision(t *testing.T) {
	s, path := newConfigServer(t)
	next := strings.Replace(baseConfigJSON, `"kbps": 6000`, `"kbps": 6200`, 1)
	if rec := post(t, s, "/api/v1/config", map[string]any{
		"config": json.RawMessage(next), "base_rev": 1, "message": "bump",
	}); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}

	rec := post(t, s, "/api/v1/config/revert/1", map[string]any{"message": "put the ladder back"})
	if rec.Code != http.StatusOK {
		t.Fatalf("revert: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Rev int64 `json:"rev"`
	}
	decode(t, rec, &out)
	if out.Rev != 3 {
		t.Fatalf("rev = %d, want 3 — a revert appends", out.Rev)
	}
	if s.config().Quality.Table[0].Kbps != 6000 {
		t.Fatal("the reverted values are not running")
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"kbps": 6000`) {
		t.Fatal("the file was not rewritten to the old revision")
	}
}

// Saving the document that is already head is not an event.
func TestConfigSaveIsIdempotent(t *testing.T) {
	s, path := newConfigServer(t)
	before, _ := os.ReadFile(path)

	rec := post(t, s, "/api/v1/config", map[string]any{
		"config": json.RawMessage(baseConfigJSON), "base_rev": 1, "message": "noop",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Rev       int64 `json:"rev"`
		Unchanged bool  `json:"unchanged"`
	}
	decode(t, rec, &out)
	if !out.Unchanged || out.Rev != 1 {
		t.Fatalf("an identical save should be a no-op: %+v", out)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("an identical save rewrote the file")
	}
	if revs, _ := s.st.ConfigRevisions(context.Background(), 10); len(revs) != 1 {
		t.Fatalf("an identical save added a revision: %d", len(revs))
	}
}

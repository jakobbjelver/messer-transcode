package transcode

import (
	"context"
	"os"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
)

// TestPreflightOnRealFile runs the real sample-and-measure path on the file in
// TRANSCODE_PREFLIGHT_FILE. It is the only test that exercises ffmpeg here —
// the decision itself is unit-tested in preflight_test.go — and it exists so a
// claim about what the guard measures can be checked against a real encode:
//
//	TRANSCODE_PREFLIGHT_FILE='.../If I Had Legs...mkv' go test ./internal/transcode/ -run TestPreflightOnRealFile -v
func TestPreflightOnRealFile(t *testing.T) {
	path := os.Getenv("TRANSCODE_PREFLIGHT_FILE")
	if path == "" {
		t.Skip("set TRANSCODE_PREFLIGHT_FILE to a real media file")
	}
	cfg := &config.Config{Paths: config.Paths{Scratch: t.TempDir()}}
	ctx := context.Background()

	probe, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	score, ok := preflight(ctx, cfg, Task{ID: 1, Path: path}, path, probe, 6000, 60)
	if !ok {
		t.Fatal("preflight could not measure")
	}
	t.Logf("sample score %.2f over %.0fs at 6000 kbps", score, probe.DurationS)
	if score <= 0 {
		t.Fatalf("score = %.2f, want a real measurement", score)
	}
}

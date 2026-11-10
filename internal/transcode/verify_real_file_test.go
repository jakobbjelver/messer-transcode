package transcode

import (
	"context"
	"os"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/ffmpeg"
)

// TestVerifyDecodeOnRealFile is the gate the unit tests cannot be: it runs the
// real ffmpeg against a real file and reads the real exit code. Set
// TRANSCODE_REAL_FILE to a path and run:
//
//	TRANSCODE_REAL_FILE="/Volumes/Media/..." go test ./internal/transcode -run RealFile -v
//
// A file whose PTS equals its DTS is the interesting case: it is exactly what
// the null muxer complains about while decoding perfectly.
func TestVerifyDecodeOnRealFile(t *testing.T) {
	path := os.Getenv("TRANSCODE_REAL_FILE")
	if path == "" {
		t.Skip("set TRANSCODE_REAL_FILE to run the real decode check")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	ctx := context.Background()
	probe, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.DurationS <= 0 {
		t.Fatalf("no duration from %s", path)
	}
	if err := verifyDecode(ctx, path, probe.DurationS, 60); err != nil {
		t.Fatalf("verifyDecode(%s) = %v, want it to decode cleanly", path, err)
	}
}

package transcode

import (
	"math"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

// The size band guards against a truncated or bloated output, not against a
// good saving. On a high-bitrate source the ladder target projects well below
// the fixed floor, so the encode that returns the biggest saving in the library
// is the one the band rejects — after a full encode, three times.
//
// Measured 2026-11-15: City Lights, 10,472 kbps H.264 at the 2,700 rung,
// projected ratio 0.26, encoded at 0.324, rejected as "too small".
func TestRatioBandFollowsTheProjectionDown(t *testing.T) {
	cfg := &config.Config{Safety: config.Safety{SizeRatioMin: 0.35, SizeRatioMax: 0.98}}

	band := ratioBand(cfg, "encode", 2700, 12087)
	want := 0.7 * 2700.0 / 12087.0
	if math.Abs(band.Min-want) > 1e-6 {
		t.Errorf("min = %.4f, want %.4f (0.7 of the projection)", band.Min, want)
	}
	if band.Max != 0.98 {
		t.Errorf("max = %.2f, want 0.98", band.Max)
	}
}

// A file that projects inside the configured band keeps the configured floor:
// the rule only ever relaxes, it never tightens.
func TestRatioBandKeepsTheConfiguredFloorWhenTheProjectionFits(t *testing.T) {
	cfg := &config.Config{Safety: config.Safety{SizeRatioMin: 0.35, SizeRatioMax: 0.98}}

	band := ratioBand(cfg, "encode", 6000, 7000)
	if band.Min != 0.35 {
		t.Errorf("min = %.2f, want 0.35 (projection 0.86 is inside the band)", band.Min)
	}
}

// An absurd projection must not disable the truncation guard: a fifth of the
// source is already suspicious, a fiftieth is a broken encode.
func TestRatioBandHasAnAbsoluteFloor(t *testing.T) {
	cfg := &config.Config{Safety: config.Safety{SizeRatioMin: 0.35, SizeRatioMax: 0.98}}

	band := ratioBand(cfg, "encode", 960, 30000)
	if band.Min != minRatioFloor {
		t.Errorf("min = %.4f, want the absolute floor %.2f", band.Min, minRatioFloor)
	}
}

// A source with no measured bitrate cannot project anything; the configured
// band is the only honest answer.
func TestRatioBandWithoutASourceBitrate(t *testing.T) {
	cfg := &config.Config{Safety: config.Safety{SizeRatioMin: 0.35, SizeRatioMax: 0.98}}

	band := ratioBand(cfg, "encode", 6000, 0)
	if band.Min != 0.35 || band.Max != 0.98 {
		t.Errorf("band = %.2f–%.2f, want 0.35–0.98", band.Min, band.Max)
	}
}

// A remux does not encode: it should be roughly the same size, and the encode
// bands would reject it for not shrinking.
func TestRatioBandForARemux(t *testing.T) {
	cfg := &config.Config{Safety: config.Safety{SizeRatioMin: 0.35, SizeRatioMax: 0.98}}

	band := ratioBand(cfg, "remux", 0, 12087)
	if band.Min != 0.80 || band.Max != 1.15 {
		t.Errorf("band = %.2f–%.2f, want 0.80–1.15", band.Min, band.Max)
	}
}

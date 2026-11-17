package planner

import (
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

func TestNominalHeight(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		want          int
	}{
		{"4K, letterboxed", 3840, 1600, 2160},
		{"1080p, letterboxed 2.39:1", 1920, 800, 1080},
		{"1080p, letterboxed 1.85:1", 1920, 1040, 1080},
		{"1080p, full frame", 1920, 1080, 1080},
		{"720p", 1280, 720, 720},
		{"720p-class in a wider frame", 1280, 544, 720},
		{"SD 576", 576, 432, 432},
		{"no width recorded", 0, 800, 800},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NominalHeight(tc.width, tc.height); got != tc.want {
				t.Fatalf("NominalHeight(%d, %d) = %d, want %d", tc.width, tc.height, got, tc.want)
			}
		})
	}
}

// TestLetterboxed1080pGetsThe1080Rung is the regression test for a mistake that
// would have encoded half the library at 2,700 kbps: 84 % of the frames below
// 1080 lines are 1920-wide 1080p masters with a letterbox, not 720p.
func TestLetterboxed1080pGetsThe1080Rung(t *testing.T) {
	cfg := exampleConfig(t)
	f := media.Facts{
		Width: 1920, Height: 800, Codec: "h264", BitrateKbps: 8000,
		AudioTracks: 1, AudioCodecs: []string{"eac3"},
	}
	v := Decide(cfg, f)
	if v.Decision != "encode" {
		t.Fatalf("decision = %s, want encode", v.Decision)
	}
	if v.TargetKbps != 6000 {
		t.Fatalf("target = %d kbps, want the 1080p rung's 6000 (height 800 is a letterbox, not 720p)", v.TargetKbps)
	}
}

func TestTrue720pGetsThe720Rung(t *testing.T) {
	cfg := exampleConfig(t)
	f := media.Facts{
		Width: 1280, Height: 720, Codec: "h264", BitrateKbps: 6000,
		AudioTracks: 1, AudioCodecs: []string{"ac3"},
	}
	if v := Decide(cfg, f); v.TargetKbps != 2700 {
		t.Fatalf("target = %d kbps, want 2700 for 1280×720", v.TargetKbps)
	}
}

func TestSDGetsTheBottomRung(t *testing.T) {
	cfg := exampleConfig(t)
	f := media.Facts{
		Width: 576, Height: 432, Codec: "h264", BitrateKbps: 2000,
		AudioTracks: 1, AudioCodecs: []string{"aac"},
	}
	if v := Decide(cfg, f); v.TargetKbps != 960 {
		t.Fatalf("target = %d kbps, want 960 for 576×432", v.TargetKbps)
	}
}

// A narrow frame is the case the width rule gets backwards. A 1,436x1,080 4:3
// master is 1080p: its width is small because the picture is narrow, not
// because it was mastered small. Reading it as 720p priced it at 2,700 kbps,
// where Following (1999) scored VMAF 25.5 and parked (2026-11-17).
func TestNarrowFrameUsesItsHeight(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
		want          int
	}{
		{"4:3 1080p master", 1436, 1080, 1080},
		{"4:3 1080p, exact", 1440, 1080, 1080},
		{"1.48:1 1080p", 1600, 1080, 1080},
		{"4:3 4K master", 2880, 2160, 2160},
		{"4:3 SD stays SD", 576, 432, 432},
		{"16:9 is still read by width", 1920, 1080, 1080},
		{"a letterbox is still read by width", 1920, 800, 1080},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NominalHeight(tc.width, tc.height); got != tc.want {
				t.Fatalf("NominalHeight(%d, %d) = %d, want %d", tc.width, tc.height, got, tc.want)
			}
		})
	}
}

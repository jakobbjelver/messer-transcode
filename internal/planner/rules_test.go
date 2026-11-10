package planner

import (
	"strings"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
)

func exampleConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load("../../config.example.json")
	if err != nil {
		t.Fatalf("load example config: %v", err)
	}
	return cfg
}

func TestDecide(t *testing.T) {
	cfg := exampleConfig(t)

	for _, tc := range []struct {
		name   string
		facts  media.Facts
		want   string
		code   string
		target int
	}{
		{
			name:  "1080p H.264 at 7.8 Mbps is re-encoded",
			facts: media.Facts{Height: 1080, Codec: "h264", BitrateKbps: 7796},
			want:  "encode", code: "R_H264_ENCODE", target: 6000,
		},
		{
			name:  "1080p H.264 already below the target is left alone",
			facts: media.Facts{Height: 1080, Codec: "h264", BitrateKbps: 3000},
			want:  "skip", code: "R_ALREADY_LEAN",
		},
		{
			name:  "a marginal saving is not worth an encode",
			facts: media.Facts{Height: 1080, Codec: "h264", BitrateKbps: 6500},
			want:  "skip", code: "R_LOW_SAVING",
		},
		{
			name:  "efficient HEVC with clean audio is left alone",
			facts: media.Facts{Height: 1080, Codec: "hevc", BitrateKbps: 4800, AudioTracks: 1, AudioCodecs: []string{"eac3"}},
			want:  "skip", code: "R_NO_RULE",
		},
		{
			name:  "over-bitrate HEVC is re-encoded",
			facts: media.Facts{Height: 1080, Codec: "hevc", BitrateKbps: 9000},
			want:  "encode", code: "R_HEVC_OVERBITRATE", target: 6000,
		},
		{
			name:  "HEVC in the middle band is left alone",
			facts: media.Facts{Height: 1080, Codec: "hevc", BitrateKbps: 6000},
			want:  "skip", code: "R_NO_RULE",
		},
		{
			name:  "720p is never re-encoded, and no longer remuxed for nothing",
			facts: media.Facts{Height: 720, Codec: "h264", BitrateKbps: 20000, AudioTracks: 1, AudioCodecs: []string{"ac3"}},
			want:  "skip", code: "R_LOW_RES_KEEP",
		},
		{
			name:  "720p with DTS audio is remuxed for the audio alone",
			facts: media.Facts{Height: 720, Codec: "h264", BitrateKbps: 20000, AudioTracks: 2, AudioCodecs: []string{"dts", "ac3"}},
			want:  "remux", code: "R_LOW_RES_AUDIO",
		},
		{
			name:  "4K picks the 4K rung, not the 1080p one",
			facts: media.Facts{Height: 2160, Codec: "hevc", BitrateKbps: 20000},
			want:  "encode", code: "R_HEVC_OVERBITRATE", target: 15600,
		},
		{
			name:  "4K H.264 uses its own rung",
			facts: media.Facts{Height: 2160, Codec: "h264", BitrateKbps: 30000},
			want:  "encode", code: "R_H264_ENCODE", target: 15600,
		},
		{
			name:  "unknown codec falls through to skip",
			facts: media.Facts{Height: 1080, Codec: "av1", BitrateKbps: 9000},
			want:  "skip", code: "R_NO_RULE",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(cfg, tc.facts)
			if got.Decision != tc.want || got.Code != tc.code {
				t.Fatalf("Decide(%+v) = %s/%s, want %s/%s",
					tc.facts, got.Decision, got.Code, tc.want, tc.code)
			}
			if got.TargetKbps != tc.target {
				t.Errorf("target = %d, want %d", got.TargetKbps, tc.target)
			}
			if got.Reason == "" {
				t.Error("every verdict needs a human-readable reason")
			}
		})
	}
}

// TestDecisionIsTotal sweeps a plausible grid: the rule list must always
// produce a verdict, because the last rule is required to be the default.
func TestDecisionIsTotal(t *testing.T) {
	cfg := exampleConfig(t)
	codecs := []string{"h264", "hevc", "av1", "mpeg2video", "vc1", ""}
	heights := []int{0, 480, 576, 720, 1080, 2160}
	bitrates := []int{0, 800, 2500, 4999, 5000, 7999, 8000, 20000}

	for _, c := range codecs {
		for _, h := range heights {
			for _, b := range bitrates {
				v := Decide(cfg, media.Facts{Height: h, Codec: c, BitrateKbps: b})
				if v.Decision == "" || v.Code == "" {
					t.Fatalf("no verdict for %s/%d/%d", c, h, b)
				}
				if v.Decision == "encode" && v.TargetKbps == 0 {
					t.Fatalf("encode without a target for %s/%d/%d", c, h, b)
				}
			}
		}
	}
}

// TestReEncodedFileIsSkipped guards the property that matters most: running the
// planner twice must not re-encode what it just produced.
func TestReEncodedFileIsSkipped(t *testing.T) {
	cfg := exampleConfig(t)
	original := media.Facts{Height: 1080, Codec: "h264", BitrateKbps: 7796}

	d := Decide(cfg, original)
	if d.Decision != "encode" {
		t.Fatalf("expected an encode, got %s", d.Decision)
	}

	after := media.Facts{Height: 1080, Codec: "hevc", BitrateKbps: d.TargetKbps}
	if again := Decide(cfg, after); again.Decision == "encode" {
		t.Fatalf("the planner would re-encode its own output: %+v → %+v", after, again)
	}
}

func TestTargetKbps(t *testing.T) {
	cfg := exampleConfig(t)
	for _, tc := range []struct {
		codec  string
		height int
		want   int
		ok     bool
	}{
		{"h264", 2160, 15600, true},
		{"h264", 1080, 6000, true},
		{"h264", 999, 2700, true},
		{"h264", 480, 960, true},
		{"hevc", 2160, 15600, true},
		{"hevc", 1080, 6000, true},
		{"hevc", 400, 960, true},
		{"av1", 1080, 0, false},
	} {
		got, ok := TargetKbps(cfg, tc.codec, tc.height)
		if ok != tc.ok || got != tc.want {
			t.Errorf("TargetKbps(%s, %d) = %d,%v want %d,%v", tc.codec, tc.height, got, ok, tc.want, tc.ok)
		}
	}
}

func TestReasonMentionsBothNumbers(t *testing.T) {
	cfg := exampleConfig(t)
	v := Decide(cfg, media.Facts{Height: 1080, Codec: "h264", BitrateKbps: 7796})
	if v.Reason == "" {
		t.Fatal("empty reason")
	}
	for _, want := range []string{"H.264", "7796", "6000"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("reason %q should mention %q", v.Reason, want)
		}
	}
}

func TestParseTMDBID(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int64
		ok   bool
	}{
		{"/media/movies/Babylon (2022) {tmdb-615777}/Babylon.mkv", 615777, true},
		{"/media/tv/Show (2019) {tmdb-82856}/Season 01/e01.mkv", 82856, true},
		{"/media/movies/No Tag (2020)/x.mkv", 0, false},
		{"/media/movies/Broken {tmdb-}/x.mkv", 0, false},
	} {
		got := ParseTMDBID(tc.path)
		if tc.ok {
			if got == nil || *got != tc.want {
				t.Errorf("ParseTMDBID(%q) = %v, want %d", tc.path, got, tc.want)
			}
			continue
		}
		if got != nil {
			t.Errorf("ParseTMDBID(%q) = %v, want nil", tc.path, *got)
		}
	}
}

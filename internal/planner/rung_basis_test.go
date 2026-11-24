package planner

import (
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// A rung is chosen for the picture, so the guard that refuses an upward encode
// has to read the picture's bitrate. The container's figure includes the audio
// we are about to copy, and on a low-bitrate source that share is the whole
// difference.
//
// Measured 2026-11-24: Trailer Park Boys S01E04 is 1,068 kbps in total with a
// 128 kbps E-AC3 track, so its video is ~940 kbps against a 960 kbps rung — an
// upward encode the planner called a 10 % saving. It passed R_ALREADY_LEAN and
// the 10 % floor, ran a full encode, and the verifier rejected the result at
// size_ratio 0.998.
func TestRungIsJudgedAgainstTheVideoShare(t *testing.T) {
	cfg := exampleConfig(t)

	got := Decide(cfg, media.Facts{
		Width: 512, Height: 384, Codec: "h264",
		BitrateKbps: 1068, AudioKbps: 128, AudioTracks: 1, AudioCodecs: []string{"eac3"},
	})
	if got.Decision != "skip" || got.Code != "R_ALREADY_LEAN" {
		t.Fatalf("decision = %s/%s, want skip/R_ALREADY_LEAN: 940 kbps of video under a 960 rung",
			got.Decision, got.Code)
	}
}

// The saving that pays for an encode is file bytes, so the projection has to
// count the audio that stays behind. 7,700 kbps with a 1,000 kbps track is a
// 9 % file saving at the 6,000 rung, under the 10 % floor, even though the
// video alone would show 10.4 %.
func TestSavingIsMeasuredOnTheFileNotTheVideo(t *testing.T) {
	cfg := exampleConfig(t)

	got := Decide(cfg, media.Facts{
		Height: 1080, Codec: "h264", BitrateKbps: 7700, AudioKbps: 1000,
	})
	if got.Decision != "skip" || got.Code != "R_LOW_SAVING" {
		t.Fatalf("decision = %s/%s, want skip/R_LOW_SAVING: the file only shrinks 9 %%",
			got.Decision, got.Code)
	}
}

// A file that is genuinely worth the encoder still gets it: 9,000 kbps with a
// 900 kbps track is 21 % of file bytes at the 6,000 rung.
func TestARealFileSavingStillEncodes(t *testing.T) {
	cfg := exampleConfig(t)

	got := Decide(cfg, media.Facts{
		Height: 1080, Codec: "h264", BitrateKbps: 9000, AudioKbps: 900,
	})
	if got.Decision != "encode" || got.TargetKbps != 6000 {
		t.Fatalf("decision = %s/%d, want encode at 6000", got.Decision, got.TargetKbps)
	}
}

// A probe that reports no audio bitrate must not change any verdict: the
// container figure is all the planner has, and it is what it always used.
func TestMissingAudioBitrateKeepsTheContainerBasis(t *testing.T) {
	cfg := exampleConfig(t)

	got := Decide(cfg, media.Facts{Height: 1080, Codec: "h264", BitrateKbps: 7796})
	if got.Decision != "encode" || got.Code != "R_H264_ENCODE" || got.TargetKbps != 6000 {
		t.Fatalf("decision = %s/%s/%d, want encode/R_H264_ENCODE/6000", got.Decision, got.Code, got.TargetKbps)
	}
}

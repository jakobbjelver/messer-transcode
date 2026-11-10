package planner

import (
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/media"
)

func intPtr(v int) *int { return &v }

// audioRules is the shape the live config now uses: a remux only when the
// audio policy would actually change something, otherwise the default skip.
func audioRules() *config.Config {
	return &config.Config{
		Rules: []config.Rule{
			{When: config.Condition{AudioCodecIn: []string{"truehd", "flac", "pcm_bluray", "dts"}}, Do: "remux", Code: "R_AUDIO_NEEDS_EAC3"},
			{When: config.Condition{AudioTracksGT: intPtr(3)}, Do: "remux", Code: "R_TOO_MANY_TRACKS"},
			{When: config.Condition{Default: true}, Do: "skip", Code: "R_NO_RULE"},
		},
	}
}

func TestAudioCodecConditionMatchesAnyTrack(t *testing.T) {
	cfg := audioRules()
	f := media.Facts{
		Codec: "hevc", Height: 1080, BitrateKbps: 4000,
		AudioTracks: 2, AudioCodecs: []string{"aac", "dts"},
	}
	v := Decide(cfg, f)
	if v.Decision != "remux" || v.Code != "R_AUDIO_NEEDS_EAC3" {
		t.Fatalf("got %s/%s, want remux/R_AUDIO_NEEDS_EAC3", v.Decision, v.Code)
	}
}

func TestAudioCodecConditionFallsThroughWhenNoMatch(t *testing.T) {
	cfg := audioRules()
	// This is the shape of 1,915 of the 1,929 files the old rules remuxed: eac3
	// audio already, nothing to fix, 5 GB to rewrite for nothing.
	f := media.Facts{
		Codec: "hevc", Height: 1080, BitrateKbps: 4000,
		AudioTracks: 1, AudioCodecs: []string{"eac3"},
	}
	v := Decide(cfg, f)
	if v.Decision != "skip" {
		t.Fatalf("got %s, want skip", v.Decision)
	}
}

func TestAudioTracksConditionUsesStrictlyGreater(t *testing.T) {
	cfg := audioRules()
	three := media.Facts{AudioTracks: 3, AudioCodecs: []string{"eac3", "aac", "ac3"}}
	if v := Decide(cfg, three); v.Decision != "skip" {
		t.Fatalf("three tracks must not match > 3, got %s", v.Decision)
	}
	four := media.Facts{AudioTracks: 4, AudioCodecs: []string{"eac3", "aac", "ac3", "ac3"}}
	if v := Decide(cfg, four); v.Code != "R_TOO_MANY_TRACKS" {
		t.Fatalf("four tracks = %s, want R_TOO_MANY_TRACKS", v.Code)
	}
}

func TestAudioConditionWithoutFactsDoesNotMatch(t *testing.T) {
	cfg := audioRules()
	// A file probed before the audio facts existed has no codecs recorded; the
	// rule must not fire on an empty list.
	f := media.Facts{Codec: "hevc", AudioTracks: 0}
	if v := Decide(cfg, f); v.Decision != "skip" {
		t.Fatalf("empty audio facts = %s, want skip", v.Decision)
	}
}

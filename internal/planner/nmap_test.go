package planner

import (
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

func probeWith(audio []media.AudioTrack, subs []media.SubTrack) media.Probe {
	return media.Probe{VideoIndex: 0, VideoCodec: "h264", Height: 1080, Audio: audio, Subs: subs}
}

func TestSelectStreamsKeepsConfiguredLanguagesInOrder(t *testing.T) {
	cfg := exampleConfig(t)
	p := probeWith([]media.AudioTrack{
		{Index: 1, Codec: "eac3", Lang: "dan", Channels: 6},
		{Index: 2, Codec: "eac3", Lang: "eng", Channels: 6},
		{Index: 3, Codec: "eac3", Lang: "swe", Channels: 2},
		{Index: 4, Codec: "aac", Lang: "ger", Channels: 2},
	}, nil)

	sp := SelectStreams(p, cfg, "")
	if len(sp.Audio) != 3 {
		t.Fatalf("kept %d tracks, want 3: %+v", len(sp.Audio), sp.Audio)
	}
	// eng is first in keep_languages, then swe, then dan.
	want := []string{"eng", "swe", "dan"}
	for i, w := range want {
		if sp.Audio[i].Lang != w {
			t.Errorf("track %d = %q, want %q", i, sp.Audio[i].Lang, w)
		}
	}
}

func TestSelectStreamsDropsCommentary(t *testing.T) {
	cfg := exampleConfig(t)
	p := probeWith([]media.AudioTrack{
		{Index: 1, Codec: "eac3", Lang: "eng", Channels: 6},
		{Index: 2, Codec: "aac", Lang: "eng", Channels: 2, Commentary: true, Title: "Director commentary"},
	}, nil)

	sp := SelectStreams(p, cfg, "")
	if len(sp.Audio) != 1 || sp.Audio[0].Index != 1 {
		t.Fatalf("commentary survived: %+v", sp.Audio)
	}
}

func TestSelectStreamsTranscodesLossless(t *testing.T) {
	cfg := exampleConfig(t)
	p := probeWith([]media.AudioTrack{
		{Index: 1, Codec: "truehd", Lang: "eng", Channels: 8},
		{Index: 2, Codec: "eac3", Lang: "eng", Channels: 6},
	}, nil)

	sp := SelectStreams(p, cfg, "")
	if len(sp.Audio) != 2 {
		t.Fatalf("tracks = %+v", sp.Audio)
	}
	if sp.Audio[0].TranscodeTo != "eac3" || sp.Audio[0].Kbps != 448 {
		t.Errorf("truehd should become eac3 448k, got %+v", sp.Audio[0])
	}
	if sp.Audio[1].TranscodeTo != "" {
		t.Errorf("eac3 should be copied, got %+v", sp.Audio[1])
	}
}

func TestSelectStreamsKeepsOriginalLanguage(t *testing.T) {
	cfg := exampleConfig(t)
	p := probeWith([]media.AudioTrack{
		{Index: 1, Codec: "eac3", Lang: "jpn", Channels: 6},
		{Index: 2, Codec: "eac3", Lang: "eng", Channels: 6},
	}, nil)

	sp := SelectStreams(p, cfg, "jpn")
	if len(sp.Audio) != 2 {
		t.Fatalf("tracks = %+v", sp.Audio)
	}
	if sp.Audio[0].Lang != "jpn" {
		t.Errorf("the film's own language should lead, got %q", sp.Audio[0].Lang)
	}
}

func TestSelectStreamsNeverStripsEverything(t *testing.T) {
	cfg := exampleConfig(t)
	p := probeWith([]media.AudioTrack{
		{Index: 1, Codec: "eac3", Lang: "jpn", Channels: 6},
	}, nil)

	sp := SelectStreams(p, cfg, "")
	if len(sp.Audio) != 1 {
		t.Fatalf("a Japanese-only film lost its only track: %+v", sp.Audio)
	}
}

func TestSelectStreamsCapsTracks(t *testing.T) {
	cfg := exampleConfig(t)
	cfg.Audio.MaxTracks = 2
	p := probeWith([]media.AudioTrack{
		{Index: 1, Codec: "eac3", Lang: "eng"},
		{Index: 2, Codec: "eac3", Lang: "swe"},
		{Index: 3, Codec: "eac3", Lang: "dan"},
	}, nil)

	if sp := SelectStreams(p, cfg, ""); len(sp.Audio) != 2 {
		t.Errorf("kept %d tracks, want the 2-track cap", len(sp.Audio))
	}
}

func TestSelectSubs(t *testing.T) {
	cfg := exampleConfig(t)
	p := probeWith(nil, []media.SubTrack{
		{Index: 4, Codec: "subrip", Lang: "eng"},
		{Index: 5, Codec: "subrip", Lang: "eng", HearingImpaired: true, Title: "English SDH"},
		{Index: 6, Codec: "subrip", Lang: "ger"},
		{Index: 7, Codec: "subrip", Lang: "jpn", Forced: true},
		{Index: 8, Codec: "subrip", Lang: "eng", Commentary: true},
	})

	got := SelectStreams(p, cfg, "").Subs
	want := []int{4, 7}
	if len(got) != len(want) {
		t.Fatalf("subs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("subs = %v, want %v", got, want)
		}
	}
}

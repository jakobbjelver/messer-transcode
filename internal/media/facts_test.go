package media

import "testing"

func TestFactsProjectAudio(t *testing.T) {
	p := Probe{
		VideoCodec: "hevc", Height: 1080, BitrateKbps: 5000,
		Audio: []AudioTrack{{Codec: "DTS", Lang: "eng"}, {Codec: "aac", Lang: "swe"}},
	}
	f := p.Facts()
	if f.AudioTracks != 2 {
		t.Fatalf("AudioTracks = %d, want 2", f.AudioTracks)
	}
	if len(f.AudioCodecs) != 2 || f.AudioCodecs[0] != "dts" || f.AudioCodecs[1] != "aac" {
		t.Fatalf("AudioCodecs = %v, want [dts aac] lowercased", f.AudioCodecs)
	}
}

func TestFactsWithoutAudio(t *testing.T) {
	f := (Probe{VideoCodec: "h264", Height: 720}).Facts()
	if f.AudioTracks != 0 || len(f.AudioCodecs) != 0 {
		t.Fatalf("no audio streams must project to empty facts, got %d/%v", f.AudioTracks, f.AudioCodecs)
	}
}

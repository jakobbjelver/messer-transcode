package ffmpeg

import (
	"bytes"
	"testing"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// fixture is a real-shaped ffprobe dump: cover art as a video stream, a
// commentary track, and an SDH subtitle. Matroska omits format.bit_rate, so the
// bitrate must be derived from size and duration.
const fixture = `{
  "streams": [
    {"index": 0, "codec_type": "video", "codec_name": "mpeg2video", "width": 600, "height": 600,
     "disposition": {"attached_pic": 1}},
    {"index": 1, "codec_type": "video", "codec_name": "h264", "width": 1920, "height": 1080,
     "pix_fmt": "yuv420p", "disposition": {"attached_pic": 0}},
    {"index": 2, "codec_type": "audio", "codec_name": "eac3", "channels": 6,
     "tags": {"language": "eng", "title": "Surround"}, "disposition": {"default": 1}},
    {"index": 3, "codec_type": "audio", "codec_name": "aac", "channels": 2,
     "tags": {"language": "eng", "title": "Director commentary"}, "disposition": {"comment": 1}},
    {"index": 4, "codec_type": "subtitle", "codec_name": "subrip",
     "tags": {"language": "eng", "title": "English SDH"}, "disposition": {}},
    {"index": 5, "codec_type": "subtitle", "codec_name": "subrip",
     "tags": {"language": "swe"}, "disposition": {"forced": 1}}
  ],
  "format": {
    "format_name": "matroska,webm",
    "duration": "6000.000000",
    "size": "6000000000"
  }
}`

func TestParse(t *testing.T) {
	p, err := Parse(bytes.NewReader([]byte(fixture)))
	if err != nil {
		t.Fatal(err)
	}

	if p.VideoCodec != "h264" {
		t.Errorf("video codec = %q, want h264 (cover art must be skipped)", p.VideoCodec)
	}
	if p.Width != 1920 || p.Height != 1080 {
		t.Errorf("dimensions = %dx%d", p.Width, p.Height)
	}
	if p.Container != "matroska" {
		t.Errorf("container = %q, want matroska", p.Container)
	}
	if p.DurationS != 6000 {
		t.Errorf("duration = %v", p.DurationS)
	}
	// 6 GB over 6000 s = 8 Mbps.
	if p.BitrateKbps != 8000 {
		t.Errorf("bitrate = %d kbps, want 8000 derived from size/duration", p.BitrateKbps)
	}

	if len(p.Audio) != 2 {
		t.Fatalf("audio tracks = %d, want 2", len(p.Audio))
	}
	if !p.Audio[1].Commentary {
		t.Error("the commentary track should be marked")
	}
	if p.Audio[1].Lang != "eng" || p.Audio[1].Codec != "aac" {
		t.Errorf("track 1 = %+v", p.Audio[1])
	}

	if len(p.Subs) != 2 {
		t.Fatalf("subtitle tracks = %d, want 2", len(p.Subs))
	}
	if !p.Subs[0].HearingImpaired {
		t.Error("SDH should be marked hearing-impaired")
	}
	if !p.Subs[1].Forced {
		t.Error("the forced flag should survive")
	}
}

func TestParseRejectsNonVideo(t *testing.T) {
	const audioOnly = `{"streams":[{"index":0,"codec_type":"audio","codec_name":"flac"}],
	                    "format":{"format_name":"flac","duration":"10","size":"1000000"}}`
	if _, err := Parse(bytes.NewReader([]byte(audioOnly))); err == nil {
		t.Fatal("a file with no video stream must not produce a probe")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse(bytes.NewReader([]byte("not json"))); err == nil {
		t.Fatal("garbage must not parse")
	}
}

func TestNormaliseLang(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"eng", "eng"}, {"en", "eng"}, {"", "und"}, {"und", "und"},
		{"swe", "swe"}, {"sv", "swe"}, {"dan", "dan"}, {"nb", "nor"},
		{"EN", "eng"}, {"deu", "deu"},
	} {
		if got := media.NormaliseLang(tc.in); got != tc.want {
			t.Errorf("NormaliseLang(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsCommentaryAndSDH(t *testing.T) {
	if !media.IsCommentary("Director Commentary") {
		t.Error("commentary not detected")
	}
	if media.IsCommentary("Surround 5.1") {
		t.Error("false positive on a normal title")
	}
	if !media.IsHearingImpaired("English SDH") {
		t.Error("SDH not detected")
	}
	if !media.IsHearingImpaired("Closed Captions") {
		t.Error("closed captions not detected")
	}
	if media.IsHearingImpaired("Picard") {
		t.Error("\"Picard\" must not read as closed captions")
	}
}

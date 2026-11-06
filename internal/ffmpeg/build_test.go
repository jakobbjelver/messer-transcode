package ffmpeg

import (
	"strings"
	"testing"
)

func TestBuildEncode(t *testing.T) {
	args := Build(EncodeSpec{
		Src:        "/scratch/1.src.mkv",
		Dst:        "/scratch/1.out.mkv",
		VideoIndex: 1,
		Audio: []AudioSpec{
			{Index: 2},
			{Index: 3, TranscodeTo: "eac3", Kbps: 448},
		},
		Subs:       []int{5, 6},
		TargetKbps: 5500,
	})

	want := []string{
		"-hide_banner", "-nostdin", "-loglevel", "error", "-nostats", "-progress", "pipe:1",
		"-i", "/scratch/1.src.mkv",
		"-map", "0:1", "-map", "0:2", "-map", "0:3", "-map", "0:5", "-map", "0:6",
		"-c:v", "hevc_videotoolbox",
		"-b:v", "5500k", "-maxrate", "8250k", "-bufsize", "16500k", "-tag:v", "hvc1",
		"-c:a:0", "copy",
		"-c:a:1", "eac3", "-b:a:1", "448k",
		"-c:s", "copy",
		"-map_metadata", "0", "-map_chapters", "0",
		"-f", "matroska", "/scratch/1.out.mkv",
	}
	assertArgs(t, args, want)
}

func TestBuildRemuxCopiesVideo(t *testing.T) {
	args := Build(EncodeSpec{
		Src: "/a.mkv", Dst: "/b.mkv", VideoIndex: 0,
		Audio: []AudioSpec{{Index: 1}}, VideoCopy: true,
	})
	if !strings.Contains(strings.Join(args, " "), "-c:v copy") {
		t.Fatalf("remux should copy video: %v", args)
	}
	if strings.Contains(strings.Join(args, " "), "videotoolbox") {
		t.Fatalf("remux must not invoke the encoder: %v", args)
	}
	for _, a := range args {
		if a == "-b:v" {
			t.Fatalf("remux must not set a video bitrate: %v", args)
		}
	}
}

func TestBuildAudioIndexesFollowOutputOrder(t *testing.T) {
	// Input streams 7 and 2 are mapped; their output positions are 0 and 1.
	args := Build(EncodeSpec{
		Src: "s", Dst: "d", VideoIndex: 0,
		Audio: []AudioSpec{{Index: 7, TranscodeTo: "eac3", Kbps: 448}, {Index: 2}},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c:a:0 eac3") {
		t.Errorf("first mapped audio should be eac3: %v", args)
	}
	if !strings.Contains(joined, "-c:a:1 copy") {
		t.Errorf("second mapped audio should be copied: %v", args)
	}
}

func TestProbeArgs(t *testing.T) {
	args := ProbeArgs("/media/a b.mkv")
	if args[len(args)-1] != "/media/a b.mkv" {
		t.Errorf("path must be last and unquoted: %v", args)
	}
	if args[len(args)-2] != "--" {
		t.Errorf("path should be preceded by --: %v", args)
	}
}

func TestParseSpeed(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want float64
	}{{"12.3x", 12.3}, {"0.49x", 0.49}, {"", 0}, {"N/A", 0}} {
		if got := parseSpeed(tc.in); got != tc.want {
			t.Errorf("parseSpeed(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func assertArgs(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("arg count = %d, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q\n got: %v\nwant: %v", i, got[i], want[i], got, want)
		}
	}
}

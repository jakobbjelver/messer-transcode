package ffmpeg

import "testing"

const vmafFixture = `{
  "version": "3.0.0",
  "fps": 10.15,
  "frames": [
    {"frameNum": 0, "metrics": {"vmaf": 96.5}},
    {"frameNum": 1, "metrics": {"vmaf": 94.1}}
  ],
  "pooled_metrics": {
    "vmaf": {
      "min": 92.3,
      "max": 98.1,
      "mean": 95.42,
      "harmonic_mean": 95.31
    }
  }
}`

func TestParseVMAF(t *testing.T) {
	got, err := parseVMAF([]byte(vmafFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got.Mean != 95.42 {
		t.Errorf("mean = %v, want 95.42", got.Mean)
	}
	if got.Min != 92.3 || got.Max != 98.1 {
		t.Errorf("min/max = %v/%v", got.Min, got.Max)
	}
	if got.Frames != 2 {
		t.Errorf("frames = %d, want 2", got.Frames)
	}
}

func TestParseVMAFRejectsEmptyLog(t *testing.T) {
	if _, err := parseVMAF([]byte(`{"pooled_metrics":{"vmaf":{"mean":0}}}`)); err == nil {
		t.Fatal("a log with no scores must be an error, not a zero")
	}
}

func TestBuildSampleEncodeUsesProductionSettings(t *testing.T) {
	args := BuildSampleEncode("/media/film.mkv", "/tmp/out.mkv", 1200, 60, 6000)
	joined := ""
	for _, a := range args {
		joined += a + " "
	}
	for _, want := range []string{
		"-ss 1200.000", "-t 60.000",
		"-c:v hevc_videotoolbox", "-b:v 6000k", "-maxrate 9000k", "-bufsize 18000k",
		"-an", "-sn",
	} {
		if !contains(joined, want) {
			t.Errorf("sample encode is missing %q:\n%s", want, joined)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

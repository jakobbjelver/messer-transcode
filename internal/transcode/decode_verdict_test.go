package transcode

import (
	"errors"
	"testing"
)

// realMuxerNoise is copied verbatim from a real run: ffmpeg writes it to
// stderr while decoding a healthy stream whose PTS equals its DTS, and exits 0.
const realMuxerNoise = `[null @ 0xc7d018280] Application provided invalid, non monotonically increasing dts to muxer in stream 0: 73 >= 73`

func TestDecodeVerdictAcceptsMuxerDTSNoise(t *testing.T) {
	// The exit code is the verdict. Ten titles were parked as corrupt on
	// 2026-11-08..10 because this line, on a clean exit, was treated as a
	// decode failure.
	if err := decodeVerdict(nil, realMuxerNoise+"\n"+realMuxerNoise); err != nil {
		t.Fatalf("exit 0 with only muxer DTS noise = %v, want nil", err)
	}
}

func TestDecodeVerdictAcceptsEmptyStderr(t *testing.T) {
	if err := decodeVerdict(nil, ""); err != nil {
		t.Fatalf("exit 0 and silent = %v, want nil", err)
	}
}

func TestDecodeVerdictRejectsNonZeroExit(t *testing.T) {
	// A non-zero exit is a real failure even when the only line is the noise:
	// the noise explains it, it does not excuse it.
	if err := decodeVerdict(errors.New("exit status 1"), realMuxerNoise); err == nil {
		t.Fatal("a non-zero exit must fail the window")
	}
}

func TestDecodeVerdictRejectsUnknownStderrAtZeroExit(t *testing.T) {
	// Anything that is not the one known noise line stays a failure: the check
	// is conservative on purpose, just not paranoid about this line.
	stderr := `[h264 @ 0x13d80d600] Error while decoding stream #0:0: Invalid data found when processing input`
	if err := decodeVerdict(nil, stderr); err == nil {
		t.Fatal("an unexpected error line at exit 0 must still fail the window")
	}
}

func TestDecodeVerdictReportsNoisePlusRealError(t *testing.T) {
	stderr := realMuxerNoise + "\n[h264 @ 0x1] Error while decoding stream #0:0: Invalid data"
	if err := decodeVerdict(nil, stderr); err == nil {
		t.Fatal("a real error line must fail even when the known noise precedes it")
	}
}

package ffmpeg

import (
	"strings"
	"testing"
)

// parsePacketEnds reads the CSV ffprobe emits for packet pts/duration and
// returns the last frame end. Packets arrive in decode order, not presentation
// order, so the maximum — not the last line — is the end.
func TestParsePacketEndsTakesTheMaximum(t *testing.T) {
	csv := strings.Join([]string{
		"7693.352000,0.033000",
		"7693.319000,0.033000",
		"7693.385000,0.033000",
		"",
	}, "\n")
	if got := parsePacketEnds(strings.NewReader(csv)); got < 7693.385 || got > 7693.419 {
		t.Fatalf("end = %f, want 7693.418 (last pts + duration)", got)
	}
}

// A line without a duration is still a usable end: ffprobe leaves
// duration_time off for some streams.
func TestParsePacketEndsWithoutDuration(t *testing.T) {
	if got := parsePacketEnds(strings.NewReader("100.500000\n")); got != 100.5 {
		t.Fatalf("end = %f, want 100.5", got)
	}
}

func TestParsePacketEndsOfNothingIsZero(t *testing.T) {
	if got := parsePacketEnds(strings.NewReader("\n\n")); got != 0 {
		t.Fatalf("end = %f, want 0 (not measured)", got)
	}
}

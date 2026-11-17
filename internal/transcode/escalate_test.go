package transcode

import "testing"

// A quality miss is worth a second, bigger encode only when it is close. The
// measured curve on grain is flat: 89.9 at 6000 kbps was still 89.9 at 8400, so
// the escalation spent a full encode to prove a number it already knew.
func TestShouldEscalate(t *testing.T) {
	cases := []struct {
		name string
		vmaf float64
		// floor is what the tier expects minus the configured tolerance.
		floor float64
		want  bool
	}{
		{"a hair under the floor", 93.8, 94.0, true},
		{"exactly at the escalation limit", 92.5, 94.0, true},
		{"a point under", 93.0, 94.0, true},
		{"past the limit", 92.4, 94.0, false},
		{"four points under (grain)", 89.9, 94.0, false},
		{"no miss at all", 95.5, 94.0, false},
		{"no expectation to measure against", 95.5, 0, false},
	}
	for _, c := range cases {
		if got := shouldEscalate(c.vmaf, c.floor); got != c.want {
			t.Errorf("%s: shouldEscalate(%.1f, %.1f) = %v, want %v", c.name, c.vmaf, c.floor, got, c.want)
		}
	}
}

// An escalation is only worth an encode if it can still pass the size check.
// 1.4x of a 6,000 rung is 8,400 kbps; against an 8,623 kbps source that is a
// ratio near 1.0 once audio is added, so the ceiling rejects it however good
// the quality is. Measured 2026-11-17: Fargo (8,623) and Four Weddings (8,705)
// each spent two full encodes to prove exactly that.
func TestFitsSizeCeiling(t *testing.T) {
	const max = 0.98
	cases := []struct {
		name         string
		kbps, source int
		want         bool
	}{
		{"the Fargo escalation", 8400, 8623, false},
		{"the Four Weddings escalation", 8400, 8705, false},
		{"a source with room above the rung", 8400, 12654, true},
		{"the City Lights escalation", 3780, 10141, true},
		{"no measured source bitrate", 8400, 0, true},
		{"no target", 0, 8623, true},
	}
	for _, c := range cases {
		if got := fitsSizeCeiling(c.kbps, c.source, max); got != c.want {
			t.Errorf("%s: fitsSizeCeiling(%d, %d, %.2f) = %v, want %v",
				c.name, c.kbps, c.source, max, got, c.want)
		}
	}
}

// The guard follows the configured ceiling rather than hardcoding 0.98: an
// operator who allows a 20 % growth is not running the same risk.
func TestFitsSizeCeilingFollowsTheConfiguredMax(t *testing.T) {
	if !fitsSizeCeiling(8400, 8623, 1.20) {
		t.Error("with size_ratio_max 1.20 an 8,400 kbps rung on an 8,623 kbps source should fit")
	}
}

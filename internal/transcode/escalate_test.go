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

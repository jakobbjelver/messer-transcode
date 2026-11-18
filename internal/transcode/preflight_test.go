package transcode

import "testing"

// A pre-flight sample is one sixty-second window where the verifier averages
// two, so it is noisier — it may only park a file when it misses by more than
// the noise. A near miss still gets the full encode and the real verdict.
func TestPreflightMiss(t *testing.T) {
	const floor = 94.0
	const margin = 2.0
	cases := []struct {
		name   string
		sample float64
		floor  float64
		want   bool
	}{
		// The measured case: If I Had Legs sampled 63.5, the full encode 63.3.
		{"a grainy film far under the floor", 63.5, floor, true},
		{"two points under", 91.9, floor, true},
		{"exactly at the margin", 92.0, floor, false},
		{"a hair under", 93.5, floor, false},
		{"at the floor", floor, floor, false},
		{"above the floor", 96.0, floor, false},
		// A failed measurement is not a verdict.
		{"no score", 0, floor, false},
		// A tier with no expectation cannot be held to one.
		{"no floor", 63.5, 0, false},
	}
	for _, c := range cases {
		if got := preflightMiss(c.sample, c.floor, margin); got != c.want {
			t.Errorf("%s: preflightMiss(%.1f, %.1f) = %v, want %v", c.name, c.sample, c.floor, got, c.want)
		}
	}
}

package transcode

import (
	"math"
	"strings"
	"testing"
)

// The duration check compares the picture, not the container.
//
// Measured 2026-11-24: Vertigo's encode was rejected as "Δ -30.0s, tolerance
// 23s" after a full 10.6 GB encode. The source's last video packet ends at
// 7693.35s and its audio at 7693.38s; only a subtitle packet reaches 7723.4s,
// so the container is thirty seconds longer than the film and the encode was
// right. On a two-hour feature the 0.3 % tolerance caps at 30 s, which is
// exactly where a trailing-subtitle WEB-DL lands.
func TestDurationIsJudgedOnTheVideoEndNotTheContainer(t *testing.T) {
	ok, detail := durationVerdict(7723.416, 7693.385, 7693.400, 7693.367, 2)
	if !ok {
		t.Fatalf("verdict = %s, want it accepted: the picture is the same length", detail)
	}
	if !strings.Contains(detail, "video") {
		t.Errorf("detail = %q, want it to say the comparison used the video end", detail)
	}
}

// The check still fails a real truncation: the video end is the basis, so a
// missing reel cannot hide behind a subtitle tail.
func TestDurationStillFailsATruncatedVideo(t *testing.T) {
	ok, detail := durationVerdict(7723.416, 7693.385, 7600.0, 7599.9, 2)
	if ok {
		t.Fatalf("verdict = %s, want a failure: 93s of picture is missing", detail)
	}
	if !strings.Contains(detail, "E_VERIFY_DURATION") && !strings.Contains(detail, "-93.5s") {
		t.Errorf("detail = %q, want the delta reported", detail)
	}
}

// An output whose own container runs past its last frame is not a failure
// either: the same tail can be carried by the remux.
func TestDurationIgnoresATailOnTheOutput(t *testing.T) {
	ok, detail := durationVerdict(7723.416, 7693.385, 7800.0, 7693.400, 2)
	if !ok {
		t.Fatalf("verdict = %s, want it accepted: the output's picture matches the source's", detail)
	}
}

// When either end cannot be measured the container is the only basis there is,
// and the check behaves exactly as it did before.
func TestDurationFallsBackToTheContainerWhenAnEndIsUnknown(t *testing.T) {
	ok, detail := durationVerdict(7723.416, 0, 7693.400, 7693.367, 2)
	if ok {
		t.Fatalf("verdict = %s, want the container comparison to stand", detail)
	}
	if strings.Contains(detail, "video") {
		t.Errorf("detail = %q, want no claim of a video basis", detail)
	}
}

func TestDurationRejectsAnOutputThatRunsLong(t *testing.T) {
	ok, _ := durationVerdict(1345.184, 1345.100, 1500.0, 1499.900, 2)
	if ok {
		t.Fatal("want a failure: the output is 155s longer than the source")
	}
}

// The scaled tolerance is unchanged: max(configured, 0.3 % of the length),
// capped at 30 s, never below what the operator asked for.
func TestDurationToleranceScaling(t *testing.T) {
	for _, tc := range []struct {
		name       string
		length     float64
		configured float64
		want       float64
	}{
		{"short file keeps the floor", 60, 2, 2},
		{"feature widens to a share", 7723.416, 2, 23.17},
		{"cap on a very long film", 14400, 2, 30},
		{"operator asking for more keeps it", 60, 45, 45},
	} {
		if got := durationTolerance(tc.length, tc.configured); math.Abs(got-tc.want) > 0.01 {
			t.Errorf("%s: tolerance = %.2f, want %.2f", tc.name, got, tc.want)
		}
	}
}

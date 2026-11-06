package calibrate

import (
	"testing"
)

func TestWindowStartSpreadsThroughTheFilm(t *testing.T) {
	// Two windows in a 6000 s film: both strictly inside, and in order.
	a := windowStart(6000, 60, 0, 2)
	b := windowStart(6000, 60, 1, 2)

	if a <= 0 || b <= a {
		t.Fatalf("windows not spread: a=%v b=%v", a, b)
	}
	if a+60 > 6000 || b+60 > 6000 {
		t.Fatalf("a window runs past the end: a=%v b=%v", a, b)
	}
	// The first window must not sit at the very start, where titles live.
	if a < 500 {
		t.Errorf("first window at %.0fs is too close to the head", a)
	}
}

func TestWindowStartOnAShortFile(t *testing.T) {
	if got := windowStart(90, 60, 0, 2); got != 0 {
		t.Errorf("a file shorter than two windows should start at 0, got %v", got)
	}
}

func TestBuildCurvePicksLowestRungMeetingTarget(t *testing.T) {
	points := []Point{
		{Kbps: 4000, VMAF: 91.0},
		{Kbps: 5000, VMAF: 93.2},
		{Kbps: 6000, VMAF: 94.4},
		{Kbps: 7000, VMAF: 95.6},
		{Kbps: 8500, VMAF: 96.9},
	}
	c := buildCurve("h264", 1080, points, 95.0)

	if !c.Met {
		t.Fatal("expected the target to be met")
	}
	if c.Chosen != 7000 {
		t.Errorf("chosen = %d, want 7000 (6000 is below target)", c.Chosen)
	}
	if len(c.Rungs) != 5 {
		t.Fatalf("rungs = %d, want 5", len(c.Rungs))
	}
	if c.Rungs[0].Kbps != 4000 {
		t.Errorf("rungs should be ascending, got %+v", c.Rungs)
	}
}

func TestBuildCurveReportsFailureRatherThanGuessing(t *testing.T) {
	points := []Point{
		{Kbps: 4000, VMAF: 80},
		{Kbps: 5000, VMAF: 85},
	}
	c := buildCurve("hevc", 1080, points, 95.0)
	if c.Met {
		t.Fatal("no rung reached the target; Met must be false")
	}
	if c.Chosen != 0 {
		t.Errorf("chosen = %d, want 0 when nothing met the target", c.Chosen)
	}
}

func TestBuildCurveAveragesAndTracksMinimum(t *testing.T) {
	points := []Point{
		{Kbps: 6000, VMAF: 96},
		{Kbps: 6000, VMAF: 92},
	}
	c := buildCurve("h264", 1080, points, 95)
	if c.Rungs[0].VMAF != 94 {
		t.Errorf("mean = %v, want 94", c.Rungs[0].VMAF)
	}
	if c.Rungs[0].MinVMAF != 92 {
		t.Errorf("min = %v, want 92", c.Rungs[0].MinVMAF)
	}
}

func TestSortRowsIsSpecificFirst(t *testing.T) {
	rows := []SuggestRow{
		{Codec: "hevc", HeightGTE: 1000, Kbps: 7000},
		{Codec: "h264", HeightGTE: 0, Kbps: 1200},
		{Codec: "h264", HeightGTE: 2000, Kbps: 18000},
		{Codec: "h264", HeightGTE: 1000, Kbps: 7000},
	}
	sortRows(rows)

	want := []struct {
		codec  string
		height int
	}{
		{"h264", 2000}, {"h264", 1000}, {"h264", 0}, {"hevc", 1000},
	}
	for i, w := range want {
		if rows[i].Codec != w.codec || rows[i].HeightGTE != w.height {
			t.Fatalf("row %d = %+v, want %s/%d", i, rows[i], w.codec, w.height)
		}
	}
}

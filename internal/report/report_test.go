package report

import (
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

func cp() *time.Location {
	l, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		panic(err)
	}
	return l
}

func sched() config.Schedule {
	return config.Schedule{
		Weekday:  "00:30-05:30",
		Weekend:  "02:00-07:00",
		Timezone: "Europe/Copenhagen",
	}
}

// A weekday window opens and closes on the same day it is named for.
func TestWindowsWeekday(t *testing.T) {
	loc := cp()
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, loc) // Wednesday noon
	got := Windows(sched(), now, 1)
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	w := got[0]
	if w.Kind != "weekday" {
		t.Errorf("kind = %q, want weekday", w.Kind)
	}
	if w.Start.Hour() != 0 || w.Start.Minute() != 30 {
		t.Errorf("start = %s, want 00:30", w.Start)
	}
	if w.End.Hour() != 5 || w.End.Minute() != 30 {
		t.Errorf("end = %s, want 05:30", w.End)
	}
	if w.Start.Day() != 4 {
		t.Errorf("start day = %d, want 4 (the same Wednesday)", w.Start.Day())
	}
}

// Saturday and Sunday use the weekend spec, and the window belongs to the day
// it opened on.
func TestWindowsWeekend(t *testing.T) {
	loc := cp()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, loc) // Saturday noon
	w := Windows(sched(), now, 1)[0]
	if w.Kind != "weekend" {
		t.Fatalf("kind = %q, want weekend", w.Kind)
	}
	if w.Start.Hour() != 2 || w.End.Hour() != 7 {
		t.Errorf("window = %s-%s, want 02:00-07:00", w.Start, w.End)
	}
}

// A window that wraps midnight ends on the following day, and the spec still
// comes from the day it opened.
func TestWindowWrapsMidnight(t *testing.T) {
	loc := cp()
	s := config.Schedule{Weekday: "23:00-03:00", Weekend: "23:00-03:00", Timezone: "Europe/Copenhagen"}
	now := time.Date(2026, 11, 4, 1, 0, 0, 0, loc) // Wednesday 01:00, inside the window opened Tuesday
	w := Windows(s, now, 1)[0]
	if w.Start.Day() != 3 {
		t.Errorf("start = %s, want Tuesday the 3rd", w.Start)
	}
	if w.End.Day() != 4 || w.End.Hour() != 3 {
		t.Errorf("end = %s, want Wednesday 03:00", w.End)
	}
}

// A window that has not opened yet is not reported as an empty run: asking at
// 00:10 for "the last run" should describe yesterday, not tonight.
func TestFutureWindowIsNotReported(t *testing.T) {
	loc := cp()
	now := time.Date(2026, 11, 4, 0, 10, 0, 0, loc) // Wednesday, 00:10 — before 00:30
	got := Windows(sched(), now, 1)
	if len(got) != 1 {
		t.Fatalf("got %d windows, want 1", len(got))
	}
	if got[0].Start.Day() != 3 {
		t.Errorf("start = %s, want the previous day's window", got[0].Start)
	}
}

// The window counts back across a weekend boundary correctly: asking on Monday
// morning must find Friday's window before Sunday's.
func TestWindowsOrderNewestFirst(t *testing.T) {
	loc := cp()
	now := time.Date(2026, 11, 9, 6, 0, 0, 0, loc) // Monday 06:00
	got := Windows(sched(), now, 3)
	if len(got) != 3 {
		t.Fatalf("got %d windows, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Start.Before(got[i-1].Start) {
			t.Fatalf("window %d (%s) is not before window %d (%s)",
				i, got[i].Start, i-1, got[i-1].Start)
		}
	}
	// Monday 00:30-05:30 is the newest, then Sunday's, then Saturday's.
	if got[0].Kind != "weekday" {
		t.Errorf("newest kind = %q, want weekday", got[0].Kind)
	}
	if got[1].Kind != "weekend" || got[2].Kind != "weekend" {
		t.Errorf("older kinds = %q/%q, want weekend/weekend", got[1].Kind, got[2].Kind)
	}
}

// A daytime window is the point of the whole exercise: nothing may assume the
// work happens at night.
func TestDaytimeWindow(t *testing.T) {
	loc := cp()
	s := config.Schedule{Weekday: "09:00-17:00", Weekend: "10:00-14:00", Timezone: "Europe/Copenhagen"}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, loc)
	w := Windows(s, now, 1)[0]
	if w.Start.Hour() != 9 || w.End.Hour() != 17 {
		t.Errorf("window = %s-%s, want 09:00-17:00", w.Start, w.End)
	}
	r := Run{Window: w, Open: true}
	if got := r.WindowSeconds(now); got != 3*3600 {
		t.Errorf("elapsed for an open window = %ds, want 10800", got)
	}
}

func TestParseWindowRejectsJunk(t *testing.T) {
	for _, spec := range []string{"", "00:30", "aa:bb-cc:dd", "25:00-26:00", "00:70-05:00"} {
		if _, _, err := parseWindow(spec); err == nil {
			t.Errorf("parseWindow(%q) accepted junk", spec)
		}
	}
}

func TestUtilisation(t *testing.T) {
	loc := cp()
	start := time.Date(2026, 11, 4, 0, 30, 0, 0, loc)
	r := Run{Window: Window{Start: start, End: start.Add(5 * time.Hour)}}
	r.BusySeconds = 3 * 3600
	if got := r.Utilisation(start.Add(5 * time.Hour)); got != 60 {
		t.Errorf("utilisation = %.1f, want 60", got)
	}
}

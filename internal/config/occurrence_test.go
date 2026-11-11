package config

import (
	"testing"
	"time"
)

// testSchedule builds the pre-windows form and migrates it, which is exactly
// what a load does — so these tests exercise the compatibility path rather than
// a hand-made windows literal.
func testSchedule(wd, we string) Schedule {
	s := Schedule{Weekday: wd, Weekend: we, DrainBeforeMinutes: 0, Timezone: "UTC"}
	s.migrate()
	return s
}

func at(y int, mo time.Month, d, h, m int) time.Time {
	return time.Date(y, mo, d, h, m, 0, 0, time.UTC)
}

func TestOccurrenceAt(t *testing.T) {
	// Fri 2026-11-13 22:00 → Sat 06:00.
	s := testSchedule("22:00-06:00", "22:00-06:00")

	occ, ok := s.OccurrenceAt(at(2026, 11, 14, 2, 0))
	if !ok {
		t.Fatal("02:00 on Saturday should be inside Friday's window")
	}
	if !occ.Start.Equal(at(2026, 11, 13, 22, 0)) || !occ.End.Equal(at(2026, 11, 14, 6, 0)) {
		t.Fatalf("occurrence %s–%s, want Fri 22:00–Sat 06:00", occ.Start, occ.End)
	}

	if _, ok := s.OccurrenceAt(at(2026, 11, 14, 12, 0)); ok {
		t.Fatal("noon is outside a 22:00-06:00 window")
	}
}

func TestOccurrenceAtUsesWeekdayAndWeekendSpecs(t *testing.T) {
	s := testSchedule("00:30-05:30", "02:00-07:00")

	// Mon 2026-11-09 is a weekday.
	if occ, ok := s.OccurrenceAt(at(2026, 11, 9, 3, 0)); !ok || occ.Kind != "weekday" {
		t.Fatalf("monday: %v %q", ok, occ.Kind)
	}
	// Sat 2026-11-14 is the weekend.
	if occ, ok := s.OccurrenceAt(at(2026, 11, 14, 3, 0)); !ok || occ.Kind != "weekend" {
		t.Fatalf("saturday: %v %q", ok, occ.Kind)
	}
}

func TestPreviousOccurrencesNewestFirst(t *testing.T) {
	s := testSchedule("00:30-05:30", "02:00-07:00")
	now := at(2026, 11, 11, 12, 0)

	got := s.PreviousOccurrences(now, 3)
	if len(got) != 3 {
		t.Fatalf("got %d occurrences, want 3", len(got))
	}
	if !got[0].Start.Equal(at(2026, 11, 11, 0, 30)) {
		t.Fatalf("newest occurrence starts %s, want today 00:30", got[0].Start)
	}
	if !got[1].Start.Before(got[0].Start) {
		t.Fatal("occurrences are not newest first")
	}
}

func TestOccurrencesRespectTheRange(t *testing.T) {
	s := testSchedule("00:30-05:30", "02:00-07:00")
	from, to := at(2026, 11, 9, 0, 0), at(2026, 11, 11, 12, 0)

	got := s.Occurrences(from, to)
	if len(got) != 3 {
		t.Fatalf("got %d occurrences, want 3 (9, 10 and 11 Nov)", len(got))
	}
	if !got[0].Start.Equal(at(2026, 11, 9, 0, 30)) {
		t.Fatalf("first occurrence %s", got[0].Start)
	}
}

func TestOccurrenceRespectsTheZone(t *testing.T) {
	// A window in Stockholm is not a window in UTC.
	s := testSchedule("00:30-05:30", "00:30-05:30")
	s.Timezone = "Europe/Stockholm"
	occ, ok := s.OccurrenceAt(time.Date(2026, 11, 11, 23, 30, 0, 0, time.UTC)) // 00:30 local, winter
	if !ok {
		t.Fatal("23:30Z is 00:30 in Stockholm and should be inside the window")
	}
	if got := occ.Start.UTC(); !got.Equal(time.Date(2026, 11, 11, 23, 30, 0, 0, time.UTC)) {
		t.Fatalf("start in UTC %s, want 23:30Z", got)
	}
}

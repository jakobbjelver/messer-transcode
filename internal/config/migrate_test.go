package config

import (
	"testing"
	"time"
)

// legacyOccurrences is the pre-windows arithmetic, kept here as the reference
// the migration is checked against.
//
// The schedule release changes how a schedule is written down, not what it
// means: a config that says weekday "00:30-05:30" and weekend "02:00-07:00"
// must open and close at exactly the same instants before and after. This test
// is that promise, over a year of quarter-hours rather than one night — a
// boundary bug would hide in a single sample.
func legacyOccurrences(weekday, weekend, zone string, from, to time.Time) []Occurrence {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	specFor := func(d time.Time) (string, string) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			return weekend, "weekend"
		}
		return weekday, "weekday"
	}
	on := func(d time.Time) (Occurrence, bool) {
		spec, kind := specFor(d)
		w, err := ParseWindow(spec)
		if err != nil {
			return Occurrence{}, false
		}
		start := time.Date(d.Year(), d.Month(), d.Day(), w.StartMin/60, w.StartMin%60, 0, 0, loc)
		endDay := d
		if w.EndMin <= w.StartMin {
			endDay = d.AddDate(0, 0, 1)
		}
		end := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), w.EndMin/60, w.EndMin%60, 0, 0, loc)
		return Occurrence{Start: start, End: end, Spec: spec, Kind: kind}, true
	}
	var out []Occurrence
	lf, lt := from.In(loc), to.In(loc)
	for d := lf.AddDate(0, 0, -1); !d.After(lt.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		if occ, ok := on(d); ok && occ.End.After(from) && occ.Start.Before(to) {
			out = append(out, occ)
		}
	}
	return out
}

func TestMigrationPreservesTheSchedule(t *testing.T) {
	cases := []struct{ weekday, weekend, zone string }{
		{"00:30-05:30", "02:00-07:00", "Europe/Stockholm"},
		{"23:00-03:00", "22:00-06:00", "Europe/Stockholm"},
		{"00:00-23:59", "00:00-23:59", "UTC"},
		{"09:00-17:00", "10:00-14:00", "Europe/Copenhagen"},
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, c := range cases {
		t.Run(c.weekday+" "+c.zone, func(t *testing.T) {
			want := legacyOccurrences(c.weekday, c.weekend, c.zone, from, to)
			if len(want) < 100 {
				t.Fatalf("reference produced %d occurrences, the test is not exercising anything", len(want))
			}

			s := Schedule{Weekday: c.weekday, Weekend: c.weekend, Timezone: c.zone}
			s.migrate()
			got := s.Occurrences(from, to)

			if len(got) != len(want) {
				t.Fatalf("got %d occurrences, want %d", len(got), len(want))
			}
			for i := range want {
				if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) {
					t.Fatalf("occurrence %d = %s–%s, want %s–%s",
						i, got[i].Start, got[i].End, want[i].Start, want[i].End)
				}
				if got[i].Kind != want[i].Kind {
					t.Fatalf("occurrence %d kind = %q, want %q", i, got[i].Kind, want[i].Kind)
				}
			}

			// OccurrenceAt must agree at every quarter hour, including the
			// instants just either side of a boundary.
			for at := from; at.Before(to); at = at.Add(15 * time.Minute) {
				legacyInside := false
				for _, occ := range want {
					if !at.Before(occ.Start) && at.Before(occ.End) {
						legacyInside = true
						break
					}
				}
				_, ok := s.OccurrenceAt(at)
				if ok != legacyInside {
					t.Fatalf("at %s: OccurrenceAt=%v, reference=%v", at, ok, legacyInside)
				}
			}
		})
	}
}

// A window written as days + a clock range says what the legacy pair could not:
// one night, on one day, crossing midnight.
func TestWindowCrossingMidnightBelongsToItsStartDay(t *testing.T) {
	s := Schedule{
		Windows:  []ScheduleWindow{{Days: []string{"fri"}, Start: "22:00", End: "06:00"}},
		Timezone: "UTC",
	}
	// Saturday 02:00 is inside Friday night.
	occ, ok := s.OccurrenceAt(time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC))
	if !ok || !occ.Start.Equal(time.Date(2026, 11, 13, 22, 0, 0, 0, time.UTC)) {
		t.Fatalf("friday night: %v %s", ok, occ.Start)
	}
	// Saturday 22:00 is not: the block is Friday only.
	if _, ok := s.OccurrenceAt(time.Date(2026, 11, 14, 23, 0, 0, 0, time.UTC)); ok {
		t.Fatal("saturday night should not be inside a friday window")
	}
}

// Two windows on one day are two occurrences, ordered.
func TestTwoWindowsInADay(t *testing.T) {
	s := Schedule{
		Windows: []ScheduleWindow{
			{Days: []string{"mon", "tue"}, Start: "09:00", End: "11:00"},
			{Days: []string{"mon"}, Start: "13:00", End: "15:00"},
		},
		Timezone: "UTC",
	}
	// Mon 2026-11-09.
	got := s.Occurrences(at(2026, 11, 9, 0, 0), at(2026, 11, 10, 0, 0))
	if len(got) != 2 || got[0].Start.Hour() != 9 || got[1].Start.Hour() != 13 {
		t.Fatalf("occurrences = %+v", got)
	}
	if got[0].Spec == "" || got[1].Spec == "" {
		t.Fatal("an occurrence needs its wording: the ledger stores it")
	}
}

// The day list is indexed by time.Weekday, which starts at Sunday. An
// off-by-one here shifts every label by a day while the occurrence arithmetic
// stays correct — the quietest possible bug.
func TestDayNamesMatchTimeWeekday(t *testing.T) {
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		name := DayName(wd)
		if got := DayIndex(name); got != int(wd) {
			t.Fatalf("%v is %q, which maps back to %d", wd, name, got)
		}
		w := ScheduleWindow{Days: []string{name}, Start: "09:00", End: "10:00"}
		if got := w.describe(); got != "on "+title(name)+", 09:00–10:00" {
			t.Fatalf("%v describes as %q", wd, got)
		}
	}
	if DayName(time.Wednesday) != "wed" {
		t.Fatalf("Wednesday is %q", DayName(time.Wednesday))
	}
}

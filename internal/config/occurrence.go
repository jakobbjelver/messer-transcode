package config

import "time"

// Occurrence is one run of the schedule, resolved to instants in its zone.
//
// A window is a wall-clock range; an occurrence is that range on a particular
// day. The distinction matters at midnight: 22:00–06:00 is one occurrence that
// starts on Friday and ends on Saturday, and every piece of arithmetic in this
// app (claims, runs, the ledger) needs the pair, not the pair of clock times.
type Occurrence struct {
	Start time.Time
	End   time.Time
	Spec  string
	Kind  string // weekday | weekend
}

// Location is the schedule's zone. An unset or unparseable zone is UTC, which
// is what every other reader in this app already assumes.
func (s Schedule) Location() *time.Location {
	if s.Timezone != "" {
		if l, err := time.LoadLocation(s.Timezone); err == nil {
			return l
		}
	}
	return time.UTC
}

// specFor picks the weekday or weekend window for a day.
func (s Schedule) specFor(d time.Time) (spec, kind string) {
	if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
		return s.Weekend, "weekend"
	}
	return s.Weekday, "weekday"
}

// occurrenceOn builds the window that starts on the calendar day of d.
func (s Schedule) occurrenceOn(d time.Time) (Occurrence, bool) {
	spec, kind := s.specFor(d)
	w, err := ParseWindow(spec)
	if err != nil {
		return Occurrence{}, false
	}
	loc := d.Location()
	start := time.Date(d.Year(), d.Month(), d.Day(), w.StartMin/60, w.StartMin%60, 0, 0, loc)
	endDay := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	if w.EndMin <= w.StartMin {
		endDay = endDay.AddDate(0, 0, 1) // the window wraps past midnight
	}
	end := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), w.EndMin/60, w.EndMin%60, 0, 0, loc)
	return Occurrence{Start: start, End: end, Spec: spec, Kind: kind}, true
}

// OccurrenceAt returns the window containing t, if any.
func (s Schedule) OccurrenceAt(t time.Time) (Occurrence, bool) {
	local := t.In(s.Location())
	// Today's window, or yesterday's when it wrapped past midnight.
	for _, d := range []time.Time{local, local.AddDate(0, 0, -1)} {
		occ, ok := s.occurrenceOn(d)
		if !ok {
			continue
		}
		if !t.Before(occ.Start) && t.Before(occ.End) {
			return occ, true
		}
	}
	return Occurrence{}, false
}

// Occurrences lists every window whose occurrence ends after `from` and starts
// before `to`, oldest first. A window that started before `from` but is still
// running is included: it is the one a report is usually about.
func (s Schedule) Occurrences(from, to time.Time) []Occurrence {
	loc := s.Location()
	localFrom := from.In(loc)
	localTo := to.In(loc)

	var out []Occurrence
	// One day of slack on each side covers wraps in both directions.
	for d := localFrom.AddDate(0, 0, -1); !d.After(localTo.AddDate(0, 0, 1)); d = d.AddDate(0, 0, 1) {
		occ, ok := s.occurrenceOn(d)
		if !ok {
			continue
		}
		if occ.End.After(from) && occ.Start.Before(to) {
			out = append(out, occ)
		}
	}
	return out
}

// PreviousOccurrences returns up to n occurrences that have already started,
// newest first, including the one in progress.
func (s Schedule) PreviousOccurrences(now time.Time, n int) []Occurrence {
	if n <= 0 {
		return nil
	}
	loc := s.Location()
	local := now.In(loc)
	out := make([]Occurrence, 0, n)
	for d := local.AddDate(0, 0, 1); len(out) < n && local.Sub(d) < 400*24*time.Hour; d = d.AddDate(0, 0, -1) {
		occ, ok := s.occurrenceOn(d)
		if !ok || occ.Start.After(local) {
			continue
		}
		out = append(out, occ)
	}
	return out
}

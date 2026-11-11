package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ScheduleWindow is one block of permitted work: a set of days and a
// wall-clock range on those days.
//
// Days are the *day the block starts on*, which is what makes a window that
// crosses midnight sayable: {days:[fri], start:"22:00", end:"06:00"} is Friday
// night, not "Friday and Saturday". The old weekday/weekend strings could not
// express that, or a Tuesday-only block, or an afternoon block.
type ScheduleWindow struct {
	Days  []string `json:"days"`
	Start string   `json:"start"`
	End   string   `json:"end"`
}

// dayNames is indexed by time.Weekday, which starts at Sunday. Getting this
// order wrong shifts every label by a day while the arithmetic stays correct,
// so it is asserted in a test rather than trusted.
var dayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// DayIndex maps a short day name to time.Weekday, or -1.
func DayIndex(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "mon", "monday":
		return int(time.Monday)
	case "tue", "tues", "tuesday":
		return int(time.Tuesday)
	case "wed", "wednesday":
		return int(time.Wednesday)
	case "thu", "thur", "thurs", "thursday":
		return int(time.Thursday)
	case "fri", "friday":
		return int(time.Friday)
	case "sat", "saturday":
		return int(time.Saturday)
	case "sun", "sunday":
		return int(time.Sunday)
	}
	return -1
}

// DayName is the short name for a weekday.
func DayName(d time.Weekday) string { return dayNames[int(d)] }

// describe is the window in words: "Mon–Fri, 00:30–05:30".
func (w ScheduleWindow) describe() string {
	return fmt.Sprintf("%s, %s–%s", describeDays(w.Days), w.Start, w.End)
}

// describeDays collapses a day list into the phrase a person would use.
func describeDays(days []string) string {
	set := map[int]bool{}
	for _, d := range days {
		if i := DayIndex(d); i >= 0 {
			set[i] = true
		}
	}
	switch len(set) {
	case 0:
		return "no days"
	case 7:
		return "every day"
	case 5:
		weekdays := true
		for i := 1; i <= 5; i++ {
			weekdays = weekdays && set[i]
		}
		if weekdays {
			return "weekdays"
		}
	case 2:
		if set[int(time.Saturday)] && set[int(time.Sunday)] {
			return "weekends"
		}
	}
	var names []string
	for i := 1; i <= 7; i++ {
		wd := i % 7
		if set[wd] {
			names = append(names, dayNames[wd])
		}
	}
	if len(names) == 1 {
		return "on " + title(names[0])
	}
	return strings.Join(names, ", ")
}

// kindOf names a window's shape, for the run ledger.
func (w ScheduleWindow) kind() string {
	switch describeDays(w.Days) {
	case "weekdays":
		return "weekday"
	case "weekends":
		return "weekend"
	case "every day":
		return "daily"
	}
	return "custom"
}

// Windows is the schedule as blocks, oldest-first within a day.
//
// Legacy configs still carry weekday/weekend strings; migrate() turns them into
// two windows on load, so exactly one representation ever reaches the rest of
// the program.
func (s *Schedule) migrate() {
	if len(s.Windows) > 0 {
		s.Weekday, s.Weekend = "", ""
		return
	}
	add := func(spec string, days []string) {
		if strings.TrimSpace(spec) == "" {
			return
		}
		w, err := ParseWindow(spec)
		if err != nil {
			return // validation reports it if nothing valid is left
		}
		s.Windows = append(s.Windows, ScheduleWindow{
			Days:  days,
			Start: fmt.Sprintf("%02d:%02d", w.StartMin/60, w.StartMin%60),
			End:   fmt.Sprintf("%02d:%02d", w.EndMin/60, w.EndMin%60),
		})
	}
	add(s.Weekday, []string{"mon", "tue", "wed", "thu", "fri"})
	add(s.Weekend, []string{"sat", "sun"})
	s.Weekday, s.Weekend = "", ""
}

// Occurrence is one run of the schedule, resolved to instants in its zone.
//
// A window is a wall-clock range; an occurrence is that range on a particular
// day. The distinction matters at midnight: 22:00–06:00 is one occurrence that
// starts on Friday and ends on Saturday, and every piece of arithmetic in this
// app (claims, runs, the ledger) needs the pair, not the pair of clock times.
type Occurrence struct {
	Start time.Time
	End   time.Time
	// Spec is the window in words — what the ledger stores, and what a person
	// reads in the run history a year later.
	Spec string
	// Kind is weekday | weekend | daily | custom.
	Kind string
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

// occurrencesOn lists every window that starts on the calendar day of d.
func (s Schedule) occurrencesOn(d time.Time) []Occurrence {
	loc := d.Location()
	wd := int(d.Weekday())
	var out []Occurrence
	for _, w := range s.Windows {
		if !hasDay(w.Days, wd) {
			continue
		}
		r, err := ParseWindow(w.Start + "-" + w.End)
		if err != nil {
			continue
		}
		start := time.Date(d.Year(), d.Month(), d.Day(), r.StartMin/60, r.StartMin%60, 0, 0, loc)
		endDay := d
		if r.EndMin <= r.StartMin {
			endDay = d.AddDate(0, 0, 1) // the window wraps past midnight
		}
		end := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), r.EndMin/60, r.EndMin%60, 0, 0, loc)
		out = append(out, Occurrence{Start: start, End: end, Spec: w.describe(), Kind: w.kind()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func hasDay(days []string, wd int) bool {
	for _, d := range days {
		if DayIndex(d) == wd {
			return true
		}
	}
	return false
}

// OccurrenceAt returns the window containing t, if any.
func (s Schedule) OccurrenceAt(t time.Time) (Occurrence, bool) {
	local := t.In(s.Location())
	// Today's windows, or yesterday's when one wrapped past midnight.
	var best Occurrence
	found := false
	for _, d := range []time.Time{local, local.AddDate(0, 0, -1)} {
		for _, occ := range s.occurrencesOn(d) {
			if t.Before(occ.Start) || !t.Before(occ.End) {
				continue
			}
			// Overlapping windows are a union: the earliest start is the run
			// the work belongs to, which keeps the ledger stable.
			if !found || occ.Start.Before(best.Start) {
				best, found = occ, true
			}
		}
	}
	return best, found
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
		for _, occ := range s.occurrencesOn(d) {
			if occ.End.After(from) && occ.Start.Before(to) {
				out = append(out, occ)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
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
	var out []Occurrence
	for d := local.AddDate(0, 0, 1); len(out) < n && local.Sub(d) < 400*24*time.Hour; d = d.AddDate(0, 0, -1) {
		for _, occ := range s.occurrencesOn(d) {
			if occ.Start.After(local) {
				continue
			}
			out = append(out, occ)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.After(out[j].Start) })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// CronProjects renders each window as the two cron lines a person would write
// for it. It is a projection, never a source: this system schedules from the
// windows, and the lines exist so the schedule can be read by the rest of the
// world (and pasted into something that speaks cron).
//
// A window that crosses midnight closes on the following day, so the close line
// carries the day list shifted by one — the mistake a hand-written cron for
// "Friday 22:00 to 06:00" always makes.
func (s Schedule) CronProjects() []CronLine {
	var out []CronLine
	for _, w := range s.Windows {
		r, err := ParseWindow(w.Start + "-" + w.End)
		if err != nil {
			continue
		}
		open := crontab(r.StartMin, w.Days)
		closeDays := w.Days
		if r.EndMin <= r.StartMin {
			closeDays = shiftDays(w.Days, 1)
		}
		close := crontab(r.EndMin, closeDays)
		out = append(out, CronLine{Open: open, Close: close, Window: w.describe()})
	}
	return out
}

// CronLine is one window's two-line projection.
type CronLine struct {
	Open   string `json:"open"`
	Close  string `json:"close"`
	Window string `json:"window"`
}

func crontab(minuteOfDay int, days []string) string {
	nums := map[int]bool{}
	for _, d := range days {
		if i := DayIndex(d); i >= 0 {
			nums[i] = true
		}
	}
	// cron: 0 and 7 are both Sunday; this app's day list starts at Monday, so
	// the numbers are printed in cron's own order.
	var list []string
	for i := 0; i <= 6; i++ {
		if nums[i] {
			list = append(list, itoa(i))
		}
	}
	if len(list) == 0 {
		list = []string{"*"}
	}
	return fmt.Sprintf("%d %d * * %s", minuteOfDay%60, minuteOfDay/60, strings.Join(list, ","))
}

func shiftDays(days []string, by int) []string {
	out := make([]string, 0, len(days))
	for _, d := range days {
		if i := DayIndex(d); i >= 0 {
			out = append(out, dayNames[(i+by)%7])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return DayIndex(out[i]) < DayIndex(out[j]) })
	return out
}

func title(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

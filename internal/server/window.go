package server

import (
	"time"
)

// windowState reports whether the node may work now, the window it applies,
// and — when shut — when it next opens.
//
// It reads the schedule through the same occurrence arithmetic the claims use,
// so the badge and the claim path can never disagree about whether the window
// is open. The previous version had its own string parsing, which is exactly
// the kind of second implementation that goes stale the day the format changes.
func (s *Server) windowState() (open bool, spec, nextAt string) {
	sched := s.config().Schedule
	now := time.Now()
	loc := sched.Location()

	if occ, ok := sched.OccurrenceAt(now); ok {
		return true, occ.Spec, ""
	}

	// Shut: name the next window that opens, and the one that would have been
	// running now (so the badge still says what the schedule is).
	if open, ok := sched.OccurrenceAt(now.Add(-24 * time.Hour)); ok {
		spec = open.Spec
	}
	for _, occ := range sched.Occurrences(now, now.Add(8*24*time.Hour)) {
		if occ.Start.After(now) {
			return false, firstNonEmpty(spec, occ.Spec), occ.Start.In(loc).Format("Mon 15:04")
		}
	}
	return false, spec, "?"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

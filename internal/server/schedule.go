package server

import "time"

// ClaimRefused says why a node was told to come back later. The node treats
// every refusal the same (back off and retry) but the reason is worth having
// in a log line when somebody asks why nothing ran last night.
const (
	RefusedWindow   = "window-shut"
	RefusedDraining = "draining"
	RefusedEmpty    = "empty"
)

// claimsAllowed reports whether a node may start new work now.
//
// The window is the envelope. Drain is the tail we refuse to start in, so a
// file claimed at 05:29 with a 15-minute drain cannot still be encoding when
// the backup job wants the disk at 05:45. A job already running is never
// killed: it is cheaper to let one film finish than to throw away an hour of
// encoding.
func (s *Server) claimsAllowed(now time.Time) (bool, string) {
	loc := time.UTC
	if s.cfg.Schedule.Timezone != "" {
		if l, err := time.LoadLocation(s.cfg.Schedule.Timezone); err == nil {
			loc = l
		}
	}
	local := now.In(loc)

	spec := s.cfg.Schedule.Weekday
	if wd := local.Weekday(); wd == time.Saturday || wd == time.Sunday {
		spec = s.cfg.Schedule.Weekend
	}
	start, end, err := parseWindow(spec)
	if err != nil {
		return false, RefusedWindow
	}
	if !withinWindow(local, start, end) {
		return false, RefusedWindow
	}

	drain := time.Duration(s.cfg.Schedule.DrainBeforeMinutes) * time.Minute
	if drain > 0 && minutesUntilClose(local, end) <= drain {
		return false, RefusedDraining
	}
	return true, ""
}

// minutesUntilClose is how long the window has left, handling the wrap past
// midnight the same way withinWindow does.
func minutesUntilClose(now time.Time, end clockMinute) time.Duration {
	cur := now.Hour()*60 + now.Minute()
	left := end.minutes() - cur
	if left < 0 {
		left += 24 * 60
	}
	return time.Duration(left) * time.Minute
}

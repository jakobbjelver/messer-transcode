package server

import (
	"context"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// ClaimRefused says why a node was told to come back later. The node treats
// every refusal the same (back off and retry) but the reason is worth having
// in a log line when somebody asks why nothing ran last night.
const (
	RefusedWindow   = "window-shut"
	RefusedDraining = "draining"
	RefusedEmpty    = "empty"
	RefusedPaused   = "paused"
	RefusedForced   = "window-forced-shut"
)

// claimsAllowed reports whether a node may start new work now.
//
// The order is the operator's, then the schedule's:
//
//  1. a pause refuses everything — that is what it is for;
//  2. an override forces the window open (until it expires) or shut;
//  3. otherwise the schedule decides, with the drain tail refusing starts so a
//     file claimed at 05:29 with a 15-minute drain cannot still be encoding
//     when the backup job wants the disk.
//
// A job already running is never killed: it is cheaper to let one film finish
// than to throw away an hour of encoding.
func (s *Server) claimsAllowed(ctx context.Context, now time.Time) (bool, string) {
	if ctl, err := s.st.Control(ctx); err == nil {
		if ctl.Paused {
			return false, RefusedPaused
		}
		switch ctl.Override {
		case store.OverrideOpen:
			return true, ""
		case store.OverrideClose:
			return false, RefusedForced
		}
	}
	// A control read that failed (a locked database, say) falls back to the
	// schedule rather than to a refusal: refusing would look like an empty
	// window and cost a night, while allowing is exactly what the config says.

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

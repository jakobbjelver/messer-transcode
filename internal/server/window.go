package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// windowState reports whether the node may work now, the window it applies,
// and — when shut — when it next opens.
func (s *Server) windowState() (open bool, spec, nextAt string) {
	loc := time.UTC
	if s.cfg.Schedule.Timezone != "" {
		if l, err := time.LoadLocation(s.cfg.Schedule.Timezone); err == nil {
			loc = l
		}
	}
	now := time.Now().In(loc)

	spec = s.cfg.Schedule.Weekday
	if wd := now.Weekday(); wd == time.Saturday || wd == time.Sunday {
		spec = s.cfg.Schedule.Weekend
	}

	start, end, err := parseWindow(spec)
	if err != nil {
		return false, spec, "?"
	}
	open = withinWindow(now, start, end)
	if !open {
		nextAt = start.format()
	}
	return open, spec, nextAt
}

// idleNote is the sentence shown when nothing is encoding.
func (s *Server) idleNote() string {
	open, _, next := s.windowState()
	if open {
		return "The window is open — start the node and it will claim work."
	}
	return "Next window opens at " + next + ". Nothing is queued to a machine that is not running."
}

func (s *Server) windowNote() string {
	open, spec, next := s.windowState()
	if open {
		return "open · " + spec
	}
	return "shut · opens " + next
}

// clockMinute is a wall-clock time of day, with no date attached.
type clockMinute struct {
	h, m int
}

func (c clockMinute) format() string { return fmt.Sprintf("%02d:%02d", c.h, c.m) }

// minutes returns the offset from midnight, so comparisons are arithmetic.
func (c clockMinute) minutes() int { return c.h*60 + c.m }

func parseWindow(spec string) (start, end clockMinute, err error) {
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return start, end, fmt.Errorf("window %q is not HH:MM-HH:MM", spec)
	}
	start, err = parseClock(parts[0])
	if err != nil {
		return start, end, err
	}
	end, err = parseClock(parts[1])
	return start, end, err
}

func parseClock(s string) (clockMinute, error) {
	var c clockMinute
	bits := strings.Split(strings.TrimSpace(s), ":")
	if len(bits) != 2 {
		return c, fmt.Errorf("clock %q is not HH:MM", s)
	}
	h, err := strconv.Atoi(bits[0])
	if err != nil || h < 0 || h > 23 {
		return c, fmt.Errorf("hour in %q is out of range", s)
	}
	m, err := strconv.Atoi(bits[1])
	if err != nil || m < 0 || m > 59 {
		return c, fmt.Errorf("minute in %q is out of range", s)
	}
	return clockMinute{h: h, m: m}, nil
}

// withinWindow handles the wrap-past-midnight case, which is the normal one:
// 00:30-05:30 is the small hours of the same day, but 22:00-06:00 is not.
func withinWindow(now time.Time, start, end clockMinute) bool {
	cur := now.Hour()*60 + now.Minute()
	s, e := start.minutes(), end.minutes()
	if s <= e {
		return cur >= s && cur < e
	}
	return cur >= s || cur < e
}

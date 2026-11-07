// Package report answers "what did the last few runs actually do?".
//
// A run is one occurrence of the configured window — whatever hours that
// window covers. Nothing here assumes the window is at night: the schedule is
// read from the config every time, so a daytime window reports exactly the
// same way.
package report

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// Window is one occurrence of the schedule, in local time.
type Window struct {
	Start time.Time
	End   time.Time
	// Spec is the HH:MM-HH:MM this window came from, and Kind says which
	// entry of the schedule that was.
	Spec string
	Kind string // "weekday" or "weekend"
}

// Run is one window and what happened inside it.
type Run struct {
	Window
	// Open is true for the window containing now, which may be half done.
	Open bool

	Done    int   `json:"done"`
	Failed  int   `json:"failed"`
	Skipped int   `json:"skipped"`
	Remuxed int   `json:"remuxed"`
	Encoded int   `json:"encoded"`
	Before  int64 `json:"bytes_before"`
	After   int64 `json:"bytes_after"`

	// BusySeconds is the sum of encode runtimes: time the node was actually
	// working, against WindowSeconds for the length of the window.
	BusySeconds int64 `json:"busy_seconds"`

	// BusyPercent is Utilisation resolved at collection time, so templates and
	// JSON do not each have to know what "now" was.
	BusyPercent float64 `json:"busy_percent"`

	VmafMean float64 `json:"vmaf_mean,omitempty"`
	VmafMin  float64 `json:"vmaf_min,omitempty"`
	VmafN    int     `json:"vmaf_n,omitempty"`

	FailedCodes  map[string]int `json:"failed_codes,omitempty"`
	SkippedCodes map[string]int `json:"skipped_codes,omitempty"`
}

// Reclaimed is the bytes this run returned to the library.
func (r Run) Reclaimed() int64 {
	if r.Before <= r.After {
		return 0
	}
	return r.Before - r.After
}

// WindowSeconds is the length of the window, or how much of it has elapsed for
// a run that is still open.
func (r Run) WindowSeconds(now time.Time) int64 {
	end := r.End
	if r.Open && now.Before(end) {
		end = now
	}
	return int64(end.Sub(r.Start).Seconds())
}

// Utilisation is the share of the window the node spent encoding, 0-100.
func (r Run) Utilisation(now time.Time) float64 {
	w := r.WindowSeconds(now)
	if w <= 0 {
		return 0
	}
	return float64(r.BusySeconds) / float64(w) * 100
}

// Windows returns the last n window occurrences up to now, newest first,
// including the one in progress.
func Windows(sched config.Schedule, now time.Time, n int) []Window {
	loc := time.UTC
	if sched.Timezone != "" {
		if l, err := time.LoadLocation(sched.Timezone); err == nil {
			loc = l
		}
	}
	local := now.In(loc)

	var out []Window
	for day := 0; day < 400 && len(out) < n; day++ {
		d := local.AddDate(0, 0, -day)
		spec, kind := sched.Weekday, "weekday"
		if wd := d.Weekday(); wd == time.Saturday || wd == time.Sunday {
			spec, kind = sched.Weekend, "weekend"
		}
		startMin, endMin, err := parseWindow(spec)
		if err != nil {
			continue
		}
		start := time.Date(d.Year(), d.Month(), d.Day(), startMin/60, startMin%60, 0, 0, loc)
		endDay := d
		if endMin <= startMin {
			endDay = d.AddDate(0, 0, 1)
		}
		end := time.Date(endDay.Year(), endDay.Month(), endDay.Day(), endMin/60, endMin%60, 0, 0, loc)

		// A window that has not started yet belongs to the future; skip it
		// rather than reporting an empty run for tonight.
		if start.After(local) {
			continue
		}
		out = append(out, Window{Start: start, End: end, Spec: spec, Kind: kind})
	}
	return out
}

// Collect builds the report for the last n runs.
func Collect(ctx context.Context, st *store.Store, sched config.Schedule, now time.Time, n int) ([]Run, error) {
	windows := Windows(sched, now, n)
	runs := make([]Run, 0, len(windows))
	for _, w := range windows {
		r, err := one(ctx, st, w, now)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, nil
}

func one(ctx context.Context, st *store.Store, w Window, now time.Time) (Run, error) {
	r := Run{Window: w, Open: !now.Before(w.Start) && now.Before(w.End)}
	from, to := w.Start.Unix(), w.End.Unix()
	db := st.DB()

	// Finished work inside the window's span.
	row := db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(CASE WHEN bytes_before > 0 THEN bytes_before ELSE size_bytes END), 0),
       COALESCE(SUM(CASE WHEN bytes_after  > 0 THEN bytes_after  ELSE size_bytes END), 0),
       COALESCE(SUM(CASE WHEN started_at > 0 AND finished_at > started_at
                         THEN finished_at - started_at ELSE 0 END), 0)
FROM files
WHERE state='done' AND finished_at >= ? AND finished_at < ?`, from, to)
	if err := row.Scan(&r.Done, &r.Before, &r.After, &r.BusySeconds); err != nil {
		return r, fmt.Errorf("run %s: %w", w.Start.Format(time.DateOnly), err)
	}

	if r.Done > 0 {
		_ = db.QueryRowContext(ctx, `
SELECT COUNT(*), AVG(vmaf), MIN(vmaf) FROM files
WHERE state='done' AND vmaf > 0 AND finished_at >= ? AND finished_at < ?`,
			from, to).Scan(&r.VmafN, &r.VmafMean, &r.VmafMin)
		_ = db.QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(CASE WHEN decision='remux' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN decision='encode' THEN 1 ELSE 0 END), 0)
FROM files WHERE state='done' AND finished_at >= ? AND finished_at < ?`, from, to).
			Scan(&r.Remuxed, &r.Encoded)
	}

	// Failures are stamped by updated_at: a file that failed has no
	// finished_at, and updated_at is when the failure was recorded.
	r.FailedCodes = map[string]int{}
	rows, err := db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(last_error_code,''), 'E_UNKNOWN'), COUNT(*)
FROM files
WHERE state='failed' AND updated_at >= ? AND updated_at < ?
GROUP BY 1`, from, to)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var code string
		var c int
		if err := rows.Scan(&code, &c); err != nil {
			rows.Close()
			return r, err
		}
		r.FailedCodes[code] = c
		r.Failed += c
	}
	rows.Close()

	r.SkippedCodes = map[string]int{}
	rows, err = db.QueryContext(ctx, `
SELECT COALESCE(NULLIF(reason_code,''), 'R_UNKNOWN'), COUNT(*)
FROM files
WHERE state='skipped' AND updated_at >= ? AND updated_at < ?
GROUP BY 1`, from, to)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var code string
		var c int
		if err := rows.Scan(&code, &c); err != nil {
			rows.Close()
			return r, err
		}
		r.SkippedCodes[code] = c
		r.Skipped += c
	}
	rows.Close()

	r.BusyPercent = r.Utilisation(now)
	if len(r.FailedCodes) == 0 {
		r.FailedCodes = nil
	}
	if len(r.SkippedCodes) == 0 {
		r.SkippedCodes = nil
	}
	return r, nil
}

// parseWindow splits HH:MM-HH:MM into minutes since midnight.
func parseWindow(spec string) (start, end int, err error) {
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("window %q is not HH:MM-HH:MM", spec)
	}
	if start, err = parseClock(parts[0]); err != nil {
		return 0, 0, err
	}
	end, err = parseClock(parts[1])
	return start, end, err
}

func parseClock(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("clock %q is not HH:MM", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("clock %q is out of range", s)
	}
	return h*60 + m, nil
}

// Summary is the one-line answer for a run: files, size, quality, failures.
func (r Run) Summary(now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s–%s (%s)", r.Start.Format("Mon 02 Jan"),
		r.Start.Format("15:04"), r.End.Format("15:04"), r.Kind)
	if r.Open {
		b.WriteString(" · in progress")
	}
	fmt.Fprintf(&b, ": %d done", r.Done)
	if r.Remuxed > 0 || r.Encoded > 0 {
		fmt.Fprintf(&b, " (%d encoded, %d remuxed)", r.Encoded, r.Remuxed)
	}
	if gb := float64(r.Reclaimed()) / 1e9; gb > 0 {
		fmt.Fprintf(&b, ", %.2f GB reclaimed", gb)
	}
	if r.VmafN > 0 {
		fmt.Fprintf(&b, ", VMAF %.1f", r.VmafMean)
	}
	if r.Failed > 0 {
		fmt.Fprintf(&b, ", %d failed %v", r.Failed, sortedCodes(r.FailedCodes))
	}
	fmt.Fprintf(&b, ", %.0f%% of the window busy", r.Utilisation(now))
	return b.String()
}

func sortedCodes(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, fmt.Sprintf("%s×%d", k, m[k]))
	}
	// Stable order for logs and tests.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

var _ = sql.ErrNoRows

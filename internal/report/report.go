// Package report answers "what did the last few runs actually do?".
//
// A run is one occurrence of the configured window — whatever hours that
// window covers. Nothing here assumes the window is at night: the schedule is
// read from the config every time, so a daytime window reports exactly the
// same way.
package report

import (
	"context"
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
	// ID is the run's identity: the unix start of the window, and the key the
	// ledger stores. It is stable across schedule edits, which a derived run
	// never was.
	ID int64 `json:"id"`
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

// Utilisation is the share of the window covered by file time, 0-100.
//
// It is an upper bound, not encoder utilisation: ingest, encode and placement
// run in separate lanes, so two files' wall time can overlap. Clamped to 100
// for that reason — a number above that reads as a bug rather than as work.
func (r Run) Utilisation(now time.Time) float64 {
	w := r.WindowSeconds(now)
	if w <= 0 {
		return 0
	}
	u := float64(r.BusySeconds) / float64(w) * 100
	if u > 100 {
		return 100
	}
	return u
}

// Windows returns the last n window occurrences up to now, newest first,
// including the one in progress.
//
// The arithmetic moved to config.Occurrence so the ledger, the claim path and
// this report all resolve a window the same way — three copies of "does this
// wrap past midnight" is how a schedule change silently re-cuts history.
func Windows(sched config.Schedule, now time.Time, n int) []Window {
	occs := sched.PreviousOccurrences(now, n)
	out := make([]Window, 0, len(occs))
	for _, o := range occs {
		out = append(out, Window{Start: o.Start, End: o.End, Spec: o.Spec, Kind: o.Kind})
	}
	return out
}

// Collect builds the report for the last n runs from the ledger.
//
// It first materialises the windows that have started (so an empty run is still
// a row) and then reads what the store recorded, rather than recomputing the
// past from the schedule this request happens to see.
func Collect(ctx context.Context, st *store.Store, sched config.Schedule, now time.Time, n int) ([]Run, error) {
	if n <= 0 {
		return nil, nil
	}
	if err := st.SyncRuns(ctx, sched.PreviousOccurrences(now, n), now); err != nil {
		return nil, fmt.Errorf("sync runs: %w", err)
	}

	// Read deeper than n: a manual run (a forced window) shares the ledger and
	// interleaves with the scheduled ones.
	rows, err := st.RunRows(ctx, n*2+10)
	if err != nil {
		return nil, fmt.Errorf("read runs: %w", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	aggs, err := st.RunAggregates(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("aggregate runs: %w", err)
	}

	out := make([]Run, 0, n)
	for _, row := range rows {
		if len(out) >= n {
			break
		}
		out = append(out, FromRow(row, aggs[row.ID], now))
	}
	return out, nil
}

// FromRow turns a ledger row and its aggregates into the report's shape.
func FromRow(row store.RunRow, agg store.RunAggregate, now time.Time) Run {
	w := Window{
		Start: time.Unix(row.StartAt, 0).In(time.Local),
		Spec:  row.Spec,
		Kind:  row.Kind,
	}
	if row.EndAt > 0 {
		w.End = time.Unix(row.EndAt, 0).In(time.Local)
	}
	r := Run{
		ID:          row.ID,
		Window:      w,
		Open:        row.ClosedAt == 0 && now.After(w.Start),
		Done:        agg.Done,
		Before:      agg.Before,
		After:       agg.After,
		BusySeconds: agg.BusySeconds,
		Encoded:     agg.Encoded,
		Remuxed:     agg.Remuxed,
		VmafN:       agg.VmafN,
	}
	if agg.VmafN > 0 {
		r.VmafMean = agg.VmafSum / float64(agg.VmafN)
		r.VmafMin = agg.VmafMin
	}
	for code, n := range agg.FailedCodes {
		if r.FailedCodes == nil {
			r.FailedCodes = map[string]int{}
		}
		r.FailedCodes[code] += n
		r.Failed += n
	}
	for code, n := range agg.SkipCodes {
		if r.SkippedCodes == nil {
			r.SkippedCodes = map[string]int{}
		}
		r.SkippedCodes[code] += n
		r.Skipped += n
	}
	r.BusyPercent = r.Utilisation(now)
	return r
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

// Package quarantine expires the originals this tool has kept.
//
// The safety model is "never delete": an encode lands, the original moves to a
// dated directory, and it stays there for safety.keep_quarantine_days. Those
// originals are not free — every re-encode grows the volume by the size of the
// file it replaced until its day comes — so the expiry is part of the model,
// not housekeeping bolted on afterwards.
package quarantine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Day is one dated directory and what happened to it.
type Day struct {
	Date    time.Time
	Path    string
	Files   int
	Bytes   int64
	Expired bool
	Removed bool
	Err     error
}

// Result is the whole sweep.
type Result struct {
	Root    string
	DryRun  bool
	Days    []Day
	Removed int
	Freed   int64
	Kept    int
	KeptGB  float64
	// Oldest is the oldest keep that remains after this sweep, which is what
	// tells an operator whether the schedule is actually running.
	Oldest time.Time
	// Ignored are entries directly under root that are not dated directories.
	// They are never touched: this tool removes only what it named itself.
	Ignored []string
}

// Sweep removes dated directories older than keepDays.
//
// A directory is expired when its date plus keepDays is at or before the start
// of today, so with keepDays=14 a directory written on the 24th goes on the
// 7th of the following month.
func Sweep(ctx context.Context, root string, keepDays int, now time.Time, dryRun bool) (Result, error) {
	res := Result{Root: root, DryRun: dryRun}
	if keepDays < 0 {
		return res, fmt.Errorf("keep_quarantine_days is %d; refusing to sweep with a negative window", keepDays)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return res, nil // nothing has been quarantined yet
		}
		return res, err
	}

	today := startOfDay(now)
	cutoff := today.AddDate(0, 0, -keepDays)

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !e.IsDir() {
			res.Ignored = append(res.Ignored, e.Name())
			continue
		}
		date, err := time.ParseInLocation("2006-01-02", e.Name(), now.Location())
		if err != nil {
			// Not a directory this package created — leave it alone, loudly.
			res.Ignored = append(res.Ignored, e.Name())
			continue
		}
		d := Day{Date: date, Path: filepath.Join(root, e.Name())}
		d.Files, d.Bytes = measure(ctx, d.Path)
		d.Expired = !date.After(cutoff)

		if d.Expired {
			// A dry run reports what it WOULD free, because that is the number
			// the operator ran it to see.
			res.Removed += d.Files
			res.Freed += d.Bytes
			if !dryRun {
				if err := os.RemoveAll(d.Path); err != nil {
					d.Err = err
					res.Removed -= d.Files
					res.Freed -= d.Bytes
				} else {
					d.Removed = true
				}
			}
		} else {
			res.Kept += d.Files
			res.KeptGB += float64(d.Bytes) / 1e9
			if res.Oldest.IsZero() || date.Before(res.Oldest) {
				res.Oldest = date
			}
		}
		res.Days = append(res.Days, d)
	}

	sort.Slice(res.Days, func(i, j int) bool { return res.Days[i].Date.Before(res.Days[j].Date) })
	return res, nil
}

// measure counts what a directory holds without following symlinks: a link in
// the quarantine is a link, not a licence to read outside it.
func measure(ctx context.Context, dir string) (files int, bytes int64) {
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable entry is not a reason to stop counting
		}
		if n%64 == 0 && ctx.Err() != nil {
			return ctx.Err()
		}
		n++
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// Summary is one line an operator or a log can read.
func (r Result) Summary() string {
	verb := "removed"
	if r.DryRun {
		verb = "would remove"
	}
	if r.Removed == 0 && r.DryRun {
		return fmt.Sprintf("nothing to expire; %d files (%.1f GB) kept, oldest %s",
			r.Kept, r.KeptGB, r.Oldest.Format("2006-01-02"))
	}
	s := fmt.Sprintf("%s %d files (%.1f GB) across %d dated directories",
		verb, r.Removed, float64(r.Freed)/1e9, countExpired(r.Days))
	if r.Kept > 0 {
		s += fmt.Sprintf("; %d files (%.1f GB) kept, oldest %s",
			r.Kept, r.KeptGB, r.Oldest.Format("2006-01-02"))
	}
	if len(r.Ignored) > 0 {
		s += fmt.Sprintf("; %d entries ignored (not dated directories)", len(r.Ignored))
	}
	return s
}

func countExpired(days []Day) int {
	n := 0
	for _, d := range days {
		if d.Expired {
			n++
		}
	}
	return n
}

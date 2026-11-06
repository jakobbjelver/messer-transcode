package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/plan"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

func cmdPlan(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	dbPath := fs.String("db", "", "state database (default: <config dir>/state.db)")
	workers := fs.Int("workers", 4, "parallel probe workers")
	limit := fs.Int("limit", 0, "probe at most N files (0 = all)")
	skipProbe := fs.Bool("no-probe", false, "decide from existing probes only")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitInvalid
	}

	db := *dbPath
	if db == "" {
		db = filepath.Join(filepath.Dir(*cfgPath), "state.db")
	}
	st, err := store.Open(db)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}
	defer func() { _ = st.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	progress := func(p plan.Progress) {
		if jsonOut {
			line, _ := json.Marshal(p)
			say(stderr, "%s\n", line)
			return
		}
		say(stderr, "\r  %s %d/%d  %-6s  %s ",
			p.Phase, p.Done, p.Total, p.ETA, shorten(p.Path, 52))
	}

	rep, err := plan.Run(ctx, plan.Options{
		Config:    cfg,
		Store:     st,
		Workers:   *workers,
		Limit:     *limit,
		SkipProbe: *skipProbe,
		Progress:  progress,
	})
	if err != nil {
		say(stderr, "\n%v\n", err)
		return ExitRuntime
	}
	say(stderr, "\n")

	if jsonOut {
		writeJSON(stdout, rep)
		return ExitOK
	}
	printPlan(stdout, *cfgPath, db, rep)
	return ExitOK
}

func printPlan(w io.Writer, cfgPath, db string, r *plan.Report) {
	say(w, "plan  %s\n", cfgPath)
	say(w, "      %s\n\n", db)

	say(w, "[01] scanned    %d files\n", r.Scanned)
	for _, l := range r.LibraryStats {
		say(w, "                %s: %d files (%d skipped, %d excluded)\n",
			l.Name, l.Files, l.Skipped, l.Filtered)
	}
	say(w, "[02] probed     %d of %d needed (%d errors)\n", r.Probed, r.NeedingProbe, r.ProbeErrors)
	say(w, "[03] decided    %d\n\n", r.Decided)

	say(w, "%-10s %-22s %6s %12s %12s %12s\n",
		"DECISION", "REASON", "FILES", "SIZE", "PROJECTED", "SAVE")
	for _, b := range r.Buckets {
		say(w, "%-10s %-22s %6d %12s %12s %12s\n",
			b.Decision, b.ReasonCode, b.Files,
			bytesHuman(b.SizeBytes), bytesHuman(b.EstimateOut), bytesHuman(b.SizeBytes-b.EstimateOut))
	}
	say(w, "%s\n", strings.Repeat("─", 74))
	say(w, "%-10s %-22s %6d %12s %12s %12s\n",
		"total", "", countFiles(r.Buckets), bytesHuman(r.TotalBytes),
		bytesHuman(r.Projected), bytesHuman(r.Reclaimable))
	say(w, "\nstates   %s\n", statesHuman(r.States))
	say(w, "elapsed  %s\n", r.Elapsed)
}

func statesHuman(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

func countFiles(buckets []store.Bucket) int {
	n := 0
	for _, b := range buckets {
		n += b.Files
	}
	return n
}

// bytesHuman picks the unit a person would use. Decimal units, because the
// library was measured in decimal GB.
func bytesHuman(bytes int64) string {
	if bytes <= 0 {
		return "—"
	}
	units := []struct {
		limit float64
		name  string
	}{
		{1e12, "TB"},
		{1e9, "GB"},
		{1e6, "MB"},
		{1e3, "KB"},
	}
	f := float64(bytes)
	for _, u := range units {
		if f >= u.limit {
			return fmt.Sprintf("%.2f %s", f/u.limit, u.name)
		}
	}
	return fmt.Sprintf("%d B", bytes)
}

func shorten(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "…" + s[len(s)-max+1:]
}

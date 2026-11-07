package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/report"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// cmdReport answers "what did the last few runs do?".
//
// A run is one occurrence of the window in the config, so this says nothing
// about nights: point the schedule at 09:00–10:00 and it reports that window
// the same way.
func cmdReport(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	dbPath := fs.String("db", "", "state database (default: <config dir>/state.db)")
	last := fs.Int("last", 7, "how many runs to report, newest first")
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

	now := time.Now()
	runs, err := report.Collect(context.Background(), st, cfg.Schedule, now, *last)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}

	if jsonOut {
		out := struct {
			Schedule config.Schedule `json:"schedule"`
			Runs     []report.Run    `json:"runs"`
		}{cfg.Schedule, runs}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		return ExitOK
	}

	if len(runs) == 0 {
		say(stdout, "no runs yet — the first one starts at %s\n", cfg.Schedule.Weekday)
		return ExitOK
	}

	say(stdout, "schedule  %s weekdays · %s weekends · %s\n",
		cfg.Schedule.Weekday, cfg.Schedule.Weekend, cfg.Schedule.Timezone)
	say(stdout, "\n")
	var totalReclaimed, totalDone, totalFailed int64
	for _, r := range runs {
		say(stdout, "  %s\n", r.Summary(now))
		totalDone += int64(r.Done)
		totalFailed += int64(r.Failed)
		totalReclaimed += r.Reclaimed()
	}
	say(stdout, "\n  %d runs · %d done · %d failed · %.2f GB reclaimed\n",
		len(runs), totalDone, totalFailed, float64(totalReclaimed)/1e9)

	if totalDone == 0 && totalFailed == 0 {
		fmt.Fprintf(stderr, "%s", "")
	}
	return ExitOK
}

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
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
// scheduleText is the schedule in words, for the report header.
func scheduleText(s config.Schedule) string {
	if len(s.Windows) == 0 {
		return "no window"
	}
	var parts []string
	for _, w := range s.Windows {
		parts = append(parts, strings.Join(w.Days, ",")+" "+w.Start+"-"+w.End)
	}
	return strings.Join(parts, " · ")
}

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
	now := time.Now()

	// The orchestrator owns the queue, so ask it first. A local read of the
	// bind-mounted database lags the container's writes by minutes — a report
	// built from a stale snapshot is worse than no report.
	runs, source := runsFromServer(cfg.Server.URL, *last)

	if runs == nil {
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
		runs, err = report.Collect(context.Background(), st, cfg.Schedule, now, *last)
		if err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		source = "database " + db
	}

	if jsonOut {
		out := struct {
			Schedule config.Schedule `json:"schedule"`
			Source   string          `json:"source"`
			Runs     []report.Run    `json:"runs"`
		}{cfg.Schedule, source, runs}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		return ExitOK
	}

	if len(runs) == 0 {
		next := cfg.Schedule.PreviousOccurrences(now, 1)
		if len(next) == 0 {
			say(stdout, "no runs yet, and no window has opened\n")
			return ExitOK
		}
		say(stdout, "no runs yet — the newest window was %s\n", next[0].Spec)
		return ExitOK
	}

	say(stdout, "schedule  %s · %s\n", scheduleText(cfg.Schedule), cfg.Schedule.Timezone)
	say(stdout, "source    %s\n", source)
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
	return ExitOK
}

// runsFromServer reads the runs from the live orchestrator. Nil means "not
// reachable", which is not an error: the caller falls back to a local read.
func runsFromServer(base string, last int) ([]report.Run, string) {
	if base == "" {
		return nil, ""
	}
	url := strings.TrimRight(base, "/") + "/api/v1/runs?last=" + strconv.Itoa(last)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, ""
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Runs []report.Run `json:"runs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, ""
	}
	if out.Runs == nil {
		out.Runs = []report.Run{}
	}
	return out.Runs, "orchestrator " + base
}

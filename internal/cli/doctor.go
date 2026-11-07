package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/health"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// cmdDoctor answers "is this system able to do its job" in one object.
//
// Exit codes carry the verdict so a shell script does not have to parse JSON:
// 0 ok, 1 degraded, 3 broken. Usage errors stay 2.
func cmdDoctor(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	dbPath := fs.String("db", "", "state database (default: <config dir>/state.db)")
	role := fs.String("role", "node", "which set of checks to run: node or server")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	in := health.In{ConfigPath: *cfgPath, Role: health.Role(*role)}
	db := *dbPath
	if db == "" {
		db = filepath.Join(filepath.Dir(*cfgPath), "state.db")
	}
	in.DBPath = db

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		in.OpenError = err
	} else {
		in.Config = cfg
	}

	// Ask the live orchestrator what it thinks of the queue. A local read of
	// the bind-mounted database lags the container's writes by minutes, which
	// invents failures — a healthy node reported as silent is worse than no
	// check at all.
	if in.Config != nil && in.Config.Server.URL != "" {
		in.ServerReport = fetchServerReport(in.Config.Server.URL)
	}

	// A database that will not open is a finding, not a reason to stop: the
	// report should still say what it can about everything else. With the
	// orchestrator's verdict in hand this is a fallback, so it stays read-only.
	if in.ServerReport == nil {
		if st, err := store.Open(db); err == nil {
			in.Store = st
			defer func() { _ = st.Close() }()
		} else if in.OpenError == nil {
			in.OpenError = err
		}
	}

	rep := health.Collect(context.Background(), in)

	if jsonOut {
		writeJSON(stdout, rep)
	} else {
		printReport(stdout, rep)
	}

	switch rep.Status {
	case health.OK:
		return ExitOK
	case health.Degraded:
		return ExitRuntime
	default:
		return ExitInvalid
	}
}

// fetchServerReport gets the orchestrator's verdict, or nil when it is not
// reachable. Reachability is not a failure here: doctor has to work on a machine
// where the container is down, which is exactly when it is needed.
func fetchServerReport(base string) *health.Report {
	url := strings.TrimRight(base, "/") + "/api/v1/health"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	// The endpoint answers 503 when the verdict is broken, and that body is the
	// report we want.
	var rep health.Report
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return nil
	}
	if len(rep.Checks) == 0 {
		return nil
	}
	return &rep
}

func printReport(w io.Writer, rep health.Report) {
	say(w, "messer-transcode %s (%s) · %s · %d ms\n\n",
		rep.Version["version"], rep.Version["commit"], rep.Role, rep.DurationMS)
	for _, c := range rep.Checks {
		mark := "  ok  "
		switch c.Status {
		case health.Degraded:
			mark = " warn "
		case health.Broken:
			mark = " FAIL "
		}
		say(w, "[%s] %-10s %s\n", mark, c.Name, c.Detail)
		if c.Code != "" {
			say(w, "           %s\n", c.Code)
		}
		if c.Fix != "" && c.Status != health.OK {
			say(w, "           → %s\n", c.Fix)
		}
	}
	say(w, "\n%s\n", verdictLine(rep))
}

func verdictLine(rep health.Report) string {
	var broken, warned int
	for _, c := range rep.Checks {
		switch c.Status {
		case health.Broken:
			broken++
		case health.Degraded:
			warned++
		}
	}
	switch rep.Status {
	case health.Broken:
		return fmt.Sprintf("status: broken — %d failed, %d warnings", broken, warned)
	case health.Degraded:
		return fmt.Sprintf("status: degraded — %d warnings", warned)
	default:
		return "status: ok"
	}
}

var _ = json.Marshal // kept for symmetry with other commands

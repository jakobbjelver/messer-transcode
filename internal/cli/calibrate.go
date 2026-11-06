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
	"strconv"
	"strings"
	"syscall"

	"github.com/jakobbjelver/messer-transcode/internal/calibrate"
	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

func cmdCalibrate(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	dbPath := fs.String("db", "", "state database (default: <config dir>/state.db)")
	target := fs.Float64("target", 0, "VMAF target (default: config quality.vmaf_target)")
	perTier := fs.Int("per-tier", 3, "files sampled per codec")
	rungs := fs.String("rungs", "4000,5000,6000,7000,8500", "comma-separated bitrates to test, kbps")
	window := fs.Float64("window", 60, "seconds per sample window")
	windows := fs.Int("windows", 2, "windows per file")
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

	ladder, err := parseInts(*rungs)
	if err != nil {
		say(stderr, "--rungs: %v\n", err)
		return ExitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	progress := func(e calibrate.Event) {
		if jsonOut {
			line, _ := json.Marshal(e)
			say(stderr, "%s\n", line)
			return
		}
		if e.Stage == "measure" {
			say(stderr, "\r  %d/%d  %-8s %5d kbps  VMAF %.1f   %s ",
				e.Done, e.Total, e.Codec, e.Kbps, e.VMAF, shorten(e.File, 40))
		}
	}

	res, err := calibrate.Run(ctx, calibrate.Options{
		Config: cfg, Store: st,
		Target: *target, PerTier: *perTier, Rungs: ladder,
		WindowSeconds: *window, WindowsPerFile: *windows,
		Progress: progress,
	})
	if err != nil {
		say(stderr, "\n%v\n", err)
		return ExitRuntime
	}
	say(stderr, "\n")

	if jsonOut {
		writeJSON(stdout, res)
		return ExitOK
	}
	printCalibration(stdout, res)
	return ExitOK
}

func printCalibration(w io.Writer, r *calibrate.Result) {
	say(w, "calibration  target VMAF %.1f  %d samples  %s\n\n", r.Target, len(r.Samples), r.Elapsed)

	for _, c := range r.Curves {
		say(w, "%s %dpx\n", strings.ToUpper(c.Codec), c.Height)
		say(w, "  %8s %8s %8s %8s\n", "KBPS", "VMAF", "MIN", "N")
		for _, rung := range c.Rungs {
			mark := " "
			if rung.Kbps == c.Chosen {
				mark = "←"
			}
			say(w, "  %8d %8.1f %8.1f %8d %s\n", rung.Kbps, rung.VMAF, rung.MinVMAF, rung.Measures, mark)
		}
		if c.Met {
			say(w, "  → %d kbps meets the target\n\n", c.Chosen)
		} else {
			say(w, "  → no tested rung reached the target\n\n")
		}
	}

	if len(r.Suggested) == 0 {
		say(w, "no table produced\n")
		return
	}
	say(w, "suggested quality.table\n")
	block, _ := json.MarshalIndent(r.Suggested, "  ", "  ")
	say(w, "%s\n\n", block)
	say(w, "rows marked extrapolated are scaled from the 1080p measurement, not measured.\n")
}

func parseInts(s string) ([]int, error) {
	var out []int
	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", field)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no bitrates given")
	}
	return out, nil
}

package cli

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/quarantine"
)

// cmdSweep expires quarantined originals older than safety.keep_quarantine_days.
//
// It is a dry run unless --apply is passed. Deleting a person's films is not
// something a typo should be able to do.
func cmdSweep(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("sweep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	apply := fs.Bool("apply", false, "actually delete; without it this only reports")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitInvalid
	}
	root := cfg.Paths.Quarantine
	if root == "" {
		say(stderr, "paths.quarantine is not set; there is nothing to sweep\n")
		return ExitInvalid
	}

	res, err := quarantine.Sweep(context.Background(), root, cfg.Safety.KeepQuarantineDays, time.Now(), !*apply)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			say(stderr, "%v\n", err)
			return ExitRuntime
		}
		return ExitOK
	}

	say(stdout, "quarantine  %s\n", res.Root)
	say(stdout, "keep        %d days\n", cfg.Safety.KeepQuarantineDays)
	if res.DryRun {
		say(stdout, "\nDRY RUN — nothing was deleted. Add --apply to do it.\n\n")
	} else {
		say(stdout, "\n")
	}

	for _, d := range res.Days {
		switch {
		case d.Err != nil:
			say(stdout, "  %s  ERROR %v\n", d.Date.Format("2006-01-02"), d.Err)
		case d.Expired:
			mark := "expired"
			if res.DryRun {
				mark = "would expire"
			}
			say(stdout, "  %s  %-12s %4d files  %7.1f GB\n",
				d.Date.Format("2006-01-02"), mark, d.Files, float64(d.Bytes)/1e9)
		default:
			say(stdout, "  %s  keep         %4d files  %7.1f GB\n",
				d.Date.Format("2006-01-02"), d.Files, float64(d.Bytes)/1e9)
		}
	}

	for _, name := range res.Ignored {
		say(stdout, "  ignored      %s (not a dated directory)\n", name)
	}

	say(stdout, "\n%s\n", res.Summary())
	return ExitOK
}

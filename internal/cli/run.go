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
	"syscall"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/transcode"
)

func cmdRun(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	dbPath := fs.String("db", "", "state database (default: <config dir>/state.db)")
	limit := fs.Int("limit", 1, "process at most N files (0 = all planned)")
	dryRun := fs.Bool("dry-run", false, "encode and verify, but do not replace anything")
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

	roots := map[string]string{}
	for _, lib := range cfg.Libraries {
		roots[lib.Name] = lib.Root
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// A one-off `run` still belongs to a run: stamp the occurrence it is inside
	// so the ledger attributes the work. Outside a window there is no run, and 0
	// is the honest answer (this command is the manual path).
	runID := int64(0)
	if occ, ok := cfg.Schedule.OccurrenceAt(time.Now()); ok {
		runID, _ = st.EnsureRun(ctx, store.RunRow{
			ID:      occ.Start.Unix(),
			StartAt: occ.Start.Unix(),
			EndAt:   occ.End.Unix(),
			Spec:    occ.Spec,
			Kind:    occ.Kind,
		})
	}

	work, err := st.ClaimWork(ctx, *limit, runID)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}
	if len(work) == 0 {
		say(stdout, "nothing to do\n")
		return ExitOK
	}

	logEvent := func(e transcode.Event) {
		if jsonOut {
			line, _ := json.Marshal(e)
			say(stderr, "%s\n", line)
			return
		}
		line := fmt.Sprintf("  %-9s %s", e.Stage, e.Message)
		if e.Speed > 0 {
			line += fmt.Sprintf("  %.1f×", e.Speed)
		}
		say(stderr, "%s\n", line)
	}

	failed := 0
	reclaimed := int64(0)
	started := time.Now()

	for _, w := range work {
		if ctx.Err() != nil {
			break
		}
		root := roots[w.Library]
		if !jsonOut {
			say(stdout, "\n%s\n", filepath.Base(w.Path))
		}
		res, err := transcode.Process(ctx, transcode.Options{
			Config: cfg, Reporter: st, Log: logEvent, DryRun: *dryRun,
		}, transcode.Task{
			ID: w.ID, Path: w.Path, LibraryRoot: root,
			Decision: w.Decision, TargetKbps: w.TargetKbps,
		})
		if err != nil {
			failed++
			say(stderr, "  failed: %v\n", err)
			continue
		}
		if res.BytesBefore > res.BytesAfter {
			reclaimed += res.BytesBefore - res.BytesAfter
		}
	}

	if jsonOut {
		writeJSON(stdout, map[string]any{
			"processed":  len(work),
			"failed":     failed,
			"reclaimed":  reclaimed,
			"dry_run":    *dryRun,
			"duration_s": int64(time.Since(started).Seconds()),
		})
		return ExitOK
	}

	say(stdout, "\n%d processed, %d failed, %s reclaimed in %s\n",
		len(work)-failed, failed, bytesHuman(reclaimed),
		time.Since(started).Round(time.Second))
	if failed > 0 {
		return ExitRuntime
	}
	return ExitOK
}

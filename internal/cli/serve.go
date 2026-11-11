package cli

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/server"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

func cmdServe(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	dbPath := fs.String("db", "", "state database (default: <config dir>/state.db)")
	listen := fs.String("listen", "", "address to listen on (default: from config, :8099)")
	tokenFile := fs.String("token-file", "", "read the node token from this file instead of the config")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitInvalid
	}
	// The token is a secret, so it must not live in the config file that is
	// kept in version control beside it. It comes from a file, or from the
	// environment when a deployment prefers that.
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			say(stderr, "cannot read %s: %v\n", *tokenFile, err)
			return ExitInvalid
		}
		cfg.Server.Token = strings.TrimSpace(string(b))
	}
	if cfg.Server.Token == "" {
		if v := os.Getenv("MESSER_TRANSCODE_TOKEN"); v != "" {
			cfg.Server.Token = v
		}
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

	// Start clean: anything the previous instance left mid-flight is returned to
	// the queue once, at boot, whether or not the window is open.
	if n, err := st.ReapExpired(context.Background(), 5*time.Minute); err == nil && n > 0 {
		log := slog.New(slog.NewTextHandler(stderr, nil))
		log.Warn("requeued abandoned jobs at startup", "count", n)
	}

	addr := cfg.Server.Listen
	if *listen != "" {
		addr = *listen
	}

	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if jsonOut {
		log = slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	// Attribute history to runs once. Until this release a run existed only as
	// arithmetic over the current schedule, so the attribution is derived here —
	// exactly once, against the schedule that produced the rows — and recorded.
	// After that the ledger is the record and a schedule edit cannot move it.
	//
	// It runs against the pre-windows schedule on purpose: this binary still has
	// weekday/weekend, and the migration that replaces them comes later.
	if n, err := st.BackfillRuns(context.Background(), func(ts int64) (config.Occurrence, bool) {
		return cfg.Schedule.OccurrenceAt(time.Unix(ts, 0))
	}); err != nil {
		log.Warn("run backfill", "err", err)
	} else if n > 0 {
		log.Warn("attributed history to runs", "files", n)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := server.New(cfg, st, *cfgPath, db, log)
	if err := srv.SeedConfigRevision(ctx); err != nil {
		log.Warn("config history", "err", err)
	}
	go srv.WatchConfig(ctx)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the event stream is a long-lived response and a
		// deadline would cut it every few seconds.
	}

	// Ctrl-C and SIGTERM both mean "stop taking requests, then exit", so a
	// container restart does not leave a listener bound.
	// Keep the ledger current: a window that opens with nothing claimed is still
	// a run, and a run that ended while this process was asleep must be closed.
	// One minute is plenty — a run is hours long.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				now := time.Now()
				if err := st.SyncRuns(tick, cfg.Schedule.PreviousOccurrences(now, 2), now); err != nil {
					log.Warn("run ledger", "err", err)
				}
				cancel()
			}
		}
	}()

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()

	if cfg.Server.Token == "" {
		say(stderr, "warning: server.token is unset — the node API is disabled\n")
	}
	say(stdout, "listening on %s\n", addr)

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		say(stderr, "%v\n", err)
		return ExitRuntime
	}
	return ExitOK
}

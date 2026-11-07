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

	addr := cfg.Server.Listen
	if *listen != "" {
		addr = *listen
	}

	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if jsonOut {
		log = slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	srv := server.New(cfg, st, *cfgPath, db, log)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the event stream is a long-lived response and a
		// deadline would cut it every few seconds.
	}

	// Ctrl-C and SIGTERM both mean "stop taking requests, then exit", so a
	// container restart does not leave a listener bound.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

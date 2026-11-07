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
	"github.com/jakobbjelver/messer-transcode/internal/node"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/transcode"
)

func cmdNode(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("node", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	serverURL := fs.String("server", "", "orchestrator URL (default: server.url in the config)")
	serverToken := fs.String("token", "", "node token (default: server.token in the config)")
	name := fs.String("name", "", "node name for the logs (default: hostname)")
	journalPath := fs.String("journal", "", "crash journal (default: <config dir>/node.journal)")
	once := fs.Bool("once", false, "claim and process one file, then exit")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		say(stderr, "%v\n", err)
		return ExitInvalid
	}

	url := *serverURL
	if url == "" {
		url = cfg.Server.URL
	}
	token := *serverToken
	if token == "" {
		token = cfg.Server.Token
	}
	if url == "" {
		say(stderr, "no orchestrator URL: pass --server or set server.url\n")
		return ExitUsage
	}
	if token == "" {
		say(stderr, "no token: pass --token or set server.token\n")
		return ExitUsage
	}

	nodeID := *name
	if nodeID == "" {
		if h, err := os.Hostname(); err == nil {
			nodeID = h
		} else {
			nodeID = "node"
		}
	}

	journal := *journalPath
	if journal == "" {
		journal = filepath.Join(filepath.Dir(*cfgPath), "node.journal")
	}

	client := node.NewClient(url, token, nodeID)

	logEvent := func(e transcode.Event) {
		if jsonOut {
			b, _ := json.Marshal(e)
			say(stdout, "%s\n", b)
			return
		}
		if e.Path != "" && e.Stage == "claim" {
			say(stdout, "\n%s\n", filepath.Base(e.Path))
		}
		if e.Message != "" {
			say(stdout, "  %-9s %s\n", e.Stage, e.Message)
		}
	}

	agent := node.New(cfg, client, journal, logEvent)
	agent.MaxJobs = 0
	if *once {
		agent.MaxJobs = 1
	}

	roots := map[string]string{}
	for _, lib := range cfg.Libraries {
		roots[lib.Name] = lib.Root
	}
	agent.Process = func(ctx context.Context, job store.Work) error {
		root := roots[job.Library]
		if root == "" {
			return fmt.Errorf("library %q is not configured on this node", job.Library)
		}
		_, err := transcode.Process(ctx, transcode.Options{
			Config:   cfg,
			Reporter: client,
			Log:      logEvent,
		}, transcode.Task{
			ID:          job.ID,
			Path:        job.Path,
			LibraryRoot: root,
			Decision:    job.Decision,
			TargetKbps:  job.TargetKbps,
		})
		return err
	}

	// One signal means "finish this file and stop". A cancelled context stops
	// the claim loop but deliberately does not cancel the encode in flight.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		say(stderr, "draining: finishing the current file, then exiting\n")
		second := make(chan os.Signal, 1)
		signal.Notify(second, os.Interrupt, syscall.SIGTERM)
		<-second
		say(stderr, "second signal: exiting now\n")
		os.Exit(130)
	}()

	say(stdout, "node %s → %s\n", nodeID, url)
	started := time.Now()
	if err := agent.Run(ctx); err != nil {
		say(stderr, "node: %v\n", err)
		return ExitRuntime
	}
	if !*once {
		say(stdout, "stopped after %s\n", time.Since(started).Round(time.Second))
	}
	return ExitOK
}

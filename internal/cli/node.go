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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/node"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/transcode"
)

// liveConfig is the config the node is working from, swapped by the reload
// loop and read by every job. A pointer behind a mutex, not a value copy: the
// reads are frequent and the writes are one every few seconds at most.
type liveConfig struct {
	mu  sync.RWMutex
	cur *config.Config
}

func (l *liveConfig) get() *config.Config {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cur
}

// set installs next and reports whether it differs from what was there.
func (l *liveConfig) set(next *config.Config) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, err1 := config.Render(l.cur)
	cur, err2 := config.Render(next)
	if err1 == nil && err2 == nil && string(prev) == string(cur) {
		return false
	}
	l.cur = next
	return true
}

func cmdNode(stdout, stderr io.Writer, args []string, jsonOut bool) int {
	fs := flag.NewFlagSet("node", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", "config.json", "path to config.json")
	serverURL := fs.String("server", "", "orchestrator URL (default: server.url in the config)")
	serverToken := fs.String("token", "", "node token (default: server.token in the config)")
	tokenFile := fs.String("token-file", "", "read the node token from this file")
	name := fs.String("name", "", "node name for the logs (default: hostname)")
	journalPath := fs.String("journal", "", "crash journal (default: <config dir>/node.journal)")
	once := fs.Bool("once", false, "claim and process one file, then exit")
	retryFor := fs.Duration("report-retry", 0,
		"how long a final report waits out an orchestrator that is down (default 90s)")
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
	if token == "" && *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			say(stderr, "cannot read %s: %v\n", *tokenFile, err)
			return ExitInvalid
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		token = cfg.Server.Token
	}
	if token == "" {
		token = os.Getenv("MESSER_TRANSCODE_TOKEN")
	}
	if url == "" {
		say(stderr, "no orchestrator URL: pass --server or set server.url\n")
		return ExitUsage
	}
	if token == "" {
		say(stderr, "no token: pass --token or --token-file, or set server.token\n")
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

	// Create the working directories instead of failing on a fresh install: a
	// missing subdirectory under a mounted volume is not a configuration error.
	for _, dir := range []string{cfg.Paths.Scratch, cfg.Paths.Quarantine} {
		if dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				say(stderr, "cannot create %s: %v\n", dir, err)
				return ExitRuntime
			}
		}
	}

	journal := *journalPath
	if journal == "" {
		journal = filepath.Join(filepath.Dir(*cfgPath), "node.journal")
	}

	client := node.NewClient(url, token, nodeID)
	if *retryFor > 0 {
		client.RetryFor = *retryFor
	}

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

	// The node re-reads its config on a ticker, so a change made on the
	// configuration screen reaches the machine that does the work without a
	// restart. A job already running keeps the config it started with: its
	// closures captured that pointer, and half a job under new paths is worse
	// than one more file under the old ones.
	live := &liveConfig{cur: cfg}

	agent := node.New(cfg, client, journal, logEvent)
	agent.MaxJobs = 0
	if *once {
		agent.MaxJobs = 1
	}

	libraryFor := func(job store.Work) (string, error) {
		var root string
		for _, lib := range live.get().Libraries {
			if lib.Name == job.Library {
				root = lib.Root
				break
			}
		}
		if root == "" {
			return "", fmt.Errorf("library %q is not configured on this node", job.Library)
		}
		return root, nil
	}

	// Ingest is separate from Encode so the prefetcher can stage the next file
	// while this one is being encoded.
	agent.Ingest = func(ctx context.Context, job store.Work) error {
		logf := func(stage, msg string) {
			logEvent(transcode.Event{FileID: job.ID, Path: job.Path, Stage: stage, Message: msg})
		}
		_, _, err := transcode.EnsureSource(ctx, live.get(), job.ID, job.Path, logf)
		return err
	}

	agent.Encode = func(ctx context.Context, job store.Work) (func(context.Context) error, error) {
		root, err := libraryFor(job)
		if err != nil {
			return nil, err
		}
		_, commit, err := transcode.Split(ctx, transcode.Options{
			Config:   live.get(),
			Reporter: client,
			Log:      logEvent,
		}, transcode.Task{
			ID:          job.ID,
			Path:        job.Path,
			LibraryRoot: root,
			Decision:    job.Decision,
			TargetKbps:  job.TargetKbps,
		})
		return commit, err
	}

	// Refuse to start a file there is no room for: a full scratch disk turns
	// into a failed encode and a spent attempt otherwise.
	agent.DiskGuard = func() error {
		c := live.get()
		free, err := transcode.FreeGB(c.Paths.Scratch)
		if err != nil {
			return err
		}
		if free < float64(c.Safety.MinFreeGB) {
			return fmt.Errorf("scratch has %.0f GB free, %d GB required",
				free, c.Safety.MinFreeGB)
		}
		return nil
	}

	// One signal means "finish this file and stop". A cancelled context stops
	// the claim loop but deliberately does not cancel the encode in flight.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Reload loop: cheap, and the ticker is the only thing here that is not
	// already on a goroutine.
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				next, err := config.Load(*cfgPath)
				if err != nil {
					continue // an invalid file is the server's business to report
				}
				// The token is a flag or a secret file, never in the config.
				if next.Server.Token == "" {
					next.Server.Token = cfg.Server.Token
				}
				if live.set(next) {
					agent.SetConfig(next)
					logEvent(transcode.Event{Stage: "config", Message: "configuration reloaded"})
				}
			}
		}
	}()

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

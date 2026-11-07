package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// fakeServer answers the node protocol and records what it was told.
type fakeServer struct {
	mu         sync.Mutex
	claims     int
	states     []map[string]any
	events     int
	heartbeats int
	srv        *httptest.Server
}

func newFakeServer(t *testing.T, jobs []store.Work) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/node/register", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("POST /api/v1/node/claim", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(jobs) == 0 {
			w.Header().Set("X-Claim-Status", "empty")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		job := jobs[0]
		jobs = jobs[1:]
		f.claims++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"job": job, "lease_seconds": 300})
	})
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.heartbeats++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/state", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.states = append(f.states, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"state":"done"}`))
	})
	mux.HandleFunc("POST /api/v1/node/event", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.events++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) stateCalls() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, len(f.states))
	copy(out, f.states)
	return out
}

func testConfig() *config.Config {
	return &config.Config{
		Version: 1,
		Paths:   config.Paths{Scratch: "/tmp/scratch", Quarantine: "/tmp/q"},
		Safety:  config.Safety{MaxAttempts: 3},
	}
}

// The loop the whole milestone exists for: claim, do the work, report the
// measurements, ask again, stop when told there is nothing left.
func TestAgentClaimsProcessesAndReports(t *testing.T) {
	jobs := []store.Work{
		{ID: 7, Path: "/media/Film.mkv", Decision: "encode", TargetKbps: 6000},
	}
	f := newFakeServer(t, jobs)
	client := NewClient(f.srv.URL, "tok", "test-node")

	agent := New(testConfig(), client, "", nil)
	agent.MaxJobs = 1
	agent.Process = func(ctx context.Context, job store.Work) error {
		if err := client.SetProgress(ctx, job.ID, 0.5, 8.5); err != nil {
			return err
		}
		return client.Finish(ctx, job.ID, 1_000, 700, 93.4)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := agent.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if f.claims != 1 {
		t.Errorf("claims = %d, want 1", f.claims)
	}
	if f.heartbeats != 1 {
		t.Errorf("heartbeats = %d, want 1", f.heartbeats)
	}
	states := f.stateCalls()
	if len(states) != 1 {
		t.Fatalf("state calls = %d, want 1", len(states))
	}
	got := states[0]
	if got["state"] != "done" {
		t.Errorf("state = %v, want done", got["state"])
	}
	// Numbers must survive JSON as numbers, not strings: the dashboard sums them.
	if got["bytes_after"] != float64(700) || got["vmaf"] != 93.4 {
		t.Errorf("measurements = %v / %v, want 700 / 93.4", got["bytes_after"], got["vmaf"])
	}
}

// A node that stopped mid-file must say so on the next start, once.
func TestJournalRecoveryReportsInterruption(t *testing.T) {
	f := newFakeServer(t, nil)
	client := NewClient(f.srv.URL, "tok", "test-node")

	dir := t.TempDir()
	path := filepath.Join(dir, "node.journal")
	j := journal{path: path}
	if err := j.write(entry{JobID: 42, Path: "/media/Half.mkv", Started: time.Now().Unix(), Node: "test-node"}); err != nil {
		t.Fatal(err)
	}

	agent := New(testConfig(), client, path, nil)
	agent.MaxJobs = 1
	agent.Idle = time.Millisecond
	agent.Process = func(context.Context, store.Work) error { return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
		t.Fatalf("run: %v", err)
	}

	states := f.stateCalls()
	if len(states) == 0 {
		t.Fatal("no state reported for the interrupted job")
	}
	first := states[0]
	if first["state"] != "failed" || first["code"] != "E_NODE_INTERRUPTED" {
		t.Errorf("first state = %v/%v, want failed/E_NODE_INTERRUPTED", first["state"], first["code"])
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("journal was not cleared after the interruption was reported")
	}
}

// The server being down is not a fatal error: the node waits and asks again.
func TestAgentSurvivesUnreachableServer(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "tok", "test-node")
	agent := New(testConfig(), client, "", nil)
	agent.Process = func(context.Context, store.Work) error { return nil }
	agent.Idle = 10 * time.Millisecond
	agent.MaxIdle = 20 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := agent.Run(ctx)
	if err != nil {
		t.Fatalf("run returned %v, want nil after the context ends", err)
	}
}

// A cancelled context must not abandon the file in flight: that is the whole
// point of a graceful stop.
func TestCancelDoesNotInterruptRunningJob(t *testing.T) {
	jobs := []store.Work{{ID: 9, Path: "/media/Long.mkv", Decision: "encode", TargetKbps: 6000}}
	f := newFakeServer(t, jobs)
	client := NewClient(f.srv.URL, "tok", "test-node")

	agent := New(testConfig(), client, "", nil)
	started := make(chan struct{})
	finished := make(chan error, 1)
	agent.Process = func(ctx context.Context, job store.Work) error {
		close(started)
		// The job context must still be alive after the parent is cancelled.
		time.Sleep(120 * time.Millisecond)
		if ctx.Err() != nil {
			finished <- fmt.Errorf("job context was cancelled mid-encode")
			return ctx.Err()
		}
		finished <- nil
		return client.Finish(ctx, job.ID, 1000, 700, 95)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	if err := agent.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job did not finish after cancellation")
	}
	if states := f.stateCalls(); len(states) != 1 || states[0]["state"] != "done" {
		t.Errorf("the finished job was not reported: %v", states)
	}
}

package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
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
	claimGate  chan struct{} // when set, a claim blocks until it is closed
	statesSeen chan struct{} // signal on each state call
}

func newFakeServer(t *testing.T, jobs []store.Work) *fakeServer {
	t.Helper()
	f := &fakeServer{statesSeen: make(chan struct{}, 16)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/node/register", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("POST /api/v1/node/claim", func(w http.ResponseWriter, r *http.Request) {
		if f.claimGate != nil {
			<-f.claimGate
		}
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
		select {
		case f.statesSeen <- struct{}{}:
		default:
		}
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

func (f *fakeServer) claimCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.claims
}

func testConfig() *config.Config {
	return &config.Config{
		Version: 1,
		Paths:   config.Paths{Scratch: "/tmp/scratch", Quarantine: "/tmp/q"},
		Safety:  config.Safety{MaxAttempts: 3},
	}
}

// agentFor builds an agent with the two lanes disabled, so ordering is not a
// race and the assertions are about the protocol.
func agentFor(t *testing.T, f *fakeServer, journalDir string) (*Agent, *Client) {
	t.Helper()
	client := NewClient(f.srv.URL, "tok", "test-node")
	a := New(testConfig(), client, journalDir, nil)
	a.Idle = 10 * time.Millisecond
	a.MaxIdle = 20 * time.Millisecond
	a.Prefetch = false
	a.Egress = false
	a.Ingest = func(context.Context, store.Work) error { return nil }
	a.Encode = func(ctx context.Context, job store.Work) (func(context.Context) error, error) {
		return func(c context.Context) error {
			return client.Finish(c, job.ID, 1000, 700, 93.4)
		}, nil
	}
	return a, client
}

// The loop the milestone exists for: claim, do the work, report the
// measurements, ask again, stop when told there is nothing left.
func TestAgentClaimsProcessesAndReports(t *testing.T) {
	f := newFakeServer(t, []store.Work{
		{ID: 7, Path: "/media/Film.mkv", Decision: "encode", TargetKbps: 6000},
	})
	a, client := agentFor(t, f, "")
	a.MaxJobs = 1
	a.Ingest = func(context.Context, store.Work) error { return nil }
	a.Encode = func(ctx context.Context, job store.Work) (func(context.Context) error, error) {
		if err := client.SetProgress(ctx, job.ID, 0.5, 8.5); err != nil {
			return nil, err
		}
		return func(c context.Context) error {
			return client.Finish(c, job.ID, 1000, 700, 93.4)
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	if f.claimCount() != 1 {
		t.Errorf("claims = %d, want 1", f.claimCount())
	}
	states := f.stateCalls()
	if len(states) != 1 {
		t.Fatalf("state calls = %d, want 1: %v", len(states), states)
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
	dir := t.TempDir()
	j := newJournal(dir)
	if err := j.start(entry{JobID: 42, Path: "/media/Half.mkv", Node: "test-node", Stage: "encoding"}); err != nil {
		t.Fatal(err)
	}

	a, _ := agentFor(t, f, dir)
	a.MaxJobs = 1

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil && ctx.Err() == nil {
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
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("journal still holds %d entries after recovery", len(entries))
	}
}

// The server being down is not a fatal error: the node waits and asks again.
func TestAgentSurvivesUnreachableServer(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "tok", "test-node")
	a := New(testConfig(), client, "", nil)
	a.Idle = 10 * time.Millisecond
	a.MaxIdle = 20 * time.Millisecond
	a.Prefetch = false
	a.Egress = false
	a.Ingest = func(context.Context, store.Work) error { return nil }
	a.Encode = func(context.Context, store.Work) (func(context.Context) error, error) { return nil, nil }

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("run returned %v, want nil after the context ends", err)
	}
}

// A cancelled context must not abandon the file in flight: that is the whole
// point of a graceful stop.
func TestCancelDoesNotInterruptRunningJob(t *testing.T) {
	f := newFakeServer(t, []store.Work{
		{ID: 9, Path: "/media/Long.mkv", Decision: "encode", TargetKbps: 6000},
	})
	a, client := agentFor(t, f, "")

	started := make(chan struct{})
	finished := make(chan error, 1)
	a.Encode = func(ctx context.Context, job store.Work) (func(context.Context) error, error) {
		close(started)
		time.Sleep(120 * time.Millisecond)
		if ctx.Err() != nil {
			finished <- fmt.Errorf("job context was cancelled mid-encode")
			return nil, ctx.Err()
		}
		return func(c context.Context) error {
			finished <- nil
			return client.Finish(c, job.ID, 1000, 700, 95)
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	if err := a.Run(ctx); err != nil {
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

// Placing the file must not block the next encode, and shutdown must wait for
// it rather than exiting mid-swap.
func TestEgressRunsOffTheEncodePathAndDrains(t *testing.T) {
	f := newFakeServer(t, []store.Work{
		{ID: 11, Path: "/media/A.mkv", Decision: "encode", TargetKbps: 6000},
		{ID: 12, Path: "/media/B.mkv", Decision: "encode", TargetKbps: 6000},
	})
	a, client := agentFor(t, f, "")
	a.MaxJobs = 2
	a.Egress = true

	var placed sync.Map
	encodeStarted := make(chan int64, 4)
	a.Encode = func(ctx context.Context, job store.Work) (func(context.Context) error, error) {
		encodeStarted <- job.ID
		return func(c context.Context) error {
			time.Sleep(50 * time.Millisecond) // a stand-in for the copy
			placed.Store(job.ID, true)
			return client.Finish(c, job.ID, 1000, 700, 94)
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Both encodes must have started without waiting for the previous placement.
	close(encodeStarted)
	var ids []int64
	for id := range encodeStarted {
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		t.Fatalf("encoded %d files, want 2", len(ids))
	}
	// And both placements must have completed before Run returned.
	for _, id := range ids {
		if _, ok := placed.Load(id); !ok {
			t.Errorf("file %d was never placed before shutdown", id)
		}
	}
	if got := len(f.stateCalls()); got < 2 {
		t.Errorf("state calls = %d, want at least 2", got)
	}
}

// The prefetcher stages the next file while the current one encodes, and the
// encoder takes the staged job instead of claiming a third time.
func TestPrefetchStagesAhead(t *testing.T) {
	f := newFakeServer(t, []store.Work{
		{ID: 21, Path: "/media/One.mkv", Decision: "encode", TargetKbps: 6000},
		{ID: 22, Path: "/media/Two.mkv", Decision: "encode", TargetKbps: 6000},
	})
	a, client := agentFor(t, f, "")
	a.MaxJobs = 2
	a.Prefetch = true

	var mu sync.Mutex
	ingested := map[int64]bool{}
	a.Ingest = func(_ context.Context, job store.Work) error {
		mu.Lock()
		ingested[job.ID] = true
		mu.Unlock()
		return nil
	}
	a.Encode = func(ctx context.Context, job store.Work) (func(context.Context) error, error) {
		// The first job is claimed by the loop itself and staged by Encode; every
		// later one must already be on disk when the encoder picks it up.
		if job.ID != 21 {
			mu.Lock()
			staged := ingested[job.ID]
			mu.Unlock()
			if !staged {
				t.Errorf("file %d reached the encoder without being ingested", job.ID)
			}
		}
		time.Sleep(80 * time.Millisecond)
		return func(c context.Context) error {
			return client.Finish(c, job.ID, 1000, 700, 94)
		}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Two jobs, and the prefetch must have supplied the second: three claims
	// would mean it was thrown away and fetched again.
	if got := f.claimCount(); got > 2 {
		t.Errorf("claims = %d, want at most 2 (the prefetched job must be reused)", got)
	}
}

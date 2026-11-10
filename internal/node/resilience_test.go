package node

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
	"github.com/jakobbjelver/messer-transcode/internal/store"
)

// outage is a fake orchestrator that can be taken down and brought back, the
// way Komodo recreates the container at 03:01.
type outage struct {
	mu     sync.Mutex
	down   bool
	states []map[string]any
	srv    *httptest.Server
}

func newOutage(t *testing.T) *outage {
	t.Helper()
	o := &outage{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/node/register", func(w http.ResponseWriter, r *http.Request) {
		if o.isDown() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"node_id":"n1"}`))
	})
	mux.HandleFunc("POST /api/v1/node/event", func(w http.ResponseWriter, r *http.Request) {
		if o.isDown() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/node/claim", func(w http.ResponseWriter, r *http.Request) {
		if o.isDown() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/v1/node/jobs/{id}/state", func(w http.ResponseWriter, r *http.Request) {
		if o.isDown() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		o.mu.Lock()
		o.states = append(o.states, body)
		o.mu.Unlock()
		_, _ = w.Write([]byte(`{"state":"done"}`))
	})
	o.srv = httptest.NewServer(mux)
	t.Cleanup(o.srv.Close)
	return o
}

func (o *outage) isDown() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.down
}

func (o *outage) setDown(v bool) {
	o.mu.Lock()
	o.down = v
	o.mu.Unlock()
}

func (o *outage) seen() []map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]map[string]any(nil), o.states...)
}

// TestAReportThatCannotBeSentIsKeptAndReplayed is the container-restart case:
// the swap has happened, the orchestrator is being recreated, and the only
// record of the work is the node's own disk.
func TestAReportThatCannotBeSentIsKeptAndReplayed(t *testing.T) {
	dir := t.TempDir()
	o := newOutage(t)
	o.setDown(true)

	client := NewClient(o.srv.URL, "tok", "mac-mini")
	client.RetryFor = 150 * time.Millisecond // do not wait 90 s in a unit test
	a := New(testConfig(), client, dir, nil)

	out := &media.OutputFacts{
		Probe:   media.Probe{VideoCodec: "hevc", Height: 720, BitrateKbps: 2700, SizeBytes: 400},
		MtimeNS: 555,
	}
	if err := client.Finish(context.Background(), 7, 1000, 400, 95, out); err == nil {
		t.Fatal("finish should fail while the orchestrator is down")
	}

	outcomes := a.journal.outcomes()
	if len(outcomes) != 1 || outcomes[0].JobID != 7 {
		t.Fatalf("journal kept %d outcomes, want one for job 7", len(outcomes))
	}
	if outcomes[0].Probe.SizeBytes != 400 || outcomes[0].MtimeNS != 555 {
		t.Fatalf("outcome lost the output facts: %+v", outcomes[0])
	}

	// The container comes back.
	o.setDown(false)
	a.flushOutcomes(context.Background())

	if got := a.journal.outcomes(); len(got) != 0 {
		t.Fatalf("outcome still on disk after it was accepted: %d", len(got))
	}
	seen := o.seen()
	if len(seen) != 1 || seen[0]["state"] != "done" {
		t.Fatalf("server saw %v, want one done", seen)
	}
	if seen[0]["bytes_after"] != float64(400) {
		t.Fatalf("replayed bytes_after = %v, want 400", seen[0]["bytes_after"])
	}
}

// TestBootReplaysFinishedWorkBeforeDeclaringItInterrupted is the ordering that
// makes the restart harmless: a job whose swap landed must be reported as done,
// not as an attempt that failed.
func TestBootReplaysFinishedWorkBeforeDeclaringItInterrupted(t *testing.T) {
	dir := t.TempDir()
	o := newOutage(t)
	o.setDown(false)

	client := NewClient(o.srv.URL, "tok", "mac-mini")
	client.RetryFor = 150 * time.Millisecond
	a := New(testConfig(), client, dir, nil)
	a.MaxJobs = 1 // one pass: boot, flush, claim (nothing), exit
	a.Prefetch = false
	a.Egress = false
	a.Idle = 10 * time.Millisecond
	a.MaxIdle = 20 * time.Millisecond
	a.Ingest = func(context.Context, store.Work) error { return nil }
	a.Encode = func(context.Context, store.Work) (func(context.Context) error, error) {
		return nil, nil
	}

	// What a crash between the swap and the report leaves behind: a job entry
	// and its outcome.
	if err := a.journal.start(entry{JobID: 9, Path: "/media/Film.mkv", Stage: "replacing", Node: "mac-mini"}); err != nil {
		t.Fatal(err)
	}
	if err := a.journal.recordOutcome(outcome{
		JobID: 9, Before: 1000, After: 400, VMAF: 96,
		Probe: media.Probe{VideoCodec: "hevc", SizeBytes: 400}, MtimeNS: 7,
	}); err != nil {
		t.Fatal(err)
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, s := range o.seen() {
		if s["state"] == "failed" || s["code"] == "E_NODE_INTERRUPTED" {
			t.Fatalf("a finished job was reported as interrupted: %v", s)
		}
	}
	var done bool
	for _, s := range o.seen() {
		if s["state"] == "done" {
			done = true
		}
	}
	if !done {
		t.Fatalf("no done report was replayed: %v", o.seen())
	}
	if _, err := os.Stat(filepath.Join(dir, "outcomes", "9.json")); !os.IsNotExist(err) {
		t.Fatal("the outcome file should be gone once the report landed")
	}
}

// TestTheJournalSurvivesARestart is the file-level contract: an outcome written
// by one process is readable by the next one.
func TestTheJournalSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	j := newJournal(dir)
	if err := j.recordOutcome(outcome{JobID: 3, Before: 10, After: 5}); err != nil {
		t.Fatal(err)
	}
	again := newJournal(dir)
	got := again.outcomes()
	if len(got) != 1 || got[0].JobID != 3 || got[0].After != 5 {
		t.Fatalf("second process read %+v", got)
	}
	again.clearOutcome(3)
	if len(newJournal(dir).outcomes()) != 0 {
		t.Fatal("cleared outcome came back")
	}
	_ = store.Work{}
}

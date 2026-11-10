package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// entry is what the node was doing when it stopped.
type entry struct {
	JobID   int64  `json:"job_id"`
	Path    string `json:"path"`
	Stage   string `json:"stage"`
	Started int64  `json:"started"`
	Node    string `json:"node"`
}

// journal is a directory with one file per job in flight.
//
// One line stopped being enough the moment egress moved off the encode path:
// there can be a file encoding and a file being placed at the same time, and a
// crash has to account for both. A directory gives that for free, and the file
// name is the job id so a half-written entry cannot shadow another job.
type journal struct {
	dir string
	mu  sync.Mutex
}

func newJournal(dir string) *journal { return &journal{dir: dir} }

func (j *journal) start(e entry) error {
	if j == nil || j.dir == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return err
	}
	e.Started = time.Now().Unix()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// Write then rename: a truncated journal entry is worse than none.
	path := j.path(e.JobID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (j *journal) finish(id int64) {
	if j == nil || j.dir == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = os.Remove(j.path(id))
}

// all returns every job that was in flight when the process stopped.
func (j *journal) all() []entry {
	if j == nil || j.dir == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	items, err := os.ReadDir(j.dir)
	if err != nil {
		return nil
	}
	var out []entry
	for _, it := range items {
		name := it.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(j.dir, name))
		if err != nil {
			continue
		}
		var e entry
		if json.Unmarshal(b, &e) != nil || e.JobID == 0 {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (j *journal) path(id int64) string {
	return filepath.Join(j.dir, fmt.Sprintf("%d.json", id))
}

// outcome is a finished job whose result the orchestrator has not acknowledged.
//
// The swap happens on this machine and cannot be undone by a restart; the
// report travels over HTTP and can be lost. Keeping the report on disk until it
// is acknowledged is what makes a container restart a pause instead of an hour
// of re-encoding a file that is already replaced.
type outcome struct {
	JobID   int64       `json:"job_id"`
	Path    string      `json:"path,omitempty"`
	Before  int64       `json:"bytes_before"`
	After   int64       `json:"bytes_after"`
	VMAF    float64     `json:"vmaf"`
	Probe   media.Probe `json:"probe"`
	MtimeNS int64       `json:"mtime_ns"`
	At      int64       `json:"at"`
}

func (j *journal) outcomePath(id int64) string {
	return filepath.Join(j.dir, "outcomes", fmt.Sprintf("%d.json", id))
}

// recordOutcome writes the report to disk before it is sent, so a crash or a
// dead orchestrator between the swap and the acknowledgement cannot lose it.
func (j *journal) recordOutcome(o outcome) error {
	if j == nil || j.dir == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	dir := filepath.Join(j.dir, "outcomes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	o.At = time.Now().Unix()
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	path := j.outcomePath(o.JobID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// outcomes returns every report that has not been acknowledged yet, oldest
// first so a long outage is replayed in the order the work happened.
func (j *journal) outcomes() []outcome {
	if j == nil || j.dir == "" {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()

	items, err := os.ReadDir(filepath.Join(j.dir, "outcomes"))
	if err != nil {
		return nil
	}
	var out []outcome
	for _, it := range items {
		name := it.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(j.dir, "outcomes", name))
		if err != nil {
			continue
		}
		var o outcome
		if json.Unmarshal(b, &o) != nil || o.JobID == 0 {
			continue
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].At < out[k].At })
	return out
}

func (j *journal) clearOutcome(id int64) {
	if j == nil || j.dir == "" {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = os.Remove(j.outcomePath(id))
}

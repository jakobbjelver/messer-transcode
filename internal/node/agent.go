// Package node is the worker that runs on the machine with the media engine.
//
// It owns nothing: the orchestrator holds the queue, the node asks for one file
// at a time, reports progress, and reports the outcome. If the server is down
// the node sleeps and tries again — it never guesses what the queue wants.
package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/store"
	"github.com/jakobbjelver/messer-transcode/internal/transcode"
)

// ErrNoWork is not a failure: it means the queue is empty, the window is shut,
// or the drain has begun. The node backs off and asks again.
var ErrNoWork = errors.New("no work")

// Client is the node's whole view of the orchestrator.
type Client struct {
	base  string
	token string
	node  string
	http  *http.Client
}

// NewClient builds a client. The timeout is generous because a claim can wait
// on a busy database, but it exists: a hung node must not hang forever.
func NewClient(base, token, node string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		node:  node,
		http:  &http.Client{Timeout: 30 * time.Second},
	}
}

// APIError is a failure the orchestrator named. The status decides whether the
// node should retry (the server is unwell) or stop (the node is misconfigured).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) (int, http.Header, error) {
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		buf = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Node-Token", c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, resp.Header, err
	}
	if resp.StatusCode >= 400 {
		var apiErr struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(payload, &apiErr) == nil && apiErr.Error.Code != "" {
			return resp.StatusCode, resp.Header, &APIError{
				Status: resp.StatusCode, Code: apiErr.Error.Code, Message: apiErr.Error.Message,
			}
		}
		return resp.StatusCode, resp.Header, &APIError{
			Status: resp.StatusCode, Message: strings.TrimSpace(string(payload)),
		}
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return resp.StatusCode, resp.Header, err
		}
	}
	return resp.StatusCode, resp.Header, nil
}

// Register is a handshake, not an enrolment: it exists so a misconfigured URL
// or token fails at startup instead of at the first claim.
func (c *Client) Register(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	_, _, err := c.do(ctx, http.MethodPost, "/api/v1/node/register",
		map[string]string{"node": c.node}, &out)
	return out, err
}

// Claim asks for one job. A 204 is ErrNoWork, with the server's reason in the
// X-Claim-Status header for the log.
func (c *Client) Claim(ctx context.Context) (store.Work, string, error) {
	var out struct {
		Job store.Work `json:"job"`
	}
	status, header, err := c.do(ctx, http.MethodPost, "/api/v1/node/claim",
		map[string]string{"node": c.node}, &out)
	if err != nil {
		return store.Work{}, "", err
	}
	if status == http.StatusNoContent {
		return store.Work{}, header.Get("X-Claim-Status"), ErrNoWork
	}
	return out.Job, "claimed", nil
}

// The six Reporter methods below are the transcode package's contract, spoken
// over HTTP instead of SQLite. Ordering matters: progress must never overtake
// a state change, so every call is synchronous.

func (c *Client) SetState(ctx context.Context, id int64, state string) error {
	return c.state(ctx, id, map[string]any{"state": state})
}

func (c *Client) SetProgress(ctx context.Context, id int64, fraction, speed float64) error {
	_, _, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/node/jobs/%d/heartbeat", id),
		map[string]float64{"fraction": fraction, "speed": speed}, nil)
	return err
}

func (c *Client) Skip(ctx context.Context, id int64, code, reason string) error {
	return c.state(ctx, id, map[string]any{"state": "skipped", "code": code, "detail": reason})
}

func (c *Client) Finish(ctx context.Context, id int64, before, after int64, vmaf float64) error {
	return c.state(ctx, id, map[string]any{
		"state": "done", "bytes_before": before, "bytes_after": after, "vmaf": vmaf,
	})
}

// Fail reports a failed attempt. The store decides whether the file is
// retried or parked, so the answer comes back rather than being assumed.
func (c *Client) Fail(ctx context.Context, id int64, code, detail string, _ int) (string, error) {
	var out struct {
		State string `json:"state"`
	}
	_, _, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/v1/node/jobs/%d/state", id),
		map[string]any{"state": "failed", "code": code, "detail": detail}, &out)
	return out.State, err
}

func (c *Client) Event(ctx context.Context, level, code string, fileID int64, message, data string) error {
	_, _, err := c.do(ctx, http.MethodPost, "/api/v1/node/event", map[string]any{
		"file_id": fileID, "level": level, "code": code, "message": message, "data": data,
	}, nil)
	return err
}

func (c *Client) state(ctx context.Context, id int64, body map[string]any) error {
	_, _, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/api/v1/node/jobs/%d/state", id), body, nil)
	return err
}

// ── journal ───────────────────────────────────────────────────────────────

// entry is what the node was doing when it stopped.
type entry struct {
	JobID   int64  `json:"job_id"`
	Path    string `json:"path"`
	Started int64  `json:"started"`
	Node    string `json:"node"`
}

// journal is a one-line file. It exists so a node that is killed mid-encode
// tells the orchestrator what happened on the next start instead of leaving a
// row stuck in "encoding" until the lease expires.
type journal struct {
	path string
}

func (j journal) write(e entry) error {
	if err := os.MkdirAll(filepath.Dir(j.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// Write then rename: a half-written journal is worse than none.
	tmp := j.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

func (j journal) read() (entry, bool) {
	b, err := os.ReadFile(j.path)
	if err != nil {
		return entry{}, false
	}
	var e entry
	if json.Unmarshal(b, &e) != nil || e.JobID == 0 {
		return entry{}, false
	}
	return e, true
}

func (j journal) clear() {
	_ = os.Remove(j.path)
}

// ── agent ─────────────────────────────────────────────────────────────────

// Agent runs one job at a time, forever, until its context is cancelled.
type Agent struct {
	cfg     *config.Config
	client  *Client
	journal journal
	log     func(transcode.Event)

	// Process is the work itself, injected so the loop can be tested without
	// an encoder.
	Process func(ctx context.Context, job store.Work) error

	// Idle is how long to wait when there is nothing to do. It backs off to
	// MaxIdle while the queue stays empty and resets the moment a job arrives.
	Idle    time.Duration
	MaxIdle time.Duration

	// MaxJobs stops the agent after this many jobs. Zero means run forever;
	// one is what `--once` uses and what makes the loop testable.
	MaxJobs int
}

// New build an agent. journalPath may be empty to disable crash recovery.
func New(cfg *config.Config, client *Client, journalPath string, log func(transcode.Event)) *Agent {
	if log == nil {
		log = func(transcode.Event) {}
	}
	return &Agent{
		cfg:     cfg,
		client:  client,
		journal: journal{path: journalPath},
		log:     log,
		Idle:    5 * time.Second,
		MaxIdle: 2 * time.Minute,
	}
}

// Run is the whole worker: recover, then claim and process until cancelled.
//
// A cancelled context does not cancel the job in flight: the point of a
// graceful stop is that the encode finishes. A second signal is the operator
// insisting, and that is handled by the caller.
func (a *Agent) Run(ctx context.Context) error {
	// A node with no work function would claim a file and drop it, which is
	// worse than not starting: the file sits in "encoding" until the lease
	// expires and the operator learns nothing.
	if a.Process == nil {
		return errors.New("node: Process is not set — refusing to claim work that would be dropped")
	}

	// Registration is a handshake. A server that is down is worth waiting for;
	// a token the server rejects never becomes correct by itself, so that stops
	// the node instead of letting it spin against the wall all night.
	wait := a.Idle
	for {
		_, err := a.client.Register(ctx)
		if err == nil {
			break
		}
		var api *APIError
		if errors.As(err, &api) && api.Status < 500 {
			return fmt.Errorf("register: %w", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		a.log(transcode.Event{Stage: "error", Code: "E_NODE_SERVER",
			Message: "cannot reach the orchestrator, retrying: " + err.Error()})
		if !sleep(ctx, wait) {
			return nil
		}
		wait = nextBackoff(wait, a.MaxIdle)
	}
	a.recoverInterrupted(ctx)

	wait = a.Idle
	refusals := 0
	done := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		if a.MaxJobs > 0 && done >= a.MaxJobs {
			return nil
		}

		job, reason, err := a.client.Claim(ctx)
		switch {
		case errors.Is(err, ErrNoWork):
			refusals++
			if refusals == 1 || refusals%20 == 0 {
				a.log(transcode.Event{Stage: "idle", Message: "no work: " + reason})
			}
			if !sleep(ctx, wait) {
				return nil
			}
			wait = nextBackoff(wait, a.MaxIdle)
			continue
		case err != nil:
			// The server is down or unreachable. That is not a reason to die:
			// it comes back, and so do we.
			a.log(transcode.Event{Stage: "error", Code: "E_NODE_SERVER", Message: err.Error()})
			if !sleep(ctx, wait) {
				return nil
			}
			wait = nextBackoff(wait, a.MaxIdle)
			continue
		}

		wait = a.Idle
		refusals = 0
		a.runOne(ctx, job)
		done++
	}
}

// runOne processes a claimed job and reports the outcome. The job context is
// detached from cancellation so a stop signal lets the encode finish.
func (a *Agent) runOne(ctx context.Context, job store.Work) {
	jobCtx := context.WithoutCancel(ctx)

	_ = a.journal.write(entry{JobID: job.ID, Path: job.Path,
		Started: time.Now().Unix(), Node: a.client.node})

	a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "claim",
		Message: fmt.Sprintf("%s at %d kbps", job.Decision, job.TargetKbps)})

	if a.Process != nil {
		if err := a.Process(jobCtx, job); err != nil {
			// Process already reported the failure; the journal has done its
			// job too, so it must not re-report on the next start.
			a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "failed", Message: err.Error()})
		}
	}
	a.journal.clear()
}

// recoverInterrupted reports a job that was in flight when the node died.
//
// It is deliberately a failure rather than a silent requeue: the scratch output
// is gone, the file may have been half-replaced, and the attempt count should
// reflect that something went wrong. Three strikes and it parks.
func (a *Agent) recoverInterrupted(ctx context.Context) {
	e, ok := a.journal.read()
	if !ok {
		return
	}
	detail := fmt.Sprintf("node %s stopped while processing this file at %s",
		e.Node, time.Unix(e.Started, 0).Format(time.RFC3339))
	if state, err := a.client.Fail(ctx, e.JobID, "E_NODE_INTERRUPTED", detail, 0); err != nil {
		a.log(transcode.Event{FileID: e.JobID, Stage: "error", Code: "E_NODE_INTERRUPTED",
			Message: "could not report interrupted job, will retry: " + err.Error()})
		return // keep the journal; the next start tries again
	} else {
		a.log(transcode.Event{FileID: e.JobID, Path: e.Path, Stage: "recovered",
			Code: "E_NODE_INTERRUPTED", Message: "interrupted job requeued as " + state})
	}
	a.journal.clear()
}

// sleep waits, and reports whether the context is still alive.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	next := cur * 2
	if next > max {
		return max
	}
	return next
}

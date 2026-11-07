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
	"sync"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
	"github.com/jakobbjelver/messer-transcode/internal/quarantine"
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
	// A 200 with no job is not a job. Returning it as one would send an
	// identity-less task into the pipeline, where it could not be reported.
	if out.Job.ID == 0 {
		return store.Work{}, "empty", ErrNoWork
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

// ── agent ─────────────────────────────────────────────────────────────────

// pending is a job the prefetcher staged and is holding for the encoder.
type pending struct {
	job   store.Work
	taken chan struct{}
}

// Agent runs one job at a time, forever, until its context is cancelled.
//
// Ingest and encode are separate functions because they are separate lanes: the
// prefetcher may be copying the next file's source while this one encodes, and
// the egress worker may be placing the previous result at the same time.
type Agent struct {
	cfg     *config.Config
	client  *Client
	journal *journal
	log     func(transcode.Event)

	// Ingest stages a job's source into scratch. Idempotent: a copy the
	// prefetcher already made is verified and reused.
	Ingest func(ctx context.Context, job store.Work) error

	// Encode runs the encode and its verification, and returns the function
	// that places the result. A nil commit means there is nothing to place.
	Encode func(ctx context.Context, job store.Work) (func(context.Context) error, error)

	// DiskGuard, when set, is checked before every claim. A node that starts a
	// file it has no room for turns a full disk into a failed encode.
	DiskGuard func() error

	// Idle is how long to wait when there is nothing to do. It backs off to
	// MaxIdle while the queue stays empty and resets the moment a job arrives.
	Idle    time.Duration
	MaxIdle time.Duration

	// MaxJobs stops the agent after this many jobs. Zero means run forever.
	MaxJobs int

	// Prefetch stages the next file while this one encodes; Egress places the
	// finished one while the next one encodes. Both are on by default.
	Prefetch bool
	Egress   bool

	// idleReported is when this node last told the orchestrator it was alive
	// but had nothing to do.
	idleReported time.Time

	// sweepMarker is the file whose mtime says when the quarantine was last
	// expired, so a restart does not sweep again and a node that has been up
	// for a week still sweeps daily.
	sweepMarker string

	lastEncodeEnd time.Time
	ready         chan *pending
	prefetchOn    bool
	egressLane    *egress
	stats         runStats
}

// runStats measures the thing the operator actually cares about: how much of
// the wall clock the media engine was doing work.
type runStats struct {
	mu        sync.Mutex
	started   time.Time
	encoded   time.Duration
	egressing time.Duration
	jobs      int
	lastEnd   time.Time
	ended     time.Time
}

func (s *runStats) beginJob() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started.IsZero() {
		s.started = time.Now()
	}
	return s.lastEnd
}

// finishJob records one job. wait is the gap between the previous encode
// finishing and this one starting: that is the idle time, and the whole point
// of the lanes is to drive it to zero.
func (s *runStats) finishJob(encode, egress, wait time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.encoded += encode
	s.egressing += egress
	s.jobs++
	s.lastEnd = time.Now()
	s.ended = s.lastEnd
}

func (s *runStats) summary() (jobs int, wall, encoded time.Duration, idlePct float64, ingress time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started.IsZero() {
		return 0, 0, 0, 0, 0
	}
	// Stop the clock at the last placement, not at shutdown: the time spent
	// waiting for the queue to produce another file is not idle time the lanes
	// could have recovered, and counting it made a busy night look lazy.
	if s.ended.IsZero() {
		return 0, 0, 0, 0, 0
	}
	wall = s.ended.Sub(s.started)
	if wall > 0 {
		idlePct = 100 * (1 - float64(s.encoded)/float64(wall))
	}
	return s.jobs, wall, s.encoded, idlePct, s.egressing
}

// New builds an agent. journalPath may be empty to disable crash recovery.
func New(cfg *config.Config, client *Client, journalPath string, log func(transcode.Event)) *Agent {
	if log == nil {
		log = func(transcode.Event) {}
	}
	return &Agent{
		cfg:         cfg,
		client:      client,
		journal:     newJournal(journalPath),
		log:         log,
		Idle:        5 * time.Second,
		MaxIdle:     2 * time.Minute,
		Prefetch:    true,
		Egress:      true,
		sweepMarker: filepath.Join(filepath.Dir(journalPath), "quarantine.sweep"),
		ready:       make(chan *pending, 1),
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
	if a.Ingest == nil || a.Encode == nil {
		return errors.New("node: Ingest and Encode must be set — refusing to claim work that would be dropped")
	}

	if err := a.handshake(ctx); err != nil {
		return err
	}
	// Say hello as an event, not just as a log line. The orchestrator cannot
	// see this process, so without this a healthy idle node looks exactly like
	// a dead one — which is the failure this whole health check exists to
	// distinguish.
	_ = a.client.Event(ctx, "info", "NODE_UP", 0, "node started", "")
	a.idleReported = time.Now()
	a.sweepQuarantine(ctx)
	a.recoverInterrupted(ctx)

	if a.Egress {
		a.egressLane = newEgress(a.log)
	}

	wait := a.Idle
	refusals := 0
	done := 0
	for ctx.Err() == nil && (a.MaxJobs == 0 || done < a.MaxJobs) {
		if a.DiskGuard != nil {
			if err := a.DiskGuard(); err != nil {
				a.log(transcode.Event{Stage: "idle", Code: "E_DISK_LOW", Message: err.Error()})
				if !sleep(ctx, a.MaxIdle) {
					break
				}
				continue
			}
		}

		job, reason, err := a.takeNext(ctx)
		switch {
		case errors.Is(err, ErrNoWork):
			refusals++
			if refusals == 1 || refusals%20 == 0 {
				a.log(transcode.Event{Stage: "idle", Message: "no work: " + reason})
			}
			// Expiring quarantined originals is the node's job: it is the half
			// that can see the media volume, and the orchestrator must not.
			a.sweepQuarantine(ctx)

			// Idle is a state worth reporting, at a rate that will not fill
			// the events table: it is the difference between "the window is
			// shut" and "the node died at midnight".
			if time.Since(a.idleReported) > 5*time.Minute {
				_ = a.client.Event(ctx, "info", "NODE_IDLE", 0, reason, "")
				a.idleReported = time.Now()
			}
			// A one-shot node has nothing to wait for. Sleeping here would
			// keep a cron job alive for hours outside the window instead of
			// letting it exit with "nothing to do".
			if a.MaxJobs > 0 {
				return nil
			}
			if !sleep(ctx, wait) {
				break
			}
			wait = nextBackoff(wait, a.MaxIdle)
			continue
		case err != nil:
			if ctx.Err() != nil {
				break
			}
			// The server is down or unreachable. That is not a reason to die:
			// it comes back, and so do we.
			a.log(transcode.Event{Stage: "error", Code: "E_NODE_SERVER", Message: err.Error()})
			if !sleep(ctx, wait) {
				break
			}
			wait = nextBackoff(wait, a.MaxIdle)
			continue
		}

		wait = a.Idle
		refusals = 0
		a.beginPrefetch(ctx, a.MaxJobs == 0 || done+1 < a.MaxJobs)
		a.runOne(ctx, job)
		done++
	}

	// Shutdown: let a prefetched file go rather than hold it hostage, then wait
	// for anything already being placed.
	if a.prefetchOn {
		select {
		case p := <-a.ready:
			if p != nil {
				close(p.taken)
				a.release(p.job)
			}
		case <-time.After(2 * time.Second):
			// A prefetch still in flight will see the cancelled context and
			// release its own job.
		}
	}
	if a.egressLane != nil {
		if !a.egressLane.drain(10 * time.Minute) {
			a.log(transcode.Event{Stage: "warning", Message: "egress did not finish before shutdown"})
		}
	}
	a.reportSummary()
	return nil
}

// handshake registers, waiting out a server that is down but stopping for a
// token the server rejects. A bad token never becomes correct by itself.
func (a *Agent) handshake(ctx context.Context) error {
	wait := a.Idle
	for {
		_, err := a.client.Register(ctx)
		if err == nil {
			return nil
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
}

// takeNext returns the prefetched job when there is one, otherwise claims.
func (a *Agent) takeNext(ctx context.Context) (store.Work, string, error) {
	if a.prefetchOn {
		select {
		case p := <-a.ready:
			a.prefetchOn = false
			if p != nil && p.job.ID != 0 {
				close(p.taken)
				return p.job, "prefetched", nil
			}
		case <-ctx.Done():
			return store.Work{}, "", ctx.Err()
		}
	}
	return a.client.Claim(ctx)
}

// beginPrefetch claims the next file and stages it while this one encodes.
//
// The claim is the risky part: a prefetched file is in "encoding" on the
// server, so the goroutine keeps heartbeating it until the encoder takes it or
// shutdown gives it back. Without that, a long encode would let the lease
// expire and a second node would start the same file.
func (a *Agent) beginPrefetch(ctx context.Context, more bool) {
	// Staging ahead is only worth a claim when another job will follow; a
	// one-file run would claim a file just to hand it back.
	if !more || !a.Prefetch || a.prefetchOn || ctx.Err() != nil {
		return
	}
	a.prefetchOn = true
	go func() {
		job, _, err := a.client.Claim(ctx)
		if err != nil || errors.Is(err, ErrNoWork) {
			a.ready <- nil
			return
		}
		if err := a.Ingest(ctx, job); err != nil {
			a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "prefetch",
				Message: "staging failed, returning to the queue: " + err.Error()})
			a.release(job)
			a.ready <- nil
			return
		}

		p := &pending{job: job, taken: make(chan struct{})}
		select {
		case a.ready <- p:
		case <-ctx.Done():
			a.release(job)
			a.ready <- nil
			return
		}
		a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "prefetch",
			Message: "staged and waiting for the encoder"})

		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-p.taken:
				return
			case <-ctx.Done():
				// The loop releases on shutdown; do nothing here so we do not
				// race it.
				return
			case <-tick.C:
				_ = a.client.SetProgress(context.WithoutCancel(ctx), job.ID, 0, 0)
			}
		}
	}()
}

// release hands a claimed file back to the queue without spending an attempt.
func (a *Agent) release(job store.Work) {
	if job.ID == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.client.SetState(ctx, job.ID, "planned"); err != nil {
		a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "error",
			Message: "could not return the file to the queue: " + err.Error()})
		return
	}
	a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "release",
		Message: "returned to the queue"})
}

// runOne processes a claimed job and hands the outcome to the right lane.
func (a *Agent) runOne(ctx context.Context, job store.Work) {
	// A job with no identity cannot be reported, so doing the work would be
	// invisible and the file would stay claimed. Refuse and say so.
	if job.ID == 0 {
		a.log(transcode.Event{Stage: "error", Code: "E_NODE_EMPTY_JOB",
			Message: "claimed a job with no id; ignoring it"})
		return
	}

	// Detached from cancellation: a stop signal lets the encode finish.
	jobCtx := context.WithoutCancel(ctx)

	if err := a.journal.start(entry{JobID: job.ID, Path: job.Path, Stage: "encoding", Node: a.client.node}); err != nil {
		a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "warning",
			Message: "journal: " + err.Error()})
	}

	a.stats.beginJob()
	previousEnd := a.lastEncodeEnd
	a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "claim",
		Message: fmt.Sprintf("%s at %d kbps", job.Decision, job.TargetKbps)})

	encodeStarted := time.Now()
	wait := time.Duration(0)
	if !previousEnd.IsZero() {
		wait = encodeStarted.Sub(previousEnd)
	}

	commit, err := a.Encode(jobCtx, job)
	encodeTook := time.Since(encodeStarted)
	a.lastEncodeEnd = time.Now()
	if err != nil {
		// Encode reported the failure itself; the journal must not re-report it.
		a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "failed", Message: err.Error()})
		a.journal.finish(job.ID)
		return
	}
	if commit == nil {
		a.stats.finishJob(encodeTook, 0, wait)
		a.reportTiming(jobCtx, job, encodeTook, 0, wait)
		a.journal.finish(job.ID)
		return
	}

	if a.egressLane == nil {
		placeStarted := time.Now()
		if err := commit(jobCtx); err != nil {
			a.log(transcode.Event{FileID: job.ID, Path: job.Path, Stage: "failed", Message: err.Error()})
		}
		placeTook := time.Since(placeStarted)
		a.stats.finishJob(encodeTook, placeTook, wait)
		a.reportTiming(jobCtx, job, encodeTook, placeTook, wait)
		a.journal.finish(job.ID)
		return
	}

	// Placing happens in the background so the next file can start encoding
	// immediately. The journal entry survives until the commit is done, so a
	// crash during the swap is still accounted for on the next start.
	a.egressLane.submit(egressItem{
		id:   job.ID,
		path: job.Path,
		run:  commit,
		done: func(placeTook time.Duration) {
			a.stats.finishJob(encodeTook, placeTook, wait)
			a.reportTiming(context.WithoutCancel(jobCtx), job, encodeTook, placeTook, wait)
			a.journal.finish(job.ID)
		},
	})
}

// recoverInterrupted reports every job that was in flight when the node died.
//
// It is deliberately a failure rather than a silent requeue: the scratch output
// is gone, the file may have been half-placed, and the attempt count should
// reflect that something went wrong. Three strikes and it parks.
func (a *Agent) recoverInterrupted(ctx context.Context) {
	for _, e := range a.journal.all() {
		detail := fmt.Sprintf("node %s stopped while processing this file (%s, stage %s)",
			e.Node, time.Unix(e.Started, 0).Format(time.RFC3339), e.Stage)
		state, err := a.client.Fail(ctx, e.JobID, "E_NODE_INTERRUPTED", detail, 0)
		if err != nil {
			a.log(transcode.Event{FileID: e.JobID, Stage: "error", Code: "E_NODE_INTERRUPTED",
				Message: "could not report interrupted job, will retry next start: " + err.Error()})
			continue // keep the entry
		}
		a.log(transcode.Event{FileID: e.JobID, Path: e.Path, Stage: "recovered",
			Code: "E_NODE_INTERRUPTED", Message: "interrupted job requeued as " + state})
		a.journal.finish(e.JobID)
	}
}

// reportTiming sends one job's stage timings to the orchestrator, which is
// where the timeline comes from. wait is the gap before this job started: the
// idle time the lanes exist to remove.
func (a *Agent) reportTiming(ctx context.Context, job store.Work, encode, egress, wait time.Duration) {
	data, err := json.Marshal(map[string]float64{
		"encode_s": encode.Seconds(),
		"egress_s": egress.Seconds(),
		"wait_s":   wait.Seconds(),
	})
	if err != nil {
		return
	}
	_ = a.client.Event(ctx, "info", "METRICS", job.ID, "timings", string(data))
}

// reportSummary logs the number that decides whether the lanes did their job.
// sweepQuarantine expires originals past safety.keep_quarantine_days, at most
// once a day.
//
// Without this the quarantine grows by the size of every file ever replaced and
// never shrinks, which on a library this size fills the volume in a few nights.
// It runs here, not in the orchestrator, because the container never mounts the
// media volume.
func (a *Agent) sweepQuarantine(ctx context.Context) {
	if a.sweepMarker == "" || a.cfg.Paths.Quarantine == "" {
		return
	}
	if st, err := os.Stat(a.sweepMarker); err == nil && time.Since(st.ModTime()) < 24*time.Hour {
		return
	}

	res, err := quarantine.Sweep(ctx, a.cfg.Paths.Quarantine, a.cfg.Safety.KeepQuarantineDays, time.Now(), false)
	if err != nil {
		a.log(transcode.Event{Stage: "error", Code: "E_SWEEP", Message: err.Error()})
		return
	}
	// Touch the marker even on a no-op, or a quiet week would retry every tick.
	if f, err := os.Create(a.sweepMarker); err == nil {
		_ = f.Close()
	}
	if res.Removed == 0 {
		return
	}
	a.log(transcode.Event{Stage: "sweep", Message: res.Summary()})
	_ = a.client.Event(ctx, "info", "QUARANTINE_SWEEP", 0, res.Summary(), "")
}

func (a *Agent) reportSummary() {
	jobs, wall, encoded, idle, egress := a.stats.summary()
	if jobs == 0 {
		return
	}
	a.log(transcode.Event{Stage: "summary",
		Message: fmt.Sprintf("%d jobs in %s · encoding %s (%.1f%% idle) · placing %s",
			jobs, wall.Round(time.Second), encoded.Round(time.Second), idle,
			egress.Round(time.Second))})
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

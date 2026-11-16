package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// Work is a file the runner may pick up. The JSON tags are the wire contract
// the node claims against, so they are snake_case like everything else.
type Work struct {
	ID         int64   `json:"id"`
	Path       string  `json:"path"`
	Library    string  `json:"library"`
	Decision   string  `json:"decision"`
	TargetKbps int     `json:"target_kbps"`
	SizeBytes  int64   `json:"size_bytes"`
	DurationS  float64 `json:"duration_seconds"`
}

// ClaimWork returns up to limit planned files that need real work and moves
// them to "encoding" in the same transaction, so two runners cannot collide.
//
// runID is stamped on every claimed row: it is what a run's figures are built
// from, and stamping it at claim time (not at finish) means a job claimed at
// 05:59 stays with the run that started it even when it finishes after the
// window closed. 0 means "no ledger" (tests, a caller with no schedule).
func (s *Store) ClaimWork(ctx context.Context, limit int, runID int64) ([]Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT id, path, library, decision, COALESCE(target_kbps,0), size_bytes, COALESCE(duration_s,0)
FROM files
WHERE state = 'planned' AND decision IN ('encode','remux')
-- The operator's order beats the planner's: +100 is "next", -100 is "last".
-- Within a priority, id keeps it first-in-first-out and explainable.
ORDER BY priority DESC, id
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	var out []Work
	for rows.Next() {
		var w Work
		if err := rows.Scan(&w.ID, &w.Path, &w.Library, &w.Decision, &w.TargetKbps, &w.SizeBytes, &w.DurationS); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, w)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	for _, w := range out {
		var run any
		if runID > 0 {
			run = runID
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE files SET state='encoding', started_at=?, updated_at=?, lease_at=?, run_id=? WHERE id=?`,
			now, now, now, run, w.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// SetState moves one file to a new lifecycle state.
func (s *Store) SetState(ctx context.Context, id int64, state string) error {
	now := time.Now().Unix()
	// A state change is a heartbeat too: the node moves a job to verifying or
	// replacing and then spends a while copying, and reaping it in that window
	// would hand out work that is already placed.
	return s.exec(ctx,
		`UPDATE files SET state=?, updated_at=?, lease_at=? WHERE id=?`, state, now, now, id)
}

// Finish records a successful replacement.
func (s *Store) Finish(ctx context.Context, id, before, after int64, vmaf float64, out *media.OutputFacts) error {
	var v any
	if vmaf > 0 {
		v = vmaf
	}
	now := time.Now().Unix()

	// Without the output's own facts the row keeps describing the file that was
	// replaced, and the next scan reads the change as new work. Every finish
	// from the pipeline carries them; the fallback exists for tests and for a
	// node too old to send them.
	if out == nil || out.Probe.SizeBytes == 0 {
		return s.exec(ctx, `
UPDATE files SET state='done', bytes_before=?, bytes_after=?, vmaf=?, finished_at=?,
  last_error=NULL, last_error_code=NULL, updated_at=?
WHERE id=?`, before, after, v, now, now, id)
	}

	audio, err := json.Marshal(out.Probe.Audio)
	if err != nil {
		return err
	}
	subs, err := json.Marshal(out.Probe.Subs)
	if err != nil {
		return err
	}
	p := out.Probe
	return s.exec(ctx, `
UPDATE files SET state='done', bytes_before=?, bytes_after=?, vmaf=?, finished_at=?,
  size_bytes=?, mtime_ns=?, probe_key=?,
  container=?, video_codec=?, width=?, height=?, bitrate_kbps=?, duration_s=?, pix_fmt=?,
  audio_tracks=?, sub_tracks=?,
  last_error=NULL, last_error_code=NULL, updated_at=?
WHERE id=?`,
		before, after, v, now,
		p.SizeBytes, out.MtimeNS, ProbeKey(p.SizeBytes, out.MtimeNS),
		p.Container, p.VideoCodec, p.Width, p.Height, p.BitrateKbps, p.DurationS, p.PixFmt,
		string(audio), string(subs),
		now, id)
}

// parksImmediately reports whether a failure is structural rather than
// transient. These are the two checks that measure the output against the
// source: a bad size ratio or a wrong duration is a property of the encode, not
// of the moment it ran.
func parksImmediately(code string) bool {
	switch code {
	case "E_VERIFY_SIZE_RATIO", "E_VERIFY_DURATION":
		return true
	}
	return false
}

// Fail records an attempt. A file returns to "planned" until it runs out of
// attempts, which keeps a transient error from parking it forever.
func (s *Store) Fail(ctx context.Context, id int64, code, message string, maxAttempts int) (state string, err error) {
	var attempts int
	var current string
	if err := s.db.QueryRowContext(ctx, `SELECT attempts, state FROM files WHERE id=?`, id).
		Scan(&attempts, &current); err != nil {
		return "", err
	}
	// A failure that arrives after the file was finished is a stale report —
	// the node retried an old attempt, or the switch happened twice. Recording
	// it would re-queue a file that is already replaced.
	if current == "done" {
		return "done", nil
	}
	attempts++
	state = "planned"
	if maxAttempts > 0 && attempts >= maxAttempts {
		state = "failed"
	}
	// A structural verification failure cannot improve on a retry: the same
	// source, the same encoder and the same rung produce the same file, so the
	// second and third attempts are the first one again. Measured 2026-11-15:
	// four files parked after three or four full encodes each (ratio 1.008,
	// 1.026, 1.042, 1.280 against the 0.98 ceiling), and one 0.324 against the
	// floor. Parking on the first attempt gives that window time back.
	if parksImmediately(code) {
		state = "failed"
	}
	err = s.exec(ctx, `
UPDATE files SET attempts=?, state=?, last_error=?, last_error_code=?, updated_at=?
WHERE id=? AND state <> 'done'`, attempts, state, message, code, time.Now().Unix(), id)
	return state, err
}

// Skip retires a file with a verdict instead of a failure: it was considered,
// and the honest outcome is to leave it untouched. A quality floor that content
// cannot reach is not an error, and retrying it forever would be.
func (s *Store) Skip(ctx context.Context, id int64, code, reason string) error {
	return s.exec(ctx, `
UPDATE files SET state='skipped', decision='skip', reason=?, reason_code=?,
  last_error=NULL, last_error_code=NULL, updated_at=?
WHERE id=?`, reason, code, time.Now().Unix(), id)
}

// Event appends to the structured log. This is the table an agent queries when
// asked "what failed last night, and why".
func (s *Store) Event(ctx context.Context, level, code string, fileID int64, message, data string) error {
	var f any
	if fileID > 0 {
		f = fileID
	}
	var d any
	if data != "" {
		d = data
	}
	return s.exec(ctx, `
INSERT INTO events (at, level, code, file_id, message, data) VALUES (?,?,?,?,?,?)`,
		time.Now().Unix(), level, code, f, message, d)
}

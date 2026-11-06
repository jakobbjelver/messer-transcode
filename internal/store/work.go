package store

import (
	"context"
	"time"
)

// Work is a file the runner may pick up.
type Work struct {
	ID         int64
	Path       string
	Library    string
	Decision   string
	TargetKbps int
	SizeBytes  int64
	DurationS  float64
}

// ClaimWork returns up to limit planned files that need real work and moves
// them to "encoding" in the same transaction, so two runners cannot collide.
func (s *Store) ClaimWork(ctx context.Context, limit int) ([]Work, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx, `
SELECT id, path, library, decision, COALESCE(target_kbps,0), size_bytes, COALESCE(duration_s,0)
FROM files
WHERE state = 'planned' AND decision IN ('encode','remux')
ORDER BY id
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
		if _, err := tx.ExecContext(ctx,
			`UPDATE files SET state='encoding', started_at=?, updated_at=? WHERE id=?`, now, now, w.ID); err != nil {
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
	return s.exec(ctx,
		`UPDATE files SET state=?, updated_at=? WHERE id=?`, state, time.Now().Unix(), id)
}

// Finish records a successful replacement.
func (s *Store) Finish(ctx context.Context, id, before, after int64, vmaf float64) error {
	var v any
	if vmaf > 0 {
		v = vmaf
	}
	return s.exec(ctx, `
UPDATE files SET state='done', bytes_before=?, bytes_after=?, vmaf=?, finished_at=?,
  last_error=NULL, last_error_code=NULL, updated_at=?
WHERE id=?`, before, after, v, time.Now().Unix(), time.Now().Unix(), id)
}

// Fail records an attempt. A file returns to "planned" until it runs out of
// attempts, which keeps a transient error from parking it forever.
func (s *Store) Fail(ctx context.Context, id int64, code, message string, maxAttempts int) (state string, err error) {
	var attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT attempts FROM files WHERE id=?`, id).Scan(&attempts); err != nil {
		return "", err
	}
	attempts++
	state = "planned"
	if maxAttempts > 0 && attempts >= maxAttempts {
		state = "failed"
	}
	err = s.exec(ctx, `
UPDATE files SET attempts=?, state=?, last_error=?, last_error_code=?, updated_at=?
WHERE id=?`, attempts, state, message, code, time.Now().Unix(), id)
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

package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

// Control is the operator's hand on the queue: paused, or a window forced open
// or shut outside the schedule.
//
// It lives in the `settings` table rather than in the server's memory because
// the container is recreated nightly (Komodo's global auto-update at 03:00,
// inside the window). A pause that a redeploy forgets is a night lost with no
// error anywhere, which is the exact failure mode this state exists to prevent.
type Control struct {
	Paused     bool   `json:"paused"`
	PausedAt   int64  `json:"paused_at,omitempty"`
	PausedBy   string `json:"paused_by,omitempty"`
	Override   string `json:"override"` // "" | "open" | "close"
	Until      int64  `json:"override_until,omitempty"`
	OverrideBy string `json:"override_by,omitempty"`
}

const (
	OverrideOpen  = "open"
	OverrideClose = "close"
)

// Control reads the current steering state. An expired override is reported as
// no override: expiry is evaluated, never swept, so nothing has to run for it
// to stop being true.
func (s *Store) Control(ctx context.Context) (Control, error) {
	var c Control
	settings, err := s.settings(ctx)
	if err != nil {
		return c, err
	}

	if v := settings["queue.paused"]; v != "" {
		c.Paused = true
		c.PausedAt, _ = strconv.ParseInt(v, 10, 64)
		c.PausedBy = settings["queue.paused_by"]
	}

	kind := settings["window.override"]
	until, _ := strconv.ParseInt(settings["window.override_until"], 10, 64)
	expired := until > 0 && time.Now().Unix() >= until
	if (kind == OverrideOpen || kind == OverrideClose) && !expired {
		c.Override = kind
		c.Until = until
		c.OverrideBy = settings["window.override_by"]
	}
	return c, nil
}

// SetPaused pauses or resumes the queue. Pausing records when and by whom; the
// health check uses the timestamp to complain about a pause nobody remembered.
func (s *Store) SetPaused(ctx context.Context, paused bool, by string) error {
	if !paused {
		return s.clearSettings(ctx, "queue.paused", "queue.paused_by")
	}
	return s.putSettings(ctx, map[string]string{
		"queue.paused":    strconv.FormatInt(time.Now().Unix(), 10),
		"queue.paused_by": by,
	})
}

// SetOverride forces the window open or shut. until == 0 means "until somebody
// says otherwise"; a non-zero value expires on its own.
func (s *Store) SetOverride(ctx context.Context, kind string, until int64, by string) error {
	if kind != OverrideOpen && kind != OverrideClose {
		return errors.New("override must be open or close")
	}
	return s.putSettings(ctx, map[string]string{
		"window.override":       kind,
		"window.override_until": strconv.FormatInt(until, 10),
		"window.override_by":    by,
	})
}

// ClearOverride returns the window to the schedule.
func (s *Store) ClearOverride(ctx context.Context) error {
	return s.clearSettings(ctx, "window.override", "window.override_until", "window.override_by")
}

func (s *Store) settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) putSettings(ctx context.Context, kv map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, v := range kv {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) clearSettings(ctx context.Context, keys ...string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, k := range keys {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// setting is one key, for callers that only need a marker (the one-time
// migrations use it).
func (s *Store) setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

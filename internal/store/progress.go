package store

import (
	"context"
	"time"
)

// SetProgress records how far the current encode has got. Called from the
// encoder's progress callback, so it must stay cheap: one row, one column.
func (s *Store) SetProgress(ctx context.Context, id int64, fraction, speed float64) error {
	return s.exec(ctx, `UPDATE files SET progress = ?, speed = ? WHERE id = ?`,
		fraction, speed, id)
}

// Live is the job currently holding the machine, if any.
type Live struct {
	FileID   int64   `json:"file_id"`
	Path     string  `json:"path"`
	Stage    string  `json:"stage"`
	Fraction float64 `json:"fraction"`
	Speed    float64 `json:"speed"`
	Target   int     `json:"target_kbps"`
	Bytes    int64   `json:"bytes"`
	Since    int64   `json:"since"`
}

// Live returns the one file in flight. There is at most one by design: the
// media engine is serial and two encodes on the same volume starve each other.
func (s *Store) Live(ctx context.Context) (Live, bool, error) {
	var l Live
	err := s.db.QueryRowContext(ctx, `
SELECT id, path, state, COALESCE(progress,0), COALESCE(speed,0),
       COALESCE(target_kbps,0), size_bytes, COALESCE(updated_at,0)
FROM files
WHERE state IN ('encoding','verifying','replacing')
ORDER BY updated_at DESC LIMIT 1`).Scan(
		&l.FileID, &l.Path, &l.Stage, &l.Fraction, &l.Speed, &l.Target, &l.Bytes, &l.Since)
	if err != nil {
		return l, false, nil // no rows is the common case, not an error
	}
	return l, true, nil
}

// StaleSince reports whether a file has sat in a working state with no
// heartbeat long enough that its node is probably gone.
func (l Live) StaleSince(now time.Time, window time.Duration) bool {
	return l.Since > 0 && now.Sub(time.Unix(l.Since, 0)) > window
}

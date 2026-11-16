package store

import (
	"context"
	"time"
)

// ReapExpired returns files whose node stopped reporting to the queue.
//
// A live node writes progress every couple of seconds, and every heartbeat
// stamps lease_at. A node that died stops writing, and after the lease the file
// is claimable again — which is the whole reason the state machine has an
// "encoding" row rather than trusting the worker to come back.
//
// The lease is a separate column from updated_at on purpose: updated_at is the
// scanner's stamp and the dashboard's "since", and a heartbeat writing it would
// make an unseen row look seen and a long job look new.
//
// Rows claimed before 0005 have lease_at = 0; they fall back to updated_at so an
// upgrade cannot strand a job that was in flight when the container restarted.
//
// This runs on the claim path rather than on a timer: the only moment the
// answer matters is when somebody is asking for work.
func (s *Store) ReapExpired(ctx context.Context, lease time.Duration) (int, error) {
	cutoff := time.Now().Add(-lease).Unix()
	res, err := s.db.ExecContext(ctx, `
UPDATE files SET state='planned', updated_at=?
WHERE state IN ('encoding','verifying','replacing')
  AND COALESCE(NULLIF(lease_at,0), updated_at) < ?`,
		time.Now().Unix(), cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

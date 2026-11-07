package store

import (
	"context"
	"time"
)

// ReapExpired returns files whose node stopped reporting to the queue.
//
// A live node writes progress every couple of seconds, so updated_at stays
// fresh. A node that died stops writing, and after the lease the file is
// claimable again — which is the whole reason the state machine has an
// "encoding" row rather than trusting the worker to come back.
//
// This runs on the claim path rather than on a timer: the only moment the
// answer matters is when somebody is asking for work.
func (s *Store) ReapExpired(ctx context.Context, lease time.Duration) (int, error) {
	cutoff := time.Now().Add(-lease).Unix()
	res, err := s.db.ExecContext(ctx, `
UPDATE files SET state='planned', updated_at=?
WHERE state IN ('encoding','verifying','replacing') AND updated_at < ?`,
		time.Now().Unix(), cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

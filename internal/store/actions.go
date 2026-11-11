package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrState is "that action does not apply to that row": a replaced file cannot
// be retried, a file mid-swap cannot be skipped. The server turns it into a 409
// so a stale page says why instead of doing nothing.
var ErrState = errors.New("the file is not in a state where that action applies")

// ErrNotFound is returned when the id does not exist at all.
var ErrNotFound = errors.New("no such file")

// Actions an operator can apply to the queue. They are named because the bulk
// endpoint takes one as a string and the CLI takes one as a verb.
const (
	ActRetry    = "retry"
	ActSkip     = "skip"
	ActPriority = "priority"
)

// Refusal is one row an action did not apply to, with the state it was in. A
// bulk action reports these instead of failing the batch: a stale page with 40
// selected rows must apply the 37 that still make sense and say so.
type Refusal struct {
	ID     int64  `json:"id"`
	State  string `json:"state,omitempty"`
	Reason string `json:"reason"` // "state" or "missing"
}

// ActResult is what a queue action did.
type ActResult struct {
	Applied int       `json:"applied"`
	Refused []Refusal `json:"refused"`
}

// Act applies one action to many rows in a single transaction.
//
// Retry:    failed/skipped → planned, attempts reset, error cleared.
// Skip:     planned/failed/skipped → skipped, reason M_SKIPPED (idempotent).
// Priority: planned/failed → the given priority (-100 last, 0 normal, 100 next).
//
// A row in any other state is refused, not silently dropped, and one event line
// records the batch so the activity feed explains a queue that moved without
// the node.
func (s *Store) Act(ctx context.Context, action string, ids []int64, priority int) (ActResult, error) {
	// An empty slice, not nil: a JSON client reads `refused: null` as "no field"
	// and then crashes counting it. The wire shape is part of the contract.
	res := ActResult{Refused: []Refusal{}}
	if len(ids) == 0 {
		return res, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	for _, id := range ids {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM files WHERE id = ?`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			res.Refused = append(res.Refused, Refusal{ID: id, Reason: "missing"})
			continue
		}
		if err != nil {
			return res, err
		}

		query, args, err := actionSQL(action, id, now, priority)
		if err != nil {
			return res, err
		}
		out, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return res, err
		}
		if n, _ := out.RowsAffected(); n == 0 {
			res.Refused = append(res.Refused, Refusal{ID: id, State: state, Reason: "state"})
			continue
		}
		res.Applied++
	}

	if err := tx.Commit(); err != nil {
		return res, err
	}

	// One line for the batch, filed against the single row when there is one.
	// Written after the commit on purpose: an event that survived a rolled-back
	// batch would describe work that did not happen.
	if res.Applied > 0 {
		code, message := actionEvent(action, res.Applied)
		var fileID int64
		if len(ids) == 1 && res.Applied == 1 {
			fileID = ids[0]
		}
		if err := s.Event(ctx, "info", code, fileID, message, ""); err != nil {
			return res, err
		}
	}
	return res, nil
}

func actionSQL(action string, id, now int64, priority int) (string, []any, error) {
	switch action {
	case ActRetry:
		return `UPDATE files SET state='planned', attempts=0, last_error=NULL, last_error_code=NULL, updated_at=?
WHERE id=? AND state IN ('failed','skipped')`, []any{now, id}, nil
	case ActSkip:
		return `UPDATE files SET state='skipped', decision='skip', reason='skipped by hand',
  reason_code='M_SKIPPED', last_error=NULL, last_error_code=NULL, updated_at=?
WHERE id=? AND state IN ('planned','failed','skipped')`, []any{now, id}, nil
	case ActPriority:
		return `UPDATE files SET priority=?, updated_at=? WHERE id=? AND state IN ('planned','failed')`,
			[]any{priority, now, id}, nil
	default:
		return "", nil, fmt.Errorf("unknown action %q", action)
	}
}

func actionEvent(action string, n int) (code, message string) {
	switch action {
	case ActRetry:
		return "M_RETRY", fmt.Sprintf("%d file(s) retried by hand", n)
	case ActSkip:
		return "M_SKIPPED", fmt.Sprintf("%d file(s) skipped by hand", n)
	case ActPriority:
		return "M_PRIORITY", fmt.Sprintf("%d file(s) re-prioritised by hand", n)
	default:
		return "M_ACTION", "queue action"
	}
}

// Retry is the single-row form, kept for the CLI and the tests.
func (s *Store) Retry(ctx context.Context, id int64) error { return s.one(ctx, ActRetry, id, 0) }

// SkipManual retires a file with a manual verdict. (The pipeline's Skip, which
// takes the node's own code and reason, is a different verb with a different
// caller — keep them apart so a manual skip can never be mistaken for a
// verdict the encoder reached.)
func (s *Store) SkipManual(ctx context.Context, id int64) error { return s.one(ctx, ActSkip, id, 0) }

// SetPriority pushes a file to the front or the back of the claimed order.
func (s *Store) SetPriority(ctx context.Context, id int64, priority int) error {
	return s.one(ctx, ActPriority, id, priority)
}

func (s *Store) one(ctx context.Context, action string, id int64, priority int) error {
	res, err := s.Act(ctx, action, []int64{id}, priority)
	if err != nil {
		return err
	}
	if res.Applied > 0 {
		return nil
	}
	if len(res.Refused) == 0 || res.Refused[0].Reason == "missing" {
		return ErrNotFound
	}
	return fmt.Errorf("%w: state is %q", ErrState, res.Refused[0].State)
}

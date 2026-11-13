package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/config"
)

// RunRow is one entry of the run ledger: an occurrence of the schedule, frozen
// when it was first seen.
//
// It exists because a run used to be recomputed from whatever schedule happened
// to be configured when somebody looked. That is fine while the schedule is a
// constant and untenable the moment it is editable: the first save would rewrite
// every past night's figures. The ledger is the fix — the id is the start second,
// so it is also the URL.
type RunRow struct {
	ID       int64  `json:"id"`
	StartAt  int64  `json:"start_at"`
	EndAt    int64  `json:"end_at"`
	Spec     string `json:"spec"`
	Kind     string `json:"kind"`
	ClosedAt int64  `json:"closed_at,omitempty"`
}

// KindManual marks a run that exists because an operator forced the window or
// claimed work outside it. It has no scheduled end.
const KindManual = "manual"

// EnsureRun records an occurrence, idempotently, and returns its id.
func (s *Store) EnsureRun(ctx context.Context, run RunRow) (int64, error) {
	if run.ID == 0 {
		run.ID = run.StartAt
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO runs (id, start_at, end_at, spec, kind, created_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO NOTHING`, run.ID, run.StartAt, run.EndAt, run.Spec, run.Kind, time.Now().Unix())
	return run.ID, err
}

// SyncRuns materialises every occurrence it is given and closes the ones that
// have ended.
//
// This is what makes an *empty* run visible: nothing was claimed, but the window
// still happened, and "it ran and did nothing" is a different fact from "it did
// not run". Closing is recorded once, so a run's end cannot drift.
func (s *Store) SyncRuns(ctx context.Context, occ []config.Occurrence, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, o := range occ {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO runs (id, start_at, end_at, spec, kind, created_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO NOTHING`,
			o.Start.Unix(), o.Start.Unix(), o.End.Unix(), o.Spec, o.Kind, now.Unix()); err != nil {
			return err
		}
		if o.End.Before(now) {
			if _, err := tx.ExecContext(ctx, `
UPDATE runs SET closed_at = ?
WHERE id = ? AND closed_at = 0`, o.End.Unix(), o.Start.Unix()); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// OpenManualRun returns the manual run that is still collecting, if any.
func (s *Store) OpenManualRun(ctx context.Context) (int64, bool) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
SELECT id FROM runs WHERE kind = ? AND closed_at = 0 ORDER BY id DESC LIMIT 1`, KindManual).Scan(&id)
	if err != nil {
		return 0, false
	}
	return id, true
}

// CloseManualRuns ends every open manual run at `at`, and reports how many it
// closed. Called when the schedule takes over again (a scheduled claim, or an
// operator handing the window back): a forced session is a run, not a mode.
func (s *Store) CloseManualRuns(ctx context.Context, at int64) (int, error) {
	out, err := s.db.ExecContext(ctx, `
UPDATE runs SET closed_at = ? WHERE kind = ? AND closed_at = 0`, at, KindManual)
	if err != nil {
		return 0, err
	}
	n, _ := out.RowsAffected()
	return int(n), nil
}

// RunRows returns the newest runs.
func (s *Store) RunRows(ctx context.Context, limit int) ([]RunRow, error) {
	if limit <= 0 || limit > 400 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, start_at, end_at, spec, kind, closed_at FROM runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.ID, &r.StartAt, &r.EndAt, &r.Spec, &r.Kind, &r.ClosedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunRowByID reads one run.
func (s *Store) RunRowByID(ctx context.Context, id int64) (RunRow, error) {
	var r RunRow
	err := s.db.QueryRowContext(ctx,
		`SELECT id, start_at, end_at, spec, kind, closed_at FROM runs WHERE id = ?`, id).
		Scan(&r.ID, &r.StartAt, &r.EndAt, &r.Spec, &r.Kind, &r.ClosedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// RunAggregate is what one run did, computed from the rows stamped with it.
type RunAggregate struct {
	Done        int
	Encoded     int
	Remuxed     int
	Before      int64
	After       int64
	BusySeconds int64
	VmafN       int
	VmafSum     float64
	VmafMin     float64
	FailedCodes map[string]int
	SkipCodes   map[string]int
}

// RunAggregates gathers the finished, failed and skipped figures for a set of
// runs in two queries: one for the finished totals, one for the refusal codes.
//
// Rows are attributed by run_id, which is stamped when the file is claimed — so
// a job claimed at 05:59 belongs to the run that started it even if it finishes
// after the window closes. (The old code attributed by finished_at and quietly
// moved that job into the next night.)
func (s *Store) RunAggregates(ctx context.Context, ids []int64) (map[int64]RunAggregate, error) {
	out := map[int64]RunAggregate{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := placeholders(len(ids))
	args := int64Args(ids)

	rows, err := s.db.QueryContext(ctx, `
SELECT run_id,
       COUNT(*),
       COALESCE(SUM(CASE WHEN bytes_before > 0 THEN bytes_before ELSE size_bytes END),0),
       COALESCE(SUM(CASE WHEN bytes_after  > 0 THEN bytes_after  ELSE size_bytes END),0),
       COALESCE(SUM(CASE WHEN started_at > 0 AND finished_at > started_at
                         THEN finished_at - started_at ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN decision='encode' THEN 1 ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN decision='remux'  THEN 1 ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN vmaf > 0 THEN 1 ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN vmaf > 0 THEN vmaf ELSE 0 END),0),
       COALESCE(MIN(CASE WHEN vmaf > 0 THEN vmaf END),0)
FROM files
WHERE run_id IN (`+ph+`) AND finished_at IS NOT NULL
GROUP BY run_id`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var a RunAggregate
		if err := rows.Scan(&id, &a.Done, &a.Before, &a.After, &a.BusySeconds,
			&a.Encoded, &a.Remuxed, &a.VmafN, &a.VmafSum, &a.VmafMin); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[id] = a
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Refusals: a row that failed or was skipped while claimed in this run.
	// Two queries rather than one with a CASE: the code that identifies a
	// failure (last_error_code) and the one that identifies a skip (reason_code)
	// are different fields, and a single grouped query has to invent which
	// counter a row belongs to.
	for _, q := range []struct {
		state string
		col   string
		into  func(*RunAggregate) map[string]int
	}{
		{"failed", "COALESCE(NULLIF(last_error_code,''), 'E_UNKNOWN')", func(a *RunAggregate) map[string]int {
			if a.FailedCodes == nil {
				a.FailedCodes = map[string]int{}
			}
			return a.FailedCodes
		}},
		{"skipped", "COALESCE(NULLIF(reason_code,''), 'R_UNKNOWN')", func(a *RunAggregate) map[string]int {
			if a.SkipCodes == nil {
				a.SkipCodes = map[string]int{}
			}
			return a.SkipCodes
		}},
	} {
		rows, err := s.db.QueryContext(ctx, `
SELECT run_id, `+q.col+`, COUNT(*) FROM files
WHERE run_id IN (`+ph+`) AND COALESCE(NULLIF(retired_from,''), state) = ?
GROUP BY run_id, 2`, append(append([]any{}, args...), q.state)...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var code string
			var n int
			if err := rows.Scan(&id, &code, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			a := out[id]
			q.into(&a)[code] += n
			out[id] = a
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// BackfillRuns attributes rows that predate the ledger, once.
//
// `locate` maps a unix second to the occurrence it belongs to; the caller owns
// the schedule because the store does not. The result is the number of rows
// attributed, which is worth logging: it is the one moment history is derived
// rather than recorded.
func (s *Store) BackfillRuns(ctx context.Context, locate func(unix int64) (config.Occurrence, bool)) (int, error) {
	if done, err := s.setting(ctx, "runs.backfilled"); err != nil || done != "" {
		return 0, err
	}

	type pending struct {
		id       int64
		started  sql.NullInt64
		finished sql.NullInt64
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, started_at, finished_at FROM files
WHERE run_id IS NULL AND (started_at IS NOT NULL OR finished_at IS NOT NULL)`)
	if err != nil {
		return 0, err
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.started, &p.finished); err != nil {
			_ = rows.Close()
			return 0, err
		}
		todo = append(todo, p)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	n := 0
	for _, p := range todo {
		ts := p.finished.Int64
		if p.started.Valid && p.started.Int64 > 0 {
			ts = p.started.Int64
		}
		if ts == 0 {
			continue
		}
		occ, ok := locate(ts)
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO runs (id, start_at, end_at, spec, kind, created_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(id) DO NOTHING`,
			occ.Start.Unix(), occ.Start.Unix(), occ.End.Unix(), occ.Spec, occ.Kind, time.Now().Unix()); err != nil {
			return n, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE files SET run_id = ? WHERE id = ?`, occ.Start.Unix(), p.id); err != nil {
			return n, err
		}
		n++
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO settings (key, value) VALUES ('runs.backfilled', ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, time.Now().Format(time.RFC3339)); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	out := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

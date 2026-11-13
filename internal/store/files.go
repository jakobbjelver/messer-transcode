package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// ProbeKey fingerprints the attributes that invalidate a probe.
func ProbeKey(size, mtimeNS int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d", size, mtimeNS)))
	return hex.EncodeToString(sum[:8])
}

// UpsertScanned records the scanned set for a library in one transaction.
// Inserted counts new rows; Changed counts rows whose size or mtime moved,
// which resets them to "new" so they are re-probed rather than trusted.
func (s *Store) UpsertScanned(ctx context.Context, library string, files []media.Scanned) (inserted, changed int, err error) {
	return s.UpsertScannedAt(ctx, library, files, time.Now().Unix())
}

// UpsertScannedAt is UpsertScanned with the scan's stamp given by the caller, so
// MarkGone can tell "not in this walk" from "not in any walk" without a race
// against the clock. Every file in the set is stamped `at`; a row whose
// updated_at is older than `at` was not seen by this scan.
//
// A row that comes back from "gone" is reset to "new" with no probe key: the
// facts the planner held described a file that had left the disk, and the file
// that returned has to be measured again.
func (s *Store) UpsertScannedAt(ctx context.Context, library string, files []media.Scanned, at int64) (inserted, changed int, err error) {
	const q = `
INSERT INTO files (path, library, size_bytes, mtime_ns, probe_key, state, attempts, updated_at)
VALUES (?, ?, ?, ?, NULL, 'new', 0, ?)
ON CONFLICT(path) DO UPDATE SET
  library    = excluded.library,
  size_bytes = excluded.size_bytes,
  mtime_ns   = excluded.mtime_ns,
  probe_key  = CASE WHEN files.state = 'gone'
                     OR files.size_bytes <> excluded.size_bytes
                     OR files.mtime_ns   <> excluded.mtime_ns
                    THEN NULL ELSE files.probe_key END,
  state      = CASE WHEN files.state = 'gone'
                     OR files.size_bytes <> excluded.size_bytes
                     OR files.mtime_ns   <> excluded.mtime_ns
                    THEN 'new' ELSE files.state END,
  -- A revived file has no retirement behind it any more, so the run ledger must
  -- read its live state again rather than the one the retirement preserved.
  retired_from = CASE WHEN files.state = 'gone'
                     OR files.size_bytes <> excluded.size_bytes
                     OR files.mtime_ns   <> excluded.mtime_ns
                    THEN NULL ELSE files.retired_from END,
  updated_at = excluded.updated_at`

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = stmt.Close() }()

	for _, f := range files {
		// SQLite reports an insert and an update as one affected row, so the
		// counters have to come from the row that was there before. One indexed
		// lookup per file inside the scan's transaction is cheap next to the
		// stat that produced it, and the alternative was a counter that lied.
		var oldSize, oldMtime int64
		exists := true
		err := tx.QueryRowContext(ctx,
			`SELECT size_bytes, mtime_ns FROM files WHERE path = ?`, f.Path).
			Scan(&oldSize, &oldMtime)
		if errors.Is(err, sql.ErrNoRows) {
			exists = false
		} else if err != nil {
			return inserted, changed, fmt.Errorf("lookup %s: %w", f.Path, err)
		}

		if _, err := stmt.ExecContext(ctx, f.Path, library, f.Size, f.MtimeNS, at); err != nil {
			return inserted, changed, fmt.Errorf("upsert %s: %w", f.Path, err)
		}
		if !exists {
			inserted++
		} else if oldSize != f.Size || oldMtime != f.MtimeNS {
			changed++
		}
	}
	if err := tx.Commit(); err != nil {
		return inserted, changed, err
	}
	return inserted, changed, nil
}

// MarkGone retires the rows a scan did not see.
//
// It is the tombstone the catalogue did not have. A file deleted between scans —
// Sonarr upgrading an episode, a manual prune — kept its row, so the dashboard
// counted bytes that were not on the disk and the queue spent a claim on a path
// that could only fail with E_SOURCE_MISSING. Nothing is deleted here: a gone
// row keeps its finish, its sizes and its last error, and it keeps the state the
// retirement replaced in retired_from, because the run ledger counts what a file
// did on a night and this must not rewrite a night that already happened.
//
// Rows mid-job are never touched. The encoder holds a file under a temporary
// name while it works, so a walk can legitimately miss one that is being encoded
// right now, and the encoder is the one that is right.
func (s *Store) MarkGone(ctx context.Context, library string, seenAt int64) (int, error) {
	res, err := s.db.ExecContext(ctx, `
UPDATE files SET state = 'gone', retired_from = state
WHERE library = ? AND updated_at < ?
  AND state NOT IN ('encoding','verifying','replacing','gone')`, library, seenAt)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Pending is a file awaiting a probe or a decision.
type Pending struct {
	ID    int64
	Path  string
	Size  int64
	Mtime int64
}

// FilesNeedingProbe returns rows with no valid probe. A gone row is not work:
// the file is not there to measure.
func (s *Store) FilesNeedingProbe(ctx context.Context) ([]Pending, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, path, size_bytes, mtime_ns FROM files
		 WHERE probe_key IS NULL AND state <> 'gone' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanPending(rows)
}

// FilesUpToDate probes no file that already has a valid key.
func (s *Store) FilesUpToDate(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE probe_key IS NOT NULL`).Scan(&n)
	return n, err
}

func scanPending(rows *sql.Rows) ([]Pending, error) {
	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.ID, &p.Path, &p.Size, &p.Mtime); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetProbe stores a probe result and moves the file to "probed".
func (s *Store) SetProbe(ctx context.Context, id int64, p media.Probe, key string, tmdbID *int64) error {
	audio, err := json.Marshal(p.Audio)
	if err != nil {
		return err
	}
	subs, err := json.Marshal(p.Subs)
	if err != nil {
		return err
	}
	return s.exec(ctx, `
UPDATE files SET
  probe_key = ?, container = ?, video_codec = ?, width = ?, height = ?,
  bitrate_kbps = ?, duration_s = ?, pix_fmt = ?,
  audio_tracks = ?, sub_tracks = ?, tmdb_id = COALESCE(?, tmdb_id),
  state = 'probed', last_error = NULL, last_error_code = NULL, updated_at = ?
WHERE id = ?`,
		key, p.Container, p.VideoCodec, p.Width, p.Height,
		p.BitrateKbps, p.DurationS, p.PixFmt,
		string(audio), string(subs), nullInt(tmdbID),
		time.Now().Unix(), id)
}

// SetProbeError records why a probe failed, leaving the file for a retry.
func (s *Store) SetProbeError(ctx context.Context, id int64, code, message string) error {
	return s.exec(ctx, `
UPDATE files SET state = 'failed', last_error = ?, last_error_code = ?, updated_at = ?
WHERE id = ?`, message, code, time.Now().Unix(), id)
}

// Decision is the planner's verdict.
type Decision struct {
	Decision string
	Code     string
	Reason   string
	KeyKbps  int
	State    string
}

// SetDecision stores a verdict and its target bitrate.
func (s *Store) SetDecision(ctx context.Context, id int64, d Decision) error {
	return s.exec(ctx, `
UPDATE files SET decision = ?, reason = ?, reason_code = ?, target_kbps = ?,
  state = ?, updated_at = ?
WHERE id = ?`,
		d.Decision, d.Reason, d.Code, nullKbps(d.KeyKbps), d.State, time.Now().Unix(), id)
}

func nullKbps(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

// Undecided is a probed file with no verdict yet.
type Undecided struct {
	ID          int64
	Path        string
	Size        int64
	Width       int
	Height      int
	Codec       string
	BitrateKbps int
	// AudioTracks is the raw JSON array of audio streams, so a decision rule can
	// read codecs and the track count without the store knowing track shapes.
	AudioTracks string
}

// FilesNeedingDecision returns probed files without a verdict. A gone row keeps
// whatever verdict it had; deciding it again would put a deleted file back in
// the queue.
func (s *Store) FilesNeedingDecision(ctx context.Context) ([]Undecided, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, path, size_bytes, COALESCE(width,0), COALESCE(height,0), COALESCE(video_codec,''), COALESCE(bitrate_kbps,0),
       COALESCE(audio_tracks,'[]')
FROM files
WHERE probe_key IS NOT NULL AND (decision IS NULL OR decision = '') AND state <> 'gone'
ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Undecided
	for rows.Next() {
		var u Undecided
		if err := rows.Scan(&u.ID, &u.Path, &u.Size, &u.Width, &u.Height, &u.Codec, &u.BitrateKbps, &u.AudioTracks); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Bucket is one row of the plan summary.
type Bucket struct {
	Decision    string `json:"decision"`
	ReasonCode  string `json:"reason_code"`
	Files       int    `json:"files"`
	SizeBytes   int64  `json:"size_bytes"`
	EstimateOut int64  `json:"estimate_out_bytes"`
}

// Summary groups the decided set by decision and reason. Gone rows are not part
// of the catalogue any more, so they are not part of its arithmetic either.
func (s *Store) Summary(ctx context.Context) ([]Bucket, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT COALESCE(decision,'none'), COALESCE(reason_code,''), COUNT(*), COALESCE(SUM(size_bytes),0),
       CAST(COALESCE(SUM(CASE WHEN decision = 'encode' AND target_kbps > 0 AND duration_s > 0
                         THEN target_kbps * 1000.0 / 8 * duration_s
                         ELSE size_bytes END), 0) AS INTEGER)
FROM files
WHERE state <> 'gone'
GROUP BY 1, 2
ORDER BY 4 DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.Decision, &b.ReasonCode, &b.Files, &b.SizeBytes, &b.EstimateOut); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// StateCounts returns how many files sit in each state.
func (s *Store) StateCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM files GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, err
		}
		out[state] = n
	}
	return out, rows.Err()
}

// FileRow is the per-file detail printed by plan --json.
type FileRow struct {
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	Codec       string `json:"codec"`
	Height      int    `json:"height"`
	BitrateKbps int    `json:"bitrate_kbps"`
	Decision    string `json:"decision"`
	ReasonCode  string `json:"reason_code"`
	Reason      string `json:"reason"`
	TargetKbps  int    `json:"target_kbps,omitempty"`
}

// DecidedFiles returns every file that has a verdict.
func (s *Store) DecidedFiles(ctx context.Context) ([]FileRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT path, size_bytes, COALESCE(video_codec,''), COALESCE(height,0), COALESCE(bitrate_kbps,0),
       COALESCE(decision,''), COALESCE(reason_code,''), COALESCE(reason,''), COALESCE(target_kbps,0)
FROM files
WHERE decision IS NOT NULL AND decision <> '' AND state <> 'gone'
ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []FileRow
	for rows.Next() {
		var r FileRow
		if err := rows.Scan(&r.Path, &r.SizeBytes, &r.Codec, &r.Height, &r.BitrateKbps,
			&r.Decision, &r.ReasonCode, &r.Reason, &r.TargetKbps); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jakobbjelver/messer-transcode/internal/media"
)

// Dashboard is everything the overview page shows, in one round trip.
type Dashboard struct {
	TotalFiles     int            `json:"total_files"`
	TotalBytes     int64          `json:"total_bytes"`
	ReclaimedBytes int64          `json:"reclaimed_bytes"`
	DoneBytes      int64          `json:"done_bytes"`
	ProjectedBytes int64          `json:"projected_bytes"`
	States         map[string]int `json:"states"`
	Decisions      map[string]int `json:"decisions"`
	Failures       int            `json:"failures"`
}

// Snapshot builds the dashboard figures. It is deliberately a handful of cheap
// aggregates over an indexed table: the page polls it once a second.
func (s *Store) Snapshot(ctx context.Context) (Dashboard, error) {
	d := Dashboard{
		States:    map[string]int{},
		Decisions: map[string]int{},
	}

	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COALESCE(SUM(size_bytes),0),
       COALESCE(SUM(CASE WHEN state='done'
                         THEN MAX(COALESCE(bytes_before,0) - COALESCE(bytes_after,0), 0)
                         ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN state='done' THEN COALESCE(bytes_after,0) ELSE 0 END),0),
       COALESCE(SUM(CASE WHEN state='failed' THEN 1 ELSE 0 END),0)
FROM files`).Scan(&d.TotalFiles, &d.TotalBytes, &d.ReclaimedBytes, &d.DoneBytes, &d.Failures)
	if err != nil {
		return d, err
	}

	if err := s.countInto(ctx, `SELECT state, COUNT(*) FROM files GROUP BY 1`, d.States); err != nil {
		return d, err
	}
	if err := s.countInto(ctx, `SELECT COALESCE(decision,'pending'), COUNT(*) FROM files GROUP BY 1`, d.Decisions); err != nil {
		return d, err
	}

	// What the queue still intends to do, in bytes.
	var projected int64
	err = s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(CASE WHEN decision='encode' AND target_kbps > 0 AND duration_s > 0
                         THEN CAST(target_kbps * 1000.0 / 8 * duration_s AS INTEGER)
                         ELSE size_bytes END),0)
FROM files
WHERE state IN ('planned','encoding','verifying','replacing')`).Scan(&projected)
	if err != nil {
		return d, err
	}
	d.ProjectedBytes = projected
	return d, nil
}

func (s *Store) countInto(ctx context.Context, query string, into map[string]int) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return err
		}
		into[key] = n
	}
	return rows.Err()
}

// EventRow is one line of the activity feed.
type EventRow struct {
	At      int64  `json:"at"`
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	FileID  int64  `json:"file_id,omitempty"`
}

// RecentEvents returns the newest activity, for the dashboard feed.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]EventRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT at, level, COALESCE(code,''), message, COALESCE(file_id,0)
FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.At, &e.Level, &e.Code, &e.Message, &e.FileID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// SummaryRow is one line of the queue page.
type SummaryRow struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	Library    string `json:"library"`
	Decision   string `json:"decision"`
	ReasonCode string `json:"reason_code"`
	Reason     string `json:"reason"`
	State      string `json:"state"`
	Codec      string `json:"codec"`
	Height     int    `json:"height"`
	Bitrate    int    `json:"bitrate_kbps"`
	Target     int    `json:"target_kbps"`
	SizeBytes  int64  `json:"size_bytes"`
}

// Queue lists files for the queue page, filtered by state when given.
func (s *Store) Queue(ctx context.Context, state, decision string, limit, offset int) ([]SummaryRow, error) {
	query := `
SELECT id, path, library, COALESCE(decision,''), COALESCE(reason_code,''), COALESCE(reason,''),
       state, COALESCE(video_codec,''), COALESCE(height,0), COALESCE(bitrate_kbps,0),
       COALESCE(target_kbps,0), size_bytes
FROM files
WHERE (? = '' OR state = ?) AND (? = '' OR decision = ?)
ORDER BY
  CASE state WHEN 'encoding' THEN 0 WHEN 'verifying' THEN 1 WHEN 'replacing' THEN 2
             WHEN 'planned' THEN 3 WHEN 'failed' THEN 4 ELSE 5 END,
  size_bytes DESC
LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, query, state, state, decision, decision, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []SummaryRow
	for rows.Next() {
		var r SummaryRow
		if err := rows.Scan(&r.ID, &r.Path, &r.Library, &r.Decision, &r.ReasonCode, &r.Reason,
			&r.State, &r.Codec, &r.Height, &r.Bitrate, &r.Target, &r.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FileDetail is the per-file page.
type FileDetail struct {
	SummaryRow
	DurationS   float64 `json:"duration_s"`
	BytesBefore int64   `json:"bytes_before"`
	BytesAfter  int64   `json:"bytes_after"`
	VMAF        float64 `json:"vmaf"`
	Attempts    int     `json:"attempts"`
	LastError   string  `json:"last_error"`
	LastErrorAt int64   `json:"last_error_at"`
	AudioTracks string  `json:"audio_tracks"`
	SubTracks   string  `json:"sub_tracks"`
}

// File returns one file with its full history.
func (s *Store) File(ctx context.Context, id int64) (FileDetail, error) {
	var d FileDetail
	var lastErrCode string
	err := s.db.QueryRowContext(ctx, `
SELECT id, path, library, COALESCE(decision,''), COALESCE(reason_code,''), COALESCE(reason,''),
       state, COALESCE(video_codec,''), COALESCE(height,0), COALESCE(bitrate_kbps,0),
       COALESCE(target_kbps,0), size_bytes, COALESCE(duration_s,0),
       COALESCE(bytes_before,0), COALESCE(bytes_after,0), COALESCE(vmaf,0), attempts,
       COALESCE(last_error,''), COALESCE(last_error_code,''), COALESCE(updated_at,0),
       COALESCE(audio_tracks,''), COALESCE(sub_tracks,'')
FROM files WHERE id = ?`, id).Scan(
		&d.ID, &d.Path, &d.Library, &d.Decision, &d.ReasonCode, &d.Reason,
		&d.State, &d.Codec, &d.Height, &d.Bitrate, &d.Target, &d.SizeBytes, &d.DurationS,
		&d.BytesBefore, &d.BytesAfter, &d.VMAF, &d.Attempts,
		&d.LastError, &lastErrCode, &d.LastErrorAt, &d.AudioTracks, &d.SubTracks)
	if err != nil {
		return d, err
	}
	if d.LastError != "" && lastErrCode != "" {
		d.LastError = lastErrCode + ": " + d.LastError
	}
	return d, nil
}

// AudioList renders the stored track JSON as something a person reads.
// The raw column is a JSON array because the planner needs the full detail;
// the page needs a sentence.
func (f FileDetail) AudioList() string {
	if f.AudioTracks == "" {
		return "—"
	}
	var tracks []media.AudioTrack
	if err := json.Unmarshal([]byte(f.AudioTracks), &tracks); err != nil {
		return f.AudioTracks
	}
	parts := make([]string, 0, len(tracks))
	for _, t := range tracks {
		s := t.Lang
		if s == "" {
			s = "und"
		}
		if t.Codec != "" {
			s += " " + t.Codec
		}
		if t.Channels > 0 {
			s += fmt.Sprintf(" %dch", t.Channels)
		}
		if t.Commentary {
			s += " (commentary)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// SubList is the same for subtitles, keeping the flags that decide whether one
// is worth keeping.
func (f FileDetail) SubList() string {
	if f.SubTracks == "" {
		return "none"
	}
	var tracks []media.SubTrack
	if err := json.Unmarshal([]byte(f.SubTracks), &tracks); err != nil {
		return f.SubTracks
	}
	parts := make([]string, 0, len(tracks))
	for _, t := range tracks {
		s := t.Lang
		if s == "" {
			s = "und"
		}
		switch {
		case t.Forced:
			s += " (forced)"
		case t.HearingImpaired:
			s += " (SDH)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}

// Reclaim estimates the bytes a planned job will return.
func (f FileDetail) Reclaim() int64 {
	if f.BytesBefore > f.BytesAfter && f.BytesAfter > 0 {
		return f.BytesBefore - f.BytesAfter
	}
	if f.State == "planned" && f.Target > 0 && f.DurationS > 0 {
		projected := int64(float64(f.Target) * 1000 / 8 * f.DurationS)
		if f.SizeBytes > projected {
			return f.SizeBytes - projected
		}
	}
	return 0
}

// Now is a package hook so tests can pin the clock.
var Now = time.Now

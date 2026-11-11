package store

import (
	"context"
	"database/sql"
	"errors"
)

// Revision is one version of the configuration.
//
// The content lives here rather than in a file on the mount because the image
// is distroless (no git) and the config directory is not a repository: this is
// the only store that can hold a history and be backed up by copying one file.
type Revision struct {
	Rev       int64  `json:"rev"`
	SHA       string `json:"sha"`
	CreatedAt int64  `json:"created_at"`
	Actor     string `json:"actor"`
	Message   string `json:"message"`
	Parent    int64  `json:"parent_rev,omitempty"`
	Content   string `json:"content,omitempty"`
}

// PutConfigRevision appends a revision and returns it. parent 0 means "the head
// at the time", which is what every normal save wants.
func (s *Store) PutConfigRevision(ctx context.Context, r Revision) (Revision, error) {
	var parent any
	if r.Parent > 0 {
		parent = r.Parent
	}
	res, err := s.db.ExecContext(ctx, `
INSERT INTO config_revisions (sha256, created_at, actor, message, parent_rev, content)
VALUES (?,?,?,?,?,?)`, r.SHA, r.CreatedAt, r.Actor, r.Message, parent, r.Content)
	if err != nil {
		return r, err
	}
	r.Rev, err = res.LastInsertId()
	return r, err
}

// HeadConfigRevision is the newest revision. ErrNotFound means the store has
// never seen a save — a fresh install, or one whose config predates history.
func (s *Store) HeadConfigRevision(ctx context.Context) (Revision, error) {
	var r Revision
	err := s.db.QueryRowContext(ctx, `
SELECT rev, sha256, created_at, actor, message, COALESCE(parent_rev,0), content
FROM config_revisions ORDER BY rev DESC LIMIT 1`).
		Scan(&r.Rev, &r.SHA, &r.CreatedAt, &r.Actor, &r.Message, &r.Parent, &r.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ConfigRevision reads one revision, content included.
func (s *Store) ConfigRevision(ctx context.Context, rev int64) (Revision, error) {
	var r Revision
	err := s.db.QueryRowContext(ctx, `
SELECT rev, sha256, created_at, actor, message, COALESCE(parent_rev,0), content
FROM config_revisions WHERE rev = ?`, rev).
		Scan(&r.Rev, &r.SHA, &r.CreatedAt, &r.Actor, &r.Message, &r.Parent, &r.Content)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

// ConfigRevisions lists the newest revisions without their content: the history
// page shows messages and hashes, and 30 revisions of 10 KB each is not a page.
func (s *Store) ConfigRevisions(ctx context.Context, limit int) ([]Revision, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT rev, sha256, created_at, actor, message, COALESCE(parent_rev,0)
FROM config_revisions ORDER BY rev DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Revision
	for rows.Next() {
		var r Revision
		if err := rows.Scan(&r.Rev, &r.SHA, &r.CreatedAt, &r.Actor, &r.Message, &r.Parent); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

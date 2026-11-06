// Package store is the durable state: SQLite in WAL mode, one writer, every
// transition committed before the next step runs.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure Go driver: CGO_ENABLED=0 stays possible
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store owns the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (and migrates) the database at path.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// WAL allows concurrent readers, but this process is the only writer and
	// serialising keeps the state machine honest.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle for tests and future queries.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate() error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var current int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	for _, name := range names {
		version, err := parseVersion(name)
		if err != nil {
			return err
		}
		if version <= current {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		// PRAGMA cannot be parameterised; version is an int parsed from the filename.
		if _, err := tx.Exec("PRAGMA user_version = " + strconv.Itoa(version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: set user_version: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func parseVersion(name string) (int, error) {
	head, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q must be named <version>_<name>.sql", name)
	}
	v, err := strconv.Atoi(head)
	if err != nil {
		return 0, fmt.Errorf("migration %q has a non-numeric version", name)
	}
	return v, nil
}

// exec runs a statement, ignoring the result.
func (s *Store) exec(ctx context.Context, query string, args ...any) error {
	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

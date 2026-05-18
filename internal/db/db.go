// Package db owns the single *sql.DB shared by the search index and the
// jobs queue. One connection means coherent WAL semantics, simpler
// lifecycle, and no duplicate schema migration code.
package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// DB wraps a *sql.DB plus the path it was opened from, so a caller can
// re-open it cleanly after a restart.
type DB struct {
	*sql.DB
	path string
	mu   sync.Mutex
}

// Open opens (and migrates) the noto SQLite database at sqlitePath.
// sqlitePath is typically `<config_dir>/noto.sqlite`.
func Open(sqlitePath string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", filepath.ToSlash(sqlitePath))
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1) // serialize writes; SQLite is single-writer anyway
	sqlDB.SetMaxIdleConns(1)
	d := &DB{DB: sqlDB, path: sqlitePath}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Path() string {
	return d.path
}

// migrate applies every required schema definition. Idempotent.
func (d *DB) migrate() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, stmt := range schemaStatements {
		if _, err := d.DB.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w (stmt: %s)", err, stmt)
		}
	}
	return nil
}

var schemaStatements = []string{
	// Jobs queue. id is a ULID-ish string. Workers consume by status.
	// internal/search keeps its own DB file (noto.sqlite); this jobs DB
	// lives at noto-jobs.sqlite so writes never contend.
	`CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		meeting_id TEXT,
		status TEXT NOT NULL,
		phase TEXT,
		progress REAL DEFAULT 0,
		detail TEXT,
		error TEXT,
		options_json TEXT,
		created_at INTEGER NOT NULL,
		started_at INTEGER,
		finished_at INTEGER,
		attempt INTEGER DEFAULT 0,
		cancel_requested INTEGER DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs(status)`,
	`CREATE INDEX IF NOT EXISTS jobs_meeting_idx ON jobs(meeting_id)`,
	`CREATE INDEX IF NOT EXISTS jobs_created_at_idx ON jobs(created_at)`,
}

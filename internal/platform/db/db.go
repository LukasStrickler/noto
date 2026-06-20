// Package db is the single place that opens and tunes noto's SQLite
// connections. Open returns a pragma-tuned, write-serialized handle with no
// schema applied; each schema owner (the jobs queue here, the search index,
// the speaker store) applies its own DDL via Migrate. Centralizing Open means
// one definition of the DSN/pragmas/connection limits instead of a copy per
// caller, and lets the composition root share one handle to noto.sqlite across
// the search index and speaker store rather than opening it twice.
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

// Open opens a pragma-tuned SQLite connection at sqlitePath. It applies no
// schema — call Migrate with the appropriate DDL afterwards. The connection is
// limited to a single open connection because SQLite is single-writer anyway,
// which keeps WAL semantics coherent across all callers of this one handle.
func Open(sqlitePath string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", filepath.ToSlash(sqlitePath))
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1) // serialize writes; SQLite is single-writer anyway
	sqlDB.SetMaxIdleConns(1)
	return &DB{DB: sqlDB, path: sqlitePath}, nil
}

func (d *DB) Path() string {
	return d.path
}

// Migrate applies the given schema statements in order. Idempotent when the
// statements are (e.g. CREATE TABLE IF NOT EXISTS). Safe to call from multiple
// schema owners sharing one handle.
func (d *DB) Migrate(stmts []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, stmt := range stmts {
		if _, err := d.DB.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w (stmt: %s)", err, stmt)
		}
	}
	return nil
}

// JobsSchema is the DDL for the jobs queue (lives at noto-jobs.sqlite, a
// separate file from noto.sqlite so the write-heavy queue never contends with
// the search index / speaker store).
var JobsSchema = []string{
	// Jobs queue. id is a ULID-ish string. Workers consume by status, claiming
	// the lowest-priority-value queued row first (see notoapi.JobPriority).
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
		cancel_requested INTEGER DEFAULT 0,
		priority INTEGER NOT NULL DEFAULT 50
	)`,
	`CREATE INDEX IF NOT EXISTS jobs_status_idx ON jobs(status)`,
	`CREATE INDEX IF NOT EXISTS jobs_meeting_idx ON jobs(meeting_id)`,
	`CREATE INDEX IF NOT EXISTS jobs_created_at_idx ON jobs(created_at)`,
}

// JobsColumnMigrations are additive columns applied to an EXISTING jobs table on
// upgrade (a fresh DB already has them from JobsSchema's CREATE TABLE). Run via
// AddColumnIfMissing after Migrate(JobsSchema). SQLite has no ALTER ... ADD COLUMN
// IF NOT EXISTS, so the helper guards on PRAGMA table_info rather than erroring.
var JobsColumnMigrations = []ColumnSpec{
	{Table: "jobs", Column: "priority", DDL: "INTEGER NOT NULL DEFAULT 50"},
}

// ColumnSpec describes one additive column for AddColumnIfMissing.
type ColumnSpec struct {
	Table  string
	Column string
	DDL    string // the type/constraint clause, e.g. "INTEGER NOT NULL DEFAULT 50"
}

// AddColumnIfMissing applies each spec's column only when the table doesn't
// already have it, making additive migrations idempotent across restarts (SQLite
// can't express ALTER TABLE ADD COLUMN IF NOT EXISTS). A fresh table created by a
// CREATE TABLE that already lists the column is a no-op.
func (d *DB) AddColumnIfMissing(specs []ColumnSpec) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, s := range specs {
		has, err := d.hasColumn(s.Table, s.Column)
		if err != nil {
			return fmt.Errorf("add column %s.%s: %w", s.Table, s.Column, err)
		}
		if has {
			continue
		}
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", s.Table, s.Column, s.DDL)
		if _, err := d.DB.Exec(stmt); err != nil {
			return fmt.Errorf("add column %s.%s: %w (stmt: %s)", s.Table, s.Column, err, stmt)
		}
	}
	return nil
}

// hasColumn reports whether table already has column, via PRAGMA table_info.
func (d *DB) hasColumn(table, column string) (bool, error) {
	rows, err := d.DB.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid        int
			name       string
			ctype      string
			notnull    int
			dfltValue  sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

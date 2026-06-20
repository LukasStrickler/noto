package db

import (
	"path/filepath"
	"testing"
)

// TestAddColumnIfMissingUpgradesExistingTable simulates an OLD jobs DB (created
// before the priority column existed): AddColumnIfMissing must add the column,
// and a second call must be a no-op (idempotent across restarts).
func TestAddColumnIfMissingUpgradesExistingTable(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "jobs.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	// An old-schema jobs table WITHOUT priority.
	if _, err := d.Exec(`CREATE TABLE jobs (id TEXT PRIMARY KEY, status TEXT)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO jobs (id, status) VALUES ('j1', 'queued')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if has, err := d.hasColumn("jobs", "priority"); err != nil || has {
		t.Fatalf("priority should be absent on old table: has=%v err=%v", has, err)
	}

	if err := d.AddColumnIfMissing(JobsColumnMigrations); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	has, err := d.hasColumn("jobs", "priority")
	if err != nil || !has {
		t.Fatalf("priority should exist after migrate: has=%v err=%v", has, err)
	}
	// The existing row gets the DEFAULT.
	var prio int
	if err := d.QueryRow(`SELECT priority FROM jobs WHERE id = 'j1'`).Scan(&prio); err != nil {
		t.Fatalf("read priority: %v", err)
	}
	if prio != 50 {
		t.Errorf("legacy row priority = %d; want default 50", prio)
	}

	// Idempotent: a second call must not error (no ALTER twice).
	if err := d.AddColumnIfMissing(JobsColumnMigrations); err != nil {
		t.Fatalf("second migrate (should be no-op): %v", err)
	}
}

// TestFreshJobsSchemaHasPriority asserts a brand-new DB created from JobsSchema
// already carries the column, so AddColumnIfMissing is only needed on upgrade.
func TestFreshJobsSchemaHasPriority(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "jobs.sqlite"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if err := d.Migrate(JobsSchema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if has, err := d.hasColumn("jobs", "priority"); err != nil || !has {
		t.Fatalf("fresh schema must include priority: has=%v err=%v", has, err)
	}
}

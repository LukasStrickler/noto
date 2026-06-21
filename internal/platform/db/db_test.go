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

	// cancel_requested predates this migration system but is now read at startup
	// (recoverInterruptedJobs), so a pre-column DB must get it back-filled too —
	// else the service would crash on boot. The legacy row defaults to 0.
	if has, err := d.hasColumn("jobs", "cancel_requested"); err != nil || !has {
		t.Fatalf("cancel_requested should exist after migrate: has=%v err=%v", has, err)
	}
	var canceled int
	if err := d.QueryRow(`SELECT cancel_requested FROM jobs WHERE id = 'j1'`).Scan(&canceled); err != nil {
		t.Fatalf("read cancel_requested: %v", err)
	}
	if canceled != 0 {
		t.Errorf("legacy row cancel_requested = %d; want default 0", canceled)
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

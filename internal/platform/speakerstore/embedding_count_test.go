package speakerstore

import (
	"context"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/db"
)

// TestSpeakerProfile_EmbeddingCountRoundTrip pins the new enrollment-count column
// through Create/Get/Update, plus the >=1 clamp for unset values.
func TestSpeakerProfile_EmbeddingCountRoundTrip(t *testing.T) {
	d, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repo := NewSQLiteSpeakerProfileRepository(d)
	ctx := context.Background()

	now := time.Now()
	p := SpeakerProfile{
		ID: "p1", DisplayName: "Alice",
		EmbeddingVector: []float64{1, 0}, EmbeddingDim: 2, EmbeddingCount: 5,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EmbeddingCount != 5 {
		t.Errorf("EmbeddingCount = %d; want 5", got.EmbeddingCount)
	}

	got.EmbeddingCount = 6
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if again, _ := repo.Get(ctx, "p1"); again.EmbeddingCount != 6 {
		t.Errorf("after Update EmbeddingCount = %d; want 6", again.EmbeddingCount)
	}

	// An unset count is clamped to 1 (a profile always has its initial enrollment).
	zero := SpeakerProfile{ID: "p2", DisplayName: "Bob", EmbeddingVector: []float64{0, 1}, EmbeddingDim: 2, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, zero); err != nil {
		t.Fatalf("Create p2: %v", err)
	}
	if got2, _ := repo.Get(ctx, "p2"); got2.EmbeddingCount != 1 {
		t.Errorf("unset EmbeddingCount stored as %d; want clamp to 1", got2.EmbeddingCount)
	}
}

// TestMigrateAddsEmbeddingCount simulates an OLD speaker_profiles table (created
// before the column) and asserts Migrate adds embedding_count, defaulting an
// existing row to 1.
func TestMigrateAddsEmbeddingCount(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/old.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// Minimal pre-column schema with one row.
	if _, err := d.Exec(`CREATE TABLE speaker_profiles (
		id TEXT PRIMARY KEY, display_name TEXT NOT NULL,
		email TEXT NOT NULL DEFAULT '', pronouns TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '', affiliations TEXT NOT NULL DEFAULT '[]',
		embedding_vector BLOB, embedding_dim INTEGER NOT NULL DEFAULT 0,
		embedding_model TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, last_seen_at INTEGER)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO speaker_profiles (id, display_name, created_at, updated_at) VALUES ('old1','Carol',0,0)`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Migrate(d); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var count int
	if err := d.QueryRow(`SELECT embedding_count FROM speaker_profiles WHERE id='old1'`).Scan(&count); err != nil {
		t.Fatalf("read embedding_count after migrate: %v", err)
	}
	if count != 1 {
		t.Errorf("legacy row embedding_count = %d; want default 1", count)
	}
	// Idempotent: a second Migrate must not error on the already-present column.
	if err := Migrate(d); err != nil {
		t.Fatalf("second Migrate (idempotent): %v", err)
	}
}

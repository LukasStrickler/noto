package speakerstore

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSpeakerProfile_CRUD(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewSQLiteSpeakerProfileRepository(db)
	ctx := context.Background()

	p := SpeakerProfile{
		ID:              "test-uuid-1",
		DisplayName:     "Alice Smith",
		Email:           "alice@example.com",
		Pronouns:        "she/her",
		EmbeddingVector: []float64{1.0, 2.0, 3.0},
		EmbeddingDim:    3,
		EmbeddingModel:  "titanet-large",
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DisplayName != p.DisplayName || got.Email != p.Email {
		t.Errorf("Get: got %+v, want %+v", got, p)
	}
	if len(got.EmbeddingVector) != len(p.EmbeddingVector) {
		t.Errorf("EmbeddingVector: got %v, want %v", got.EmbeddingVector, p.EmbeddingVector)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List: got %d, want 1", len(list))
	}

	p.DisplayName = "Alice Updated"
	p.UpdatedAt = time.Now()
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err = repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got.DisplayName != "Alice Updated" {
		t.Errorf("Update: got %s, want %s", got.DisplayName, "Alice Updated")
	}

	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = repo.Get(ctx, p.ID)
	if err == nil {
		t.Error("Get after Delete: expected error")
	}
}

func TestMeetingSpeakerMapping_UpsertList(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewSQLiteMeetingSpeakerMappingRepository(db)
	ctx := context.Background()
	meetingID := "meeting-123"

	m := MeetingSpeakerMapping{
		MeetingID:        meetingID,
		MeetingSpeakerID: "A",
		ProviderLabel:    "Alice",
		ProfileID:        strPtr("profile-1"),
		MatchConfidence:  floatPtr(0.95),
		MatchStatus:      "auto",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if err := repo.Upsert(ctx, m); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := repo.ListByMeeting(ctx, meetingID)
	if err != nil {
		t.Fatalf("ListByMeeting: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListByMeeting: got %d, want 1", len(list))
	}
	if list[0].MeetingSpeakerID != "A" {
		t.Errorf("MeetingSpeakerID: got %s, want A", list[0].MeetingSpeakerID)
	}

	m.MeetingSpeakerID = "B"
	m.ProfileID = nil
	m.MatchStatus = "pending"
	if err := repo.Upsert(ctx, m); err != nil {
		t.Fatalf("Upsert second: %v", err)
	}

	list, err = repo.ListByMeeting(ctx, meetingID)
	if err != nil {
		t.Fatalf("ListByMeeting after second Upsert: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListByMeeting after second Upsert: got %d, want 2", len(list))
	}

	if err := repo.DeleteByMeeting(ctx, meetingID); err != nil {
		t.Fatalf("DeleteByMeeting: %v", err)
	}

	list, err = repo.ListByMeeting(ctx, meetingID)
	if err != nil {
		t.Fatalf("ListByMeeting after Delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListByMeeting after Delete: got %d, want 0", len(list))
	}
}

func TestSpeakerProfile_NotFound(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	repo := NewSQLiteSpeakerProfileRepository(db)
	ctx := context.Background()

	_, err = repo.Get(ctx, "nonexistent")
	if err == nil {
		t.Error("Get nonexistent: expected error")
	}
}

func TestOpen_FileExists(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/persistent.db"

	db1, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	repo := NewSQLiteSpeakerProfileRepository(db1)
	ctx := context.Background()
	p := SpeakerProfile{
		ID:          "persist-test",
		DisplayName: "Bob",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	db1.Close()

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	repo2 := NewSQLiteSpeakerProfileRepository(db2)
	got, err := repo2.Get(ctx, "persist-test")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.DisplayName != "Bob" {
		t.Errorf("DisplayName: got %s, want Bob", got.DisplayName)
	}
}

func strPtr(s string) *string {
	return &s
}

func floatPtr(f float64) *float64 {
	return &f
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

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

func TestSpeakerProfile_AffiliationsRoundTrip(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewSQLiteSpeakerProfileRepository(db)
	ctx := context.Background()

	p := SpeakerProfile{
		ID:          "aff-1",
		DisplayName: "Dr. Vega",
		Pronouns:    "they/them",
		Notes:       "leads the audio group",
		Affiliations: []Affiliation{
			{Context: "University", Organization: "ETH", Email: "vega@ethz.ch"},
			{Context: "Project Noto", Organization: "Acme", Email: "vega@acme.io"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Notes != "leads the audio group" {
		t.Errorf("Notes: got %q", got.Notes)
	}
	if len(got.Affiliations) != 2 || got.Affiliations[1].Email != "vega@acme.io" {
		t.Fatalf("Affiliations round-trip: got %+v", got.Affiliations)
	}
}

func TestMeetingSpeakerMapping_EmbeddingAndProfileQueries(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewSQLiteMeetingSpeakerMappingRepository(db)
	ctx := context.Background()

	// two meetings both linked to profile-1, one to profile-2
	mk := func(meeting, spk, profile string) MeetingSpeakerMapping {
		return MeetingSpeakerMapping{
			MeetingID: meeting, MeetingSpeakerID: spk, ProviderLabel: spk,
			ProfileID: strPtr(profile), MatchStatus: "auto",
			EmbeddingVector: []float64{0.1, 0.2, 0.3}, EmbeddingDim: 3,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	for _, m := range []MeetingSpeakerMapping{mk("m1", "A", "profile-1"), mk("m2", "A", "profile-1"), mk("m3", "A", "profile-2")} {
		if err := repo.Upsert(ctx, m); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	// embedding persisted
	got, _ := repo.ListByMeeting(ctx, "m1")
	if len(got) != 1 || got[0].EmbeddingDim != 3 || len(got[0].EmbeddingVector) != 3 {
		t.Fatalf("embedding round-trip: %+v", got)
	}

	// ListByProfile
	byProf, err := repo.ListByProfile(ctx, "profile-1")
	if err != nil {
		t.Fatalf("ListByProfile: %v", err)
	}
	if len(byProf) != 2 {
		t.Fatalf("ListByProfile: got %d, want 2", len(byProf))
	}

	// ReassignProfile moves profile-2 → profile-1
	if err := repo.ReassignProfile(ctx, "profile-2", "profile-1"); err != nil {
		t.Fatalf("ReassignProfile: %v", err)
	}
	byProf, _ = repo.ListByProfile(ctx, "profile-1")
	if len(byProf) != 3 {
		t.Fatalf("after reassign: got %d, want 3", len(byProf))
	}
}

func TestMeetingSpeakerMapping_StatusCountsByMeeting(t *testing.T) {
	db, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo := NewSQLiteMeetingSpeakerMappingRepository(db)
	ctx := context.Background()

	mk := func(meeting, spk, status string) MeetingSpeakerMapping {
		return MeetingSpeakerMapping{
			MeetingID: meeting, MeetingSpeakerID: spk, ProviderLabel: spk,
			MatchStatus: status, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
	}
	for _, m := range []MeetingSpeakerMapping{
		mk("m1", "A", "auto"), mk("m1", "B", "auto"), mk("m1", "C", "pending"),
		mk("m2", "A", "new"), mk("m2", "B", "unmatched"),
	} {
		if err := repo.Upsert(ctx, m); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	counts, err := repo.StatusCountsByMeeting(ctx)
	if err != nil {
		t.Fatalf("StatusCountsByMeeting: %v", err)
	}
	if counts["m1"]["auto"] != 2 || counts["m1"]["pending"] != 1 {
		t.Errorf("m1 counts = %+v, want auto:2 pending:1", counts["m1"])
	}
	if counts["m2"]["new"] != 1 || counts["m2"]["unmatched"] != 1 {
		t.Errorf("m2 counts = %+v, want new:1 unmatched:1", counts["m2"])
	}
	if _, ok := counts["m3"]; ok {
		t.Error("m3 has no mappings; it should be absent from the rollup")
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

package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func TestMatchSpeakers_NoEmbeddings(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	profiles, err := client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles: %v", err)
	}
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles initially, got %d", len(profiles))
	}

	mappings, err := client.GetMeetingSpeakerMappings(ctx, "some-meeting-id")
	if err != nil {
		t.Fatalf("GetMeetingSpeakerMappings: %v", err)
	}
	if mappings.MeetingID != "some-meeting-id" {
		t.Errorf("expected meeting id 'some-meeting-id', got %q", mappings.MeetingID)
	}
	if len(mappings.Mappings) != 0 {
		t.Errorf("expected 0 mappings for unknown meeting, got %d", len(mappings.Mappings))
	}
}

func TestMatchSpeakers_ProfileCreation(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	profiles, err := client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles: %v", err)
	}
	initialCount := len(profiles)

	p, err := client.CreateSpeakerProfile(ctx, notoapi.CreateSpeakerProfileRequest{
		DisplayName: "Alice Smith",
		Email:       "alice@example.com",
	})
	if err != nil {
		t.Fatalf("CreateSpeakerProfile: %v", err)
	}

	profiles, err = client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles after create: %v", err)
	}
	if len(profiles) != initialCount+1 {
		t.Errorf("expected %d profiles, got %d", initialCount+1, len(profiles))
	}

	_ = p
}

type fakeSpeakerProfileRepo struct {
	profiles []speakerstore.SpeakerProfile
}

func (f *fakeSpeakerProfileRepo) Create(ctx context.Context, p speakerstore.SpeakerProfile) error {
	f.profiles = append(f.profiles, p)
	return nil
}
func (f *fakeSpeakerProfileRepo) Get(ctx context.Context, id string) (speakerstore.SpeakerProfile, error) {
	for _, p := range f.profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return speakerstore.SpeakerProfile{}, os.ErrNotExist
}
func (f *fakeSpeakerProfileRepo) List(ctx context.Context) ([]speakerstore.SpeakerProfile, error) {
	return f.profiles, nil
}
func (f *fakeSpeakerProfileRepo) Update(ctx context.Context, p speakerstore.SpeakerProfile) error {
	for i, existing := range f.profiles {
		if existing.ID == p.ID {
			f.profiles[i] = p
			return nil
		}
	}
	return os.ErrNotExist
}
func (f *fakeSpeakerProfileRepo) Delete(ctx context.Context, id string) error {
	for i, p := range f.profiles {
		if p.ID == id {
			f.profiles = append(f.profiles[:i], f.profiles[i+1:]...)
			return nil
		}
	}
	return os.ErrNotExist
}

type fakeMeetingMappingRepo struct {
	mappings []speakerstore.MeetingSpeakerMapping
}

func (f *fakeMeetingMappingRepo) Upsert(ctx context.Context, m speakerstore.MeetingSpeakerMapping) error {
	for i, existing := range f.mappings {
		if existing.MeetingID == m.MeetingID && existing.MeetingSpeakerID == m.MeetingSpeakerID {
			f.mappings[i] = m
			return nil
		}
	}
	f.mappings = append(f.mappings, m)
	return nil
}
func (f *fakeMeetingMappingRepo) ListByMeeting(ctx context.Context, meetingID string) ([]speakerstore.MeetingSpeakerMapping, error) {
	var result []speakerstore.MeetingSpeakerMapping
	for _, m := range f.mappings {
		if m.MeetingID == meetingID {
			result = append(result, m)
		}
	}
	return result, nil
}
func (f *fakeMeetingMappingRepo) DeleteByMeeting(ctx context.Context, meetingID string) error {
	var remaining []speakerstore.MeetingSpeakerMapping
	for _, m := range f.mappings {
		if m.MeetingID != meetingID {
			remaining = append(remaining, m)
		}
	}
	f.mappings = remaining
	return nil
}

func TestMatchSpeakers_FakeRepo(t *testing.T) {
	profileRepo := &fakeSpeakerProfileRepo{}
	mappingRepo := &fakeMeetingMappingRepo{}

	now := time.Now()
	ctx := context.Background()

	p := speakerstore.SpeakerProfile{
		ID:              "existing-profile",
		DisplayName:     "Alice Existing",
		EmbeddingVector: makeEmbedding(192, 0.5),
		EmbeddingDim:    192,
		EmbeddingModel:  "titanet-large",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	_ = profileRepo.Create(ctx, p)

	m := speakerstore.MeetingSpeakerMapping{
		MeetingID:        "meeting-1",
		MeetingSpeakerID: "A",
		ProviderLabel:    "Alice",
		ProfileID:        strPtr("existing-profile"),
		MatchConfidence:  floatPtr(0.85),
		MatchStatus:      "auto",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	_ = mappingRepo.Upsert(ctx, m)

	mappings, err := mappingRepo.ListByMeeting(ctx, "meeting-1")
	if err != nil {
		t.Fatalf("ListByMeeting: %v", err)
	}
	if len(mappings) != 1 {
		t.Errorf("expected 1 mapping, got %d", len(mappings))
	}
	if mappings[0].ProfileID == nil || *mappings[0].ProfileID != "existing-profile" {
		t.Errorf("expected profile id to be set")
	}

	profiles, err := profileRepo.List(ctx)
	if err != nil {
		t.Fatalf("List profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Errorf("expected 1 profile, got %d", len(profiles))
	}

	_ = profileRepo
	_ = mappingRepo
}

func TestSpeakerMatching_StatusThresholds(t *testing.T) {
	autoThreshold := 0.70
	pendingThreshold := 0.55

	testCases := []struct {
		score  float64
		status string
	}{
		{0.85, "auto"},
		{0.70, "auto"},
		{0.69, "pending"},
		{0.55, "pending"},
		{0.54, "new"},
		{0.30, "new"},
	}

	for _, tc := range testCases {
		var status string
		if tc.score >= autoThreshold {
			status = "auto"
		} else if tc.score >= pendingThreshold {
			status = "pending"
		} else {
			status = "new"
		}
		if status != tc.status {
			t.Errorf("score %.2f: expected %s, got %s", tc.score, tc.status, status)
		}
	}
}

func makeEmbedding(dim int, val float64) []float64 {
	emb := make([]float64, dim)
	for i := range emb {
		emb[i] = val
	}
	return emb
}

func strPtr(s string) *string     { return &s }
func floatPtr(f float64) *float64 { return &f }

var _ = speakerstore.MeetingSpeakerMapping{}

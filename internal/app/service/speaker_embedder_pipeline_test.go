package service

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/platform/secrets"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/platform/storage"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

type memorySpeakerProfileRepo struct {
	// mu makes each method atomic on its own — modelling the real SQLite store,
	// whose SetMaxOpenConns(1) serializes individual statements. It deliberately
	// does NOT span a Get→Update pair: that cross-call atomicity is the service's
	// job (Service.profileLocks), and leaving it unguarded here is what lets
	// TestFoldEmbeddingIntoProfile_ConcurrentNoLostUpdate observe a lost fold if
	// the service ever drops its lock.
	mu       sync.Mutex
	profiles []speakerstore.SpeakerProfile
}

func (r *memorySpeakerProfileRepo) Create(ctx context.Context, p speakerstore.SpeakerProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.profiles = append(r.profiles, p)
	return nil
}

func (r *memorySpeakerProfileRepo) Get(ctx context.Context, id string) (speakerstore.SpeakerProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return speakerstore.SpeakerProfile{}, os.ErrNotExist
}

func (r *memorySpeakerProfileRepo) List(ctx context.Context) ([]speakerstore.SpeakerProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]speakerstore.SpeakerProfile(nil), r.profiles...), nil
}

func (r *memorySpeakerProfileRepo) Update(ctx context.Context, p speakerstore.SpeakerProfile) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.profiles {
		if r.profiles[i].ID == p.ID {
			r.profiles[i] = p
			return nil
		}
	}
	return os.ErrNotExist
}

func (r *memorySpeakerProfileRepo) Delete(ctx context.Context, id string) error { return nil }

type memoryMeetingMappingRepo struct {
	mappings []speakerstore.MeetingSpeakerMapping
}

func (r *memoryMeetingMappingRepo) Upsert(ctx context.Context, m speakerstore.MeetingSpeakerMapping) error {
	// Replace-by-key, mirroring the real store's PK (meeting_id, meeting_speaker_id)
	// ON CONFLICT DO UPDATE — so a re-upsert updates the row in place, not appends.
	for i := range r.mappings {
		if r.mappings[i].MeetingID == m.MeetingID && r.mappings[i].MeetingSpeakerID == m.MeetingSpeakerID {
			r.mappings[i] = m
			return nil
		}
	}
	r.mappings = append(r.mappings, m)
	return nil
}

func (r *memoryMeetingMappingRepo) ListByMeeting(ctx context.Context, meetingID string) ([]speakerstore.MeetingSpeakerMapping, error) {
	var out []speakerstore.MeetingSpeakerMapping
	for _, m := range r.mappings {
		if m.MeetingID == meetingID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *memoryMeetingMappingRepo) ListByProfile(ctx context.Context, profileID string) ([]speakerstore.MeetingSpeakerMapping, error) {
	var out []speakerstore.MeetingSpeakerMapping
	for _, m := range r.mappings {
		if m.ProfileID != nil && *m.ProfileID == profileID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *memoryMeetingMappingRepo) ReassignProfile(ctx context.Context, oldID, newID string) error {
	for i := range r.mappings {
		if r.mappings[i].ProfileID != nil && *r.mappings[i].ProfileID == oldID {
			r.mappings[i].ProfileID = &newID
		}
	}
	return nil
}

func (r *memoryMeetingMappingRepo) DeleteByMeeting(ctx context.Context, meetingID string) error {
	kept := r.mappings[:0]
	for _, m := range r.mappings {
		if m.MeetingID != meetingID {
			kept = append(kept, m)
		}
	}
	r.mappings = kept
	return nil
}

func (r *memoryMeetingMappingRepo) CountUnresolved(ctx context.Context) (int, error) {
	n := 0
	for _, m := range r.mappings {
		if m.MatchStatus != "auto" && m.MatchStatus != "manual" {
			n++
		}
	}
	return n, nil
}

func (r *memoryMeetingMappingRepo) StatusCountsByMeeting(ctx context.Context) (map[string]map[string]int, error) {
	out := map[string]map[string]int{}
	for _, m := range r.mappings {
		if out[m.MeetingID] == nil {
			out[m.MeetingID] = map[string]int{}
		}
		out[m.MeetingID][m.MatchStatus]++
	}
	return out, nil
}

type fakeSTTProvider struct{}

func (fakeSTTProvider) ProviderID() string { return "parakeet-local" }

func (fakeSTTProvider) FeatureMap() stt.ProviderFeatures {
	return stt.ProviderFeatures{ProviderID: "parakeet-local", Features: []stt.Feature{stt.FeatureTranscribe, stt.FeatureSpeakerDiarize}}
}

func (fakeSTTProvider) Transcribe(ctx context.Context, audio []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	return &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     opts.MeetingID,
		Provider:      artifacts.TranscriptProvider{ID: "parakeet-local"},
		Speakers: []artifacts.Speaker{
			{ID: "spk_a", Label: "Speaker A", DisplayName: "Speaker A", ProviderLabel: "A"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_1", SpeakerID: "spk_a", StartSeconds: 0, EndSeconds: 3, Text: "hello from speaker a"},
		},
		Capabilities: artifacts.TranscriptCapabilities{SpeakerDiarization: true},
	}, nil
}

type fakeSpeakerEmbedder struct {
	called bool
}

func (f *fakeSpeakerEmbedder) EmbedSpeakers(ctx context.Context, audio []byte, transcript *artifacts.Transcript) (map[string][]float64, error) {
	f.called = true
	if len(audio) == 0 || transcript == nil || len(transcript.Speakers) == 0 {
		return nil, nil
	}
	return map[string][]float64{"A": testEmbedding(192, 0.5)}, nil
}

func testEmbedding(dim int, val float64) []float64 {
	emb := make([]float64, dim)
	for i := range emb {
		emb[i] = val
	}
	return emb
}

func TestRunTranscribe_UsesSpeakerEmbedderForMappings(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.ConfigDir = t.TempDir()
	cfg.ArtifactRoot = t.TempDir()
	cfg.RecordingsDir = filepath.Join(cfg.ArtifactRoot, "recordings")

	profileRepo := &memorySpeakerProfileRepo{}
	mappingRepo := &memoryMeetingMappingRepo{}
	secretStore := secrets.NewMemoryStore()
	if err := secretStore.Set(ctx, "provider:parakeet-local", "test-key"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	embedder := &fakeSpeakerEmbedder{}
	jobsDB, err := db.Open(filepath.Join(cfg.ConfigDir, "jobs.sqlite"))
	if err != nil {
		t.Fatalf("open jobs db: %v", err)
	}
	if err := jobsDB.Migrate(db.JobsSchema); err != nil {
		t.Fatalf("migrate jobs db: %v", err)
	}
	defer jobsDB.Close()
	svc := New(Deps{
		Config:          cfg,
		Secrets:         secretStore,
		JobsDB:          jobsDB,
		SpeakerProfiles: profileRepo,
		MeetingMappings: mappingRepo,
		SpeakerEmbedder: embedder,
		STTAdapterFactory: func(providerID string) (stt.STTProvider, error) {
			return fakeSTTProvider{}, nil
		},
	})

	meetingID := "11111111-1111-1111-1111-111111111111"
	parsedID := uuid.MustParse(meetingID)
	layout, err := storage.LayoutFor(cfg.RecordingsDir, parsedID)
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := storage.EnsureDirs(layout); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	if err := os.WriteFile(layout.AudioPath, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	job := &notoapi.Job{
		ID:        "job-1",
		Kind:      notoapi.JobTranscribe,
		MeetingID: meetingID,
		Options:   map[string]any{"title": "embedder test", "output_path": layout.AudioPath},
		CreatedAt: time.Now(),
	}
	if err := svc.runTranscribe(ctx, job); err != nil {
		t.Fatalf("runTranscribe: %v", err)
	}
	if !embedder.called {
		t.Fatal("expected speaker embedder to be called")
	}
	mappings, err := mappingRepo.ListByMeeting(ctx, meetingID)
	if err != nil {
		t.Fatalf("ListByMeeting: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(mappings))
	}
	if mappings[0].MatchStatus != "new" {
		t.Fatalf("expected new speaker mapping, got %q", mappings[0].MatchStatus)
	}
	profiles, err := profileRepo.List(ctx)
	if err != nil {
		t.Fatalf("List profiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 speaker profile, got %d", len(profiles))
	}
	if profiles[0].EmbeddingDim != 192 || profiles[0].EmbeddingModel != "ecapa" {
		t.Fatalf("profile embedding not persisted correctly: dim=%d model=%q", profiles[0].EmbeddingDim, profiles[0].EmbeddingModel)
	}
}

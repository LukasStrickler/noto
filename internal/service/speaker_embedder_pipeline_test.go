package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/data"
	"github.com/lukasstrickler/noto/internal/db"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers/stt"
	"github.com/lukasstrickler/noto/internal/secrets"
	"github.com/lukasstrickler/noto/internal/storage"
)

type memorySpeakerProfileRepo struct {
	profiles []data.SpeakerProfile
}

func (r *memorySpeakerProfileRepo) Create(ctx context.Context, p data.SpeakerProfile) error {
	r.profiles = append(r.profiles, p)
	return nil
}

func (r *memorySpeakerProfileRepo) Get(ctx context.Context, id string) (data.SpeakerProfile, error) {
	for _, p := range r.profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return data.SpeakerProfile{}, os.ErrNotExist
}

func (r *memorySpeakerProfileRepo) List(ctx context.Context) ([]data.SpeakerProfile, error) {
	return append([]data.SpeakerProfile(nil), r.profiles...), nil
}

func (r *memorySpeakerProfileRepo) Update(ctx context.Context, p data.SpeakerProfile) error {
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
	mappings []data.MeetingSpeakerMapping
}

func (r *memoryMeetingMappingRepo) Upsert(ctx context.Context, m data.MeetingSpeakerMapping) error {
	r.mappings = append(r.mappings, m)
	return nil
}

func (r *memoryMeetingMappingRepo) ListByMeeting(ctx context.Context, meetingID string) ([]data.MeetingSpeakerMapping, error) {
	var out []data.MeetingSpeakerMapping
	for _, m := range r.mappings {
		if m.MeetingID == meetingID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *memoryMeetingMappingRepo) DeleteByMeeting(ctx context.Context, meetingID string) error {
	return nil
}

type fakeSTTProvider struct{}

func (fakeSTTProvider) ProviderID() string { return "assemblyai" }

func (fakeSTTProvider) FeatureMap() stt.ProviderFeatures {
	return stt.ProviderFeatures{ProviderID: "assemblyai", Features: []stt.Feature{stt.FeatureTranscribe, stt.FeatureSpeakerDiarize}}
}

func (fakeSTTProvider) Transcribe(ctx context.Context, audio []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	return &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     opts.MeetingID,
		Provider:      artifacts.TranscriptProvider{ID: "assemblyai"},
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
	if err := secretStore.Set(ctx, "provider:assemblyai", "test-key"); err != nil {
		t.Fatalf("set key: %v", err)
	}
	embedder := &fakeSpeakerEmbedder{}
	jobsDB, err := db.Open(filepath.Join(cfg.ConfigDir, "jobs.sqlite"))
	if err != nil {
		t.Fatalf("open jobs db: %v", err)
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
	if profiles[0].EmbeddingDim != 192 || profiles[0].EmbeddingModel != "titanet-large" {
		t.Fatalf("profile embedding not persisted correctly: dim=%d model=%q", profiles[0].EmbeddingDim, profiles[0].EmbeddingModel)
	}
}

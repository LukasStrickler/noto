package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// fakeSTTWithWords returns a word-level transcript with every word initially on
// ONE speaker — exactly what a local STT (no diarization) produces.
type fakeSTTWithWords struct{}

func (fakeSTTWithWords) ProviderID() string { return "parakeet-local" }
func (fakeSTTWithWords) FeatureMap() stt.ProviderFeatures {
	return stt.ProviderFeatures{ProviderID: "parakeet-local", IsLocal: true,
		Features: []stt.Feature{stt.FeatureTranscribe, stt.FeatureWordTimestamps}}
}
func (fakeSTTWithWords) Transcribe(_ context.Context, _ []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	return &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     opts.MeetingID,
		Provider:      artifacts.TranscriptProvider{ID: "parakeet-local"},
		Speakers:      []artifacts.Speaker{{ID: "spk_0", Label: "spk_0", Origin: "participants", ProviderLabel: "0", DisplayName: "Voice 1"}},
		Segments:      []artifacts.Segment{{ID: "seg_1", SpeakerID: "spk_0", StartSeconds: 0, EndSeconds: 4, Text: "hello there yes indeed"}},
		Words: []artifacts.Word{
			{ID: "w1", SegmentID: "seg_1", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 0.5, Text: "hello"},
			{ID: "w2", SegmentID: "seg_1", SpeakerID: "spk_0", StartSeconds: 1.0, EndSeconds: 1.5, Text: "there"},
			{ID: "w3", SegmentID: "seg_1", SpeakerID: "spk_0", StartSeconds: 2.5, EndSeconds: 3.0, Text: "yes"},
			{ID: "w4", SegmentID: "seg_1", SpeakerID: "spk_0", StartSeconds: 3.4, EndSeconds: 3.9, Text: "indeed"},
		},
	}, nil
}

// fakeTwoTurnDiarizer splits the timeline into two speakers at t=2s.
type fakeTwoTurnDiarizer struct{ calls int }

func (f *fakeTwoTurnDiarizer) ProviderID() string { return "fake-diar" }
func (f *fakeTwoTurnDiarizer) Diarize(_ context.Context, _ []byte, _ diarize.DiarizeOptions) ([]diarize.Turn, error) {
	f.calls++
	return []diarize.Turn{
		{Speaker: "0", StartSeconds: 0, EndSeconds: 2},
		{Speaker: "1", StartSeconds: 2, EndSeconds: 4},
	}, nil
}

// fakeNoTurnDiarizer runs cleanly but finds no speaker turns — the short/quiet
// audio (or diarizer-hiccup) case.
type fakeNoTurnDiarizer struct{ calls int }

func (f *fakeNoTurnDiarizer) ProviderID() string { return "fake-diar-empty" }
func (f *fakeNoTurnDiarizer) Diarize(_ context.Context, _ []byte, _ diarize.DiarizeOptions) ([]diarize.Turn, error) {
	f.calls++
	return nil, nil // ran, no error, zero turns
}

// TestPipelineEmptyDiarizationKeepsAttribution pins that a diarizer returning ZERO
// turns (no error) must NOT wipe the transcript's existing speakers. Attributing
// against no turns would empty the Speakers list and unlabel every word — a strict
// loss of the STT's own attribution and nothing left for identification to match.
func TestPipelineEmptyDiarizationKeepsAttribution(t *testing.T) {
	dir := t.TempDir()
	jobsDB, err := db.Open(filepath.Join(dir, "jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := jobsDB.Migrate(db.JobsSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jobsDB.Close() })

	r := repo.NewLocal(filepath.Join(dir, "recordings"))
	ctx := context.Background()
	mid := uuid.New()
	if err := r.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{Title: "empty diar test"}); err != nil {
		t.Fatal(err)
	}
	audioPath, err := r.PrepareAudio(ctx, mid, ".m4a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audioPath, []byte("fake audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	diar := &fakeNoTurnDiarizer{}
	svc := New(Deps{
		Config:            config.Config{ConfigDir: dir, ArtifactRoot: dir},
		Registry:          providers.DefaultRegistry(),
		JobsDB:            jobsDB,
		Repo:              r,
		Diarizer:          diar,
		STTAdapterFactory: func(string) (stt.STTProvider, error) { return fakeSTTWithWords{}, nil },
		Version:           "test",
	})

	job := &notoapi.Job{ID: "j1", Kind: notoapi.JobTranscribe, MeetingID: mid.String(),
		Options: map[string]any{"output_path": audioPath}}
	if err := svc.runTranscribe(ctx, job); err != nil {
		t.Fatalf("runTranscribe: %v", err)
	}
	if diar.calls != 1 {
		t.Fatalf("diarizer called %d times, want 1", diar.calls)
	}

	tr, err := r.LoadTranscript(ctx, mid)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	// The STT produced one speaker over four words; empty diarization must leave
	// that intact rather than emptying it.
	if len(tr.Speakers) == 0 {
		t.Fatalf("empty diarization wiped the speaker list; want the STT's speaker preserved")
	}
	for _, w := range tr.Words {
		if w.SpeakerID == "" {
			t.Fatalf("word %q was unlabeled by empty-turn attribution; want its STT speaker kept", w.Text)
		}
	}
}

func TestPipelineDiarizesAndAttributes(t *testing.T) {
	dir := t.TempDir()
	jobsDB, err := db.Open(filepath.Join(dir, "jobs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := jobsDB.Migrate(db.JobsSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jobsDB.Close() })

	r := repo.NewLocal(filepath.Join(dir, "recordings"))
	ctx := context.Background()
	mid := uuid.New()
	if err := r.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{Title: "diar test"}); err != nil {
		t.Fatal(err)
	}
	audioPath, err := r.PrepareAudio(ctx, mid, ".m4a")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(audioPath, []byte("fake audio bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	diar := &fakeTwoTurnDiarizer{}
	svc := New(Deps{
		Config:            config.Config{ConfigDir: dir, ArtifactRoot: dir},
		Registry:          providers.DefaultRegistry(),
		JobsDB:            jobsDB,
		Repo:              r,
		Diarizer:          diar,
		STTAdapterFactory: func(string) (stt.STTProvider, error) { return fakeSTTWithWords{}, nil },
		Version:           "test",
	})

	job := &notoapi.Job{ID: "j1", Kind: notoapi.JobTranscribe, MeetingID: mid.String(),
		Options: map[string]any{"output_path": audioPath}}
	if err := svc.runTranscribe(ctx, job); err != nil {
		t.Fatalf("runTranscribe: %v", err)
	}
	if diar.calls != 1 {
		t.Fatalf("diarizer called %d times, want 1", diar.calls)
	}

	tr, err := r.LoadTranscript(ctx, mid)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	// Words pre-diarization were all one speaker; after attribution the two
	// turns yield two distinct speakers.
	if len(tr.Speakers) != 2 {
		t.Fatalf("got %d speakers after diarization, want 2: %+v", len(tr.Speakers), tr.Speakers)
	}
	// Words before t=2 → speaker "0"; after → speaker "1".
	got := map[string]string{}
	for _, w := range tr.Words {
		got[w.Text] = w.SpeakerID
	}
	if got["hello"] == got["yes"] {
		t.Errorf("expected early/late words on different speakers, both = %q", got["hello"])
	}
}

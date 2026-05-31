// Package testutil provides test helpers shared across packages.
// Nothing here is for production use.
package testutil

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/repo"
)

// FakeRepo is an in-memory ArtifactRepository for unit tests.
// The zero value is NOT ready; use NewFakeRepo().
type FakeRepo struct {
	mu          sync.RWMutex
	meetings    map[uuid.UUID]*repo.StoredMeeting
	transcripts map[uuid.UUID]*artifacts.Transcript
	summaryMDs  map[uuid.UUID]string
	summaries   map[uuid.UUID]*artifacts.Summary
	audioPaths  map[uuid.UUID]string
	audioDir    string // temp dir for PrepareAudio

	// VerifyErr controls the result of VerifyIntegrity per meeting.
	// Set to a non-nil error to simulate verification failures.
	VerifyErr map[uuid.UUID]error
}

// NewFakeRepo returns a ready-to-use FakeRepo with a temporary audio
// directory that is cleaned up when the process exits.
func NewFakeRepo() *FakeRepo {
	tmpDir, _ := os.MkdirTemp("", "noto-fake-repo-*")
	return &FakeRepo{
		meetings:    make(map[uuid.UUID]*repo.StoredMeeting),
		transcripts: make(map[uuid.UUID]*artifacts.Transcript),
		summaryMDs:  make(map[uuid.UUID]string),
		summaries:   make(map[uuid.UUID]*artifacts.Summary),
		audioPaths:  make(map[uuid.UUID]string),
		VerifyErr:   make(map[uuid.UUID]error),
		audioDir:    tmpDir,
	}
}

func (f *FakeRepo) CreateMeeting(_ context.Context, id uuid.UUID, opts repo.CreateMeetingOpts) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.meetings[id] = &repo.StoredMeeting{
		ID:               id,
		Title:            opts.Title,
		CurrentVersionID: "ver_test",
		CreatedAt:        time.Now(),
	}
	return nil
}

func (f *FakeRepo) GetMeeting(_ context.Context, id uuid.UUID) (*repo.StoredMeeting, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	m, ok := f.meetings[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (f *FakeRepo) ListMeetings(_ context.Context) ([]*repo.StoredMeeting, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*repo.StoredMeeting, 0, len(f.meetings))
	for _, m := range f.meetings {
		cp := *m
		out = append(out, &cp)
	}
	return out, nil
}

func (f *FakeRepo) CountMeetings(_ context.Context) (int, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.meetings), nil
}

func (f *FakeRepo) DeleteMeeting(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.meetings[id]; !ok {
		return repo.ErrNotFound
	}
	delete(f.meetings, id)
	delete(f.transcripts, id)
	delete(f.summaryMDs, id)
	delete(f.summaries, id)
	delete(f.audioPaths, id)
	return nil
}

func (f *FakeRepo) SaveTranscript(_ context.Context, id uuid.UUID, t *artifacts.Transcript) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transcripts[id] = t
	if m, ok := f.meetings[id]; ok {
		m.HasTranscript = true
		m.SpeakerCount = len(t.Speakers)
		if len(t.Segments) > 0 {
			m.DurationSeconds = int(t.Segments[len(t.Segments)-1].EndSeconds)
		}
	}
	return nil
}

func (f *FakeRepo) LoadTranscript(_ context.Context, id uuid.UUID) (*artifacts.Transcript, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	t, ok := f.transcripts[id]
	if !ok {
		return nil, repo.ErrNotFound
	}
	return t, nil
}

func (f *FakeRepo) SaveSummary(_ context.Context, id uuid.UUID, md string, s *artifacts.Summary) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summaryMDs[id] = md
	f.summaries[id] = s
	if m, ok := f.meetings[id]; ok {
		m.HasSummary = true
		if s != nil {
			m.ShortSummary = s.ShortSummary
			m.DecisionCount = len(s.Decisions)
			m.ActionCount = len(s.ActionItems)
			m.RiskCount = len(s.Risks)
			m.QuestionCount = len(s.OpenQuestions)
		}
	}
	return nil
}

func (f *FakeRepo) LoadSummary(_ context.Context, id uuid.UUID) (string, *artifacts.Summary, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.summaryMDs[id], f.summaries[id], nil
}

func (f *FakeRepo) PrepareAudio(_ context.Context, id uuid.UUID, ext string) (string, error) {
	if ext == "" {
		ext = ".m4a"
	}
	path := filepath.Join(f.audioDir, id.String()+ext)
	f.mu.Lock()
	f.audioPaths[id] = path
	f.mu.Unlock()
	return path, nil
}

func (f *FakeRepo) AudioPath(id uuid.UUID) (string, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.audioPaths[id]
	return p, ok
}

func (f *FakeRepo) VerifyIntegrity(_ context.Context, id uuid.UUID) error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.VerifyErr[id]
}

func (f *FakeRepo) FilePaths(_ uuid.UUID) *repo.MeetingFilePaths {
	return nil
}

// SetAudioPath registers an explicit audio path for a meeting, useful
// when the test has already created the file itself.
func (f *FakeRepo) SetAudioPath(id uuid.UUID, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audioPaths[id] = path
}

// MeetingCount returns the number of meetings currently in the fake.
func (f *FakeRepo) MeetingCount() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.meetings)
}

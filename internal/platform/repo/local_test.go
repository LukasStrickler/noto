package repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
)

func newLocalRepo(t *testing.T) repo.ArtifactRepository {
	t.Helper()
	return repo.NewLocal(t.TempDir())
}

func TestLocalRepo_CreateAndGet(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()

	if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{
		Title:  "Team standup",
		Reason: "recorded",
	}); err != nil {
		t.Fatalf("CreateMeeting: %v", err)
	}

	sm, err := r.GetMeeting(ctx, id)
	if err != nil {
		t.Fatalf("GetMeeting: %v", err)
	}
	if sm.ID != id {
		t.Errorf("ID mismatch: want %s got %s", id, sm.ID)
	}
	if sm.Title != "Team standup" {
		t.Errorf("Title: want %q got %q", "Team standup", sm.Title)
	}
	if sm.CurrentVersionID == "" {
		t.Error("expected a CurrentVersionID")
	}
}

func TestLocalRepo_ListMeetings(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()

	for _, title := range []string{"Alpha", "Beta", "Gamma"} {
		id := uuid.New()
		if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: title}); err != nil {
			t.Fatalf("CreateMeeting %q: %v", title, err)
		}
	}

	meetings, err := r.ListMeetings(ctx)
	if err != nil {
		t.Fatalf("ListMeetings: %v", err)
	}
	if len(meetings) != 3 {
		t.Fatalf("want 3 meetings, got %d", len(meetings))
	}
}

func TestLocalRepo_SaveLoadTranscript(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()

	if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "t"}); err != nil {
		t.Fatal(err)
	}

	conf := 0.95
	tr := &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     id.String(),
		Provider:      artifacts.TranscriptProvider{ID: "parakeet-local"},
		Speakers:      []artifacts.Speaker{{ID: "spk_0", DisplayName: "Alice"}},
		Segments: []artifacts.Segment{
			{ID: "seg_001", SpeakerID: "spk_0", Text: "Hello world", StartSeconds: 0, EndSeconds: 3, Confidence: &conf},
		},
	}
	if err := r.SaveTranscript(ctx, id, tr); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}

	loaded, err := r.LoadTranscript(ctx, id)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(loaded.Segments) != 1 {
		t.Fatalf("want 1 segment, got %d", len(loaded.Segments))
	}
	if loaded.Segments[0].Text != "Hello world" {
		t.Errorf("segment text: want %q got %q", "Hello world", loaded.Segments[0].Text)
	}

	// GetMeeting should now reflect HasTranscript=true.
	sm, _ := r.GetMeeting(ctx, id)
	if !sm.HasTranscript {
		t.Error("expected HasTranscript=true after SaveTranscript")
	}
	if sm.SpeakerCount != 1 {
		t.Errorf("SpeakerCount: want 1 got %d", sm.SpeakerCount)
	}
}

// TestLocalRepo_LoadTranscript_NotFound pins that a not-yet-transcribed meeting
// surfaces the repo-level ErrNotFound sentinel (like GetMeeting), not the raw
// storage code — so mapRepoErr renders a clean "not found" and the remote data
// plane returns 404, not a CodeInternal/500 "internal error".
func TestLocalRepo_LoadTranscript_NotFound(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()
	if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "no transcript yet"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LoadTranscript(ctx, id); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("LoadTranscript on a transcript-less meeting must return repo.ErrNotFound, got %v", err)
	}
}

func TestLocalRepo_SaveLoadSummary(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()

	if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "s"}); err != nil {
		t.Fatal(err)
	}

	summary := &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     id.String(),
		ShortSummary:  "Three decisions made.",
		Decisions:     []artifacts.SummaryItem{{Text: "Ship v1"}},
		ActionItems:   []artifacts.ActionItem{{Text: "Write tests"}},
	}
	if err := r.SaveSummary(ctx, id, "# My meeting\n\nThree decisions made.", summary); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}

	md, loaded, err := r.LoadSummary(ctx, id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if md == "" {
		t.Error("expected non-empty markdown")
	}
	if loaded == nil {
		t.Fatal("expected non-nil summary")
	}
	if loaded.ShortSummary != "Three decisions made." {
		t.Errorf("ShortSummary: want %q got %q", "Three decisions made.", loaded.ShortSummary)
	}

	// GetMeeting should now reflect HasSummary=true + counts.
	sm, _ := r.GetMeeting(ctx, id)
	if !sm.HasSummary {
		t.Error("expected HasSummary=true after SaveSummary")
	}
	if sm.DecisionCount != 1 {
		t.Errorf("DecisionCount: want 1 got %d", sm.DecisionCount)
	}
	if sm.ActionCount != 1 {
		t.Errorf("ActionCount: want 1 got %d", sm.ActionCount)
	}
}

func TestLocalRepo_DeleteMeeting(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()

	if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "del"}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteMeeting(ctx, id); err != nil {
		t.Fatalf("DeleteMeeting: %v", err)
	}

	meetings, _ := r.ListMeetings(ctx)
	if len(meetings) != 0 {
		t.Errorf("want 0 meetings after delete, got %d", len(meetings))
	}
}

func TestLocalRepo_VerifyIntegrity_NewMeeting(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()

	if err := r.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "verify"}); err != nil {
		t.Fatal(err)
	}
	// A fresh meeting has a manifest + checksum but no checksums.json.
	// VerifyIntegrity should succeed (it's not an error to be missing the
	// full pipeline artifacts).
	if err := r.VerifyIntegrity(ctx, id); err != nil {
		t.Errorf("VerifyIntegrity on fresh meeting: %v", err)
	}
}

func TestLocalRepo_PrepareAudio(t *testing.T) {
	r := newLocalRepo(t)
	ctx := context.Background()
	id := uuid.New()

	path, err := r.PrepareAudio(ctx, id, ".wav")
	if err != nil {
		t.Fatalf("PrepareAudio: %v", err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
	_, ok := r.AudioPath(id)
	if ok {
		t.Error("AudioPath should return false before file is written")
	}
}

// Unit tests for the meetings service using FakeRepo.
// These run without starting a server or touching the filesystem.
package service_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/db"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/repo"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/secrets"
	"github.com/lukasstrickler/noto/internal/service"
	"github.com/lukasstrickler/noto/internal/testutil"
)

// newTestSvc builds a Service with a FakeRepo and in-memory SQLite
// deps. Workers are NOT started so no background goroutines run.
func newTestSvc(t *testing.T, fr repo.ArtifactRepository) *service.Service {
	t.Helper()
	cfgDir := t.TempDir()
	idx, err := search.NewSearchIndex(filepath.Join(cfgDir, "test.sqlite"))
	if err != nil {
		t.Fatalf("search index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	jobsDB, err := db.Open(filepath.Join(cfgDir, "jobs.sqlite"))
	if err != nil {
		t.Fatalf("jobs db: %v", err)
	}
	t.Cleanup(func() { _ = jobsDB.Close() })
	primary := secrets.NewFileStore(filepath.Join(cfgDir, "creds.json"))
	return service.New(service.Deps{
		Config:   config.Config{ConfigDir: cfgDir, ArtifactRoot: cfgDir},
		Secrets:  secrets.EnvFallbackStore{Primary: primary},
		Registry: providers.DefaultRegistry(),
		Search:   idx,
		JobsDB:   jobsDB,
		Repo:     fr,
		Version:  "unit-test",
	})
}

func TestService_ListMeetings_Empty(t *testing.T) {
	fr := testutil.NewFakeRepo()
	svc := newTestSvc(t, fr)
	ctx := context.Background()

	res, err := svc.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	if err != nil {
		t.Fatalf("ListMeetings: %v", err)
	}
	if res.Total != 0 {
		t.Errorf("want 0 total, got %d", res.Total)
	}
}

func TestService_ListMeetings_WithData(t *testing.T) {
	fr := testutil.NewFakeRepo()
	ctx := context.Background()
	for _, title := range []string{"Design review", "Sprint retrospective", "1:1"} {
		id := uuid.New()
		_ = fr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: title})
	}

	svc := newTestSvc(t, fr)
	res, err := svc.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	if err != nil {
		t.Fatalf("ListMeetings: %v", err)
	}
	if res.Total != 3 {
		t.Errorf("want 3 total, got %d", res.Total)
	}
}

func TestService_ListMeetings_QueryFilter(t *testing.T) {
	fr := testutil.NewFakeRepo()
	ctx := context.Background()
	_ = fr.CreateMeeting(ctx, uuid.New(), repo.CreateMeetingOpts{Title: "Design review"})
	_ = fr.CreateMeeting(ctx, uuid.New(), repo.CreateMeetingOpts{Title: "Sprint planning"})

	svc := newTestSvc(t, fr)
	res, err := svc.ListMeetings(ctx, notoapi.ListMeetingsOpts{Query: "sprint"})
	if err != nil {
		t.Fatalf("ListMeetings with query: %v", err)
	}
	if res.Total != 1 {
		t.Errorf("want 1 match, got %d", res.Total)
	}
	if res.Meetings[0].Title != "Sprint planning" {
		t.Errorf("unexpected title %q", res.Meetings[0].Title)
	}
}

func TestService_GetMeeting_NotFound(t *testing.T) {
	fr := testutil.NewFakeRepo()
	svc := newTestSvc(t, fr)
	_, err := svc.GetMeeting(context.Background(), uuid.New().String())
	if err == nil {
		t.Fatal("expected error for missing meeting")
	}
	apiErr, ok := notoapi.As(err)
	if !ok {
		t.Fatalf("expected notoapi error, got %T: %v", err, err)
	}
	if apiErr.Code != notoapi.CodeNotFound {
		t.Errorf("want CodeNotFound, got %q", apiErr.Code)
	}
}

func TestService_GetMeeting_InvalidID(t *testing.T) {
	svc := newTestSvc(t, testutil.NewFakeRepo())
	_, err := svc.GetMeeting(context.Background(), "not-a-uuid")
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
	apiErr, _ := notoapi.As(err)
	if apiErr.Code != notoapi.CodeInvalidRequest {
		t.Errorf("want CodeInvalidRequest, got %q", apiErr.Code)
	}
}

func TestService_GetMeeting_StatusProgression(t *testing.T) {
	fr := testutil.NewFakeRepo()
	ctx := context.Background()
	id := uuid.New()
	_ = fr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "status test"})

	svc := newTestSvc(t, fr)

	// No transcript → recorded.
	m, _ := svc.GetMeeting(ctx, id.String())
	if m.Status != notoapi.StatusRecorded {
		t.Errorf("want %q, got %q", notoapi.StatusRecorded, m.Status)
	}

	// Add transcript → transcribed.
	conf := 0.9
	_ = fr.SaveTranscript(ctx, id, &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     id.String(),
		Provider:      artifacts.TranscriptProvider{ID: "assemblyai"},
		Segments:      []artifacts.Segment{{ID: "s1", SpeakerID: "spk_0", Text: "hello", StartSeconds: 0, EndSeconds: 2, Confidence: &conf}},
		Speakers:      []artifacts.Speaker{{ID: "spk_0", DisplayName: "Alice"}},
	})
	m, _ = svc.GetMeeting(ctx, id.String())
	if m.Status != notoapi.StatusTranscribed {
		t.Errorf("want %q, got %q", notoapi.StatusTranscribed, m.Status)
	}

	// Add summary → summarized.
	_ = fr.SaveSummary(ctx, id, "# summary", &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     id.String(),
		ShortSummary:  "brief",
		Decisions:     []artifacts.SummaryItem{{Text: "Ship it"}},
	})
	m, _ = svc.GetMeeting(ctx, id.String())
	if m.Status != notoapi.StatusSummarized {
		t.Errorf("want %q, got %q", notoapi.StatusSummarized, m.Status)
	}
	if m.DecisionCount != 1 {
		t.Errorf("DecisionCount: want 1 got %d", m.DecisionCount)
	}
}

func TestService_DeleteMeeting(t *testing.T) {
	fr := testutil.NewFakeRepo()
	ctx := context.Background()
	id := uuid.New()
	_ = fr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "to delete"})

	svc := newTestSvc(t, fr)
	if err := svc.DeleteMeeting(ctx, id.String()); err != nil {
		t.Fatalf("DeleteMeeting: %v", err)
	}
	if fr.MeetingCount() != 0 {
		t.Error("expected 0 meetings after delete")
	}
	// Getting the deleted meeting should return NotFound.
	_, err := svc.GetMeeting(ctx, id.String())
	if err == nil {
		t.Error("expected error for deleted meeting")
	}
}

func TestService_UpdateSpeakerName(t *testing.T) {
	fr := testutil.NewFakeRepo()
	ctx := context.Background()
	id := uuid.New()
	_ = fr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "speakers"})
	conf := 0.9
	_ = fr.SaveTranscript(ctx, id, &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     id.String(),
		Speakers:      []artifacts.Speaker{{ID: "spk_0", DisplayName: "Unknown"}},
		Segments:      []artifacts.Segment{{ID: "s1", SpeakerID: "spk_0", Text: "hi", StartSeconds: 0, EndSeconds: 1, Confidence: &conf}},
	})

	svc := newTestSvc(t, fr)
	if err := svc.UpdateSpeakerName(ctx, id.String(), "spk_0", "Alice"); err != nil {
		t.Fatalf("UpdateSpeakerName: %v", err)
	}

	tr, err := svc.GetTranscript(ctx, id.String())
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if len(tr.Speakers) == 0 || tr.Speakers[0].DisplayName != "Alice" {
		t.Errorf("speaker name not updated: %+v", tr.Speakers)
	}
}

func TestService_GetTranscript_SpeakerLabelsResolved(t *testing.T) {
	fr := testutil.NewFakeRepo()
	ctx := context.Background()
	id := uuid.New()
	_ = fr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "labels"})
	conf := 0.9
	_ = fr.SaveTranscript(ctx, id, &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     id.String(),
		Speakers:      []artifacts.Speaker{{ID: "spk_0", DisplayName: "Bob"}},
		Segments:      []artifacts.Segment{{ID: "s1", SpeakerID: "spk_0", Text: "hello", StartSeconds: 0, EndSeconds: 1, Confidence: &conf}},
	})

	svc := newTestSvc(t, fr)
	tr, err := svc.GetTranscript(ctx, id.String())
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if len(tr.Segments) == 0 {
		t.Fatal("expected segments")
	}
	if tr.Segments[0].Speaker != "Bob" {
		t.Errorf("speaker label not resolved: want %q got %q", "Bob", tr.Segments[0].Speaker)
	}
}

package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/search"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/testutil"
)

// TestIndexOneMeeting_IndexesSpeakerName is the end-to-end UX contract: a meeting
// indexed after a speaker is identified must be findable BY THAT NAME, and the
// hit's Speaker must read the name — not the opaque "spk_0".
func TestIndexOneMeeting_IndexesSpeakerName(t *testing.T) {
	ctx := context.Background()
	idx, err := search.NewSearchIndex(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("search index: %v", err)
	}
	defer idx.Close()

	fr := testutil.NewFakeRepo()
	mid := uuid.New()
	if err := fr.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{Title: "Standup"}); err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	if err := fr.SaveTranscript(ctx, mid, &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     mid.String(),
		Speakers:      []artifacts.Speaker{{ID: "spk_0", DisplayName: "Maya"}},
		Segments:      []artifacts.Segment{{ID: "s0", SpeakerID: "spk_0", Text: "let us begin the review"}},
	}); err != nil {
		t.Fatalf("save transcript: %v", err)
	}

	svc := &Service{search: idx, repo: fr}
	if err := svc.indexOneMeeting(ctx, mid, "Standup"); err != nil {
		t.Fatalf("indexOneMeeting: %v", err)
	}

	hits, err := idx.Search("Maya") // search BY the resolved name
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.MeetingID == mid.String() {
			found = true
			if h.Speaker != "Maya" {
				t.Errorf("hit Speaker = %q; want the resolved name %q, not the raw id", h.Speaker, "Maya")
			}
		}
	}
	if !found {
		t.Errorf("searching the speaker's name should find their meeting; got %d hits", len(hits))
	}
}

// TestIndexOneMeeting_IndexesAutoIdentifiedName closes the same gap the agent
// handoff had: a speaker with NO transcript DisplayName but an AUTO mapping to a
// named profile must still be searchable by that profile name (auto-ID never writes
// the transcript label, so search must resolve via the mappings like the agent does).
func TestIndexOneMeeting_IndexesAutoIdentifiedName(t *testing.T) {
	ctx := context.Background()
	idx, err := search.NewSearchIndex(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("search index: %v", err)
	}
	defer idx.Close()

	fr := testutil.NewFakeRepo()
	mid := uuid.New()
	if err := fr.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{Title: "Standup"}); err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	if err := fr.SaveTranscript(ctx, mid, &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     mid.String(),
		Speakers:      []artifacts.Speaker{{ID: "spk_0", Label: "Speaker 1"}}, // NOT named in the transcript
		Segments:      []artifacts.Segment{{ID: "s0", SpeakerID: "spk_0", Text: "let us review the roadmap"}},
	}); err != nil {
		t.Fatalf("save transcript: %v", err)
	}

	pr := &memorySpeakerProfileRepo{}
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{ID: "p-maya", DisplayName: "Maya"})
	profileID := "p-maya"
	mr := &memoryMeetingMappingRepo{}
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: mid.String(), MeetingSpeakerID: "spk_0", ProfileID: &profileID, MatchStatus: "auto",
	})

	svc := &Service{search: idx, repo: fr, meetingMappings: mr, speakerProfiles: pr}
	if err := svc.indexOneMeeting(ctx, mid, "Standup"); err != nil {
		t.Fatalf("indexOneMeeting: %v", err)
	}

	hits, err := idx.Search("Maya")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	found := false
	for _, h := range hits {
		if h.MeetingID == mid.String() {
			found = true
			if h.Speaker != "Maya" {
				t.Errorf("hit Speaker = %q; want the auto-resolved name %q", h.Speaker, "Maya")
			}
		}
	}
	if !found {
		t.Errorf("an auto-identified speaker must be searchable by their profile name; got %d hits", len(hits))
	}
}

// TestUpdateSpeakerName_RefreshesSearchIndex closes the staleness gap: renaming a
// speaker must re-index the meeting so the NEW name is immediately searchable,
// not stuck until a manual reindex.
func TestUpdateSpeakerName_RefreshesSearchIndex(t *testing.T) {
	ctx := context.Background()
	idx, err := search.NewSearchIndex(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("search index: %v", err)
	}
	defer idx.Close()

	fr := testutil.NewFakeRepo()
	mid := uuid.New()
	if err := fr.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{Title: "Standup"}); err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	if err := fr.SaveTranscript(ctx, mid, &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     mid.String(),
		Speakers:      []artifacts.Speaker{{ID: "spk_0", Label: "Speaker 1"}}, // not yet named
		Segments:      []artifacts.Segment{{ID: "s0", SpeakerID: "spk_0", Text: "kicking off the review"}},
	}); err != nil {
		t.Fatalf("save transcript: %v", err)
	}

	svc := &Service{search: idx, repo: fr}
	if err := svc.indexOneMeeting(ctx, mid, "Standup"); err != nil {
		t.Fatalf("initial index: %v", err)
	}
	// Before the rename, "Alice" matches nothing.
	if hits, _ := idx.Search("Alice"); len(hits) != 0 {
		t.Fatalf("precondition: Alice should not match yet, got %d", len(hits))
	}

	if err := svc.UpdateSpeakerName(ctx, mid.String(), "spk_0", "Alice"); err != nil {
		t.Fatalf("UpdateSpeakerName: %v", err)
	}

	hits, err := idx.Search("Alice")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Error("renaming a speaker should re-index so the new name is searchable")
	}
}

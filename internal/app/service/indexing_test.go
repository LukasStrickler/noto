package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/search"
	"github.com/lukasstrickler/noto/internal/testutil"
)

// TestSpeakerLabelsByID pins the resolution order: display name, then the
// normalized label, then the raw id as a last resort.
func TestSpeakerLabelsByID(t *testing.T) {
	got := speakerLabelsByID([]artifacts.Speaker{
		{ID: "spk_0", DisplayName: "Maya", Label: "Speaker 1"},
		{ID: "spk_1", Label: "Speaker 2"}, // no display name → label
		{ID: "spk_2"},                     // nothing → raw id
	})
	want := map[string]string{"spk_0": "Maya", "spk_1": "Speaker 2", "spk_2": "spk_2"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("speaker %s resolved to %q; want %q", id, got[id], w)
		}
	}
}

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

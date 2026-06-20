package service

import (
	"context"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
)

// TestMatchSpeakers_AutoMatchFoldsRunningMean proves the auto-match centroid
// update is a TRUE running mean: an established profile (count=4) that auto-matches
// a new meeting advances to count=5, so the new voiceprint is folded in with weight
// 1/5 — not the 50/50 a two-point average would give.
func TestMatchSpeakers_AutoMatchFoldsRunningMean(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	v := make([]float64, 192)
	for i := range v {
		v[i] = 0.5
	}

	pr := &memorySpeakerProfileRepo{}
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{
		ID: "prof1", DisplayName: "Alice",
		EmbeddingVector: v, EmbeddingDim: 192, EmbeddingCount: 4,
		CreatedAt: now, UpdatedAt: now,
	})
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}

	tr := &artifacts.Transcript{
		MeetingID: "m1",
		Speakers:  []artifacts.Speaker{{ID: "spk_0", ProviderLabel: "A", DisplayName: "Alice"}},
		// 60s of speech clears speakers.MinEnrollSpeech so the match stays auto.
		Segments: []artifacts.Segment{{ID: "s0", SpeakerID: "spk_0", StartSeconds: 0, EndSeconds: 60}},
	}
	embeddings := map[string][]float64{"A": v} // identical to profile → auto-match

	if err := svc.matchSpeakers(ctx, "m1", tr, embeddings); err != nil {
		t.Fatalf("matchSpeakers: %v", err)
	}

	got, err := pr.Get(ctx, "prof1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EmbeddingCount != 5 {
		t.Errorf("EmbeddingCount = %d; want 5 (auto-match folds one observation via running mean)", got.EmbeddingCount)
	}

	ms, _ := mr.ListByMeeting(ctx, "m1")
	if len(ms) != 1 || ms[0].MatchStatus != "auto" {
		t.Fatalf("expected one auto mapping, got %+v", ms)
	}
}

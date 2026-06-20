package service

import (
	"context"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/speakers"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
)

// TestFoldEmbeddingIntoProfile covers the shared learning core used by BOTH the
// auto-match and the manual-confirmation paths: a new enrollment is folded as a
// count-weighted running mean, and degenerate inputs are safe no-ops.
func TestFoldEmbeddingIntoProfile(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	a := []float64{1, 0}
	b := []float64{0, 1}

	pr := &memorySpeakerProfileRepo{}
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{
		ID: "p1", DisplayName: "Alice",
		EmbeddingVector: a, EmbeddingDim: 2, EmbeddingCount: 9,
		CreatedAt: now, UpdatedAt: now,
	})
	svc := &Service{speakerProfiles: pr}

	// A new observation toward B, folded into a count-9 profile: count -> 10, and
	// the voiceprint stays much closer to A than to B (1/10 pull, not 1/2).
	svc.foldEmbeddingIntoProfile(ctx, "p1", b)
	got, _ := pr.Get(ctx, "p1")
	if got.EmbeddingCount != 10 {
		t.Errorf("EmbeddingCount = %d; want 10", got.EmbeddingCount)
	}
	simA, _ := speakers.CosineSimilarity(got.EmbeddingVector, a)
	simB, _ := speakers.CosineSimilarity(got.EmbeddingVector, b)
	if simA <= simB {
		t.Errorf("established profile should stay closer to A: A=%.4f B=%.4f", simA, simB)
	}

	// Empty embedding and missing profile are no-ops (must never fail the caller).
	before, _ := pr.Get(ctx, "p1")
	svc.foldEmbeddingIntoProfile(ctx, "p1", nil)
	svc.foldEmbeddingIntoProfile(ctx, "nope", a)
	after, _ := pr.Get(ctx, "p1")
	if after.EmbeddingCount != before.EmbeddingCount {
		t.Errorf("no-op inputs changed the profile: %d -> %d", before.EmbeddingCount, after.EmbeddingCount)
	}

	// A profile with no prior voiceprint adopts the first observation at count 1.
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{ID: "p2", DisplayName: "Bob", CreatedAt: now, UpdatedAt: now})
	svc.foldEmbeddingIntoProfile(ctx, "p2", b)
	p2, _ := pr.Get(ctx, "p2")
	if p2.EmbeddingCount != 1 || len(p2.EmbeddingVector) != 2 {
		t.Errorf("first enrollment should set count=1 and the vector; got count=%d len=%d", p2.EmbeddingCount, len(p2.EmbeddingVector))
	}
}

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

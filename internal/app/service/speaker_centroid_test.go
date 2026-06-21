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

	// A profile with no prior voiceprint adopts the first observation at count 1, and
	// records the embedding space it now lives in (so a later model switch can tell
	// which voiceprints to re-embed) — like every other voiceprint-writing path.
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{ID: "p2", DisplayName: "Bob", CreatedAt: now, UpdatedAt: now})
	svc.foldEmbeddingIntoProfile(ctx, "p2", b)
	p2, _ := pr.Get(ctx, "p2")
	if p2.EmbeddingCount != 1 || len(p2.EmbeddingVector) != 2 {
		t.Errorf("first enrollment should set count=1 and the vector; got count=%d len=%d", p2.EmbeddingCount, len(p2.EmbeddingVector))
	}
	if p2.EmbeddingModel == "" {
		t.Error("first enrollment must tag the embedding model, else the voiceprint's space is untracked")
	}
}

// TestShouldFoldOnPatch pins the human-feedback learning rule: fold exactly once
// per (embedding, profile) observation. The case that motivated the fix is
// "confirm pending in place" — accepting the review queue's suggestion without
// changing the profile — which the earlier profile-changed-only guard dropped,
// leaving the most common identity action teaching the model nothing.
func TestShouldFoldOnPatch(t *testing.T) {
	cases := []struct {
		name                   string
		oldProfile, newProfile string
		oldStatus, newStatus   string
		want                   bool
	}{
		{"confirm pending in place", "P", "P", "pending", "manual", true},
		{"confirm pending -> auto", "P", "P", "pending", "auto", true},
		{"reassign to different profile", "P", "Q", "manual", "manual", true},
		{"assign a previously-unlinked speaker", "", "P", "new", "manual", true},
		{"re-confirm already-folded manual (no double count)", "P", "P", "manual", "manual", false},
		{"confirm an auto match already folded by auto path", "P", "P", "auto", "manual", false},
		{"confirm a new mint already seeded at creation", "P", "P", "new", "manual", false},
		{"pending stays pending (confidence-only patch)", "P", "P", "pending", "pending", false},
		{"cleared to no profile", "P", "", "manual", "manual", false},
	}
	for _, c := range cases {
		if got := shouldFoldOnPatch(c.oldProfile, c.newProfile, c.oldStatus, c.newStatus); got != c.want {
			t.Errorf("%s: shouldFoldOnPatch(%q,%q,%q,%q) = %v; want %v",
				c.name, c.oldProfile, c.newProfile, c.oldStatus, c.newStatus, got, c.want)
		}
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

// TestMatchSpeakers_PreservesManualAssignment is the human-work contract: a
// re-transcribe / re-process must NOT overwrite a speaker the user manually
// assigned with a fresh auto-guess. Manual is the strongest identity signal.
func TestMatchSpeakers_PreservesManualAssignment(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	v := make([]float64, 192)
	for i := range v {
		v[i] = 0.5
	}

	pr := &memorySpeakerProfileRepo{}
	// A profile the auto-matcher WOULD pick (its centroid equals the query).
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{
		ID: "auto-target", DisplayName: "Bob",
		EmbeddingVector: v, EmbeddingDim: 192, EmbeddingCount: 4,
		CreatedAt: now, UpdatedAt: now,
	})
	mr := &memoryMeetingMappingRepo{}
	// The user already MANUALLY assigned spk_0 to a DIFFERENT person.
	manualPID := "alice-manual"
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: "m1", MeetingSpeakerID: "spk_0", ProviderLabel: "A",
		ProfileID: &manualPID, MatchStatus: "manual", EmbeddingVector: v, EmbeddingDim: 192,
		CreatedAt: now, UpdatedAt: now,
	})
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}

	tr := &artifacts.Transcript{
		MeetingID: "m1",
		Speakers:  []artifacts.Speaker{{ID: "spk_0", ProviderLabel: "A", DisplayName: "Alice"}},
		Segments:  []artifacts.Segment{{ID: "s0", SpeakerID: "spk_0", StartSeconds: 0, EndSeconds: 60}},
	}
	embeddings := map[string][]float64{"A": v} // would auto-match "auto-target"

	if err := svc.matchSpeakers(ctx, "m1", tr, embeddings); err != nil {
		t.Fatalf("matchSpeakers: %v", err)
	}

	ms, _ := mr.ListByMeeting(ctx, "m1")
	if len(ms) != 1 {
		t.Fatalf("expected the single manual mapping to be preserved, got %d", len(ms))
	}
	if ms[0].MatchStatus != "manual" || ms[0].ProfileID == nil || *ms[0].ProfileID != "alice-manual" {
		t.Errorf("manual assignment was overwritten: %+v", ms[0])
	}
}

// TestMatchSpeakers_SkipsStaleDimensionProfile is the upgrade-safety contract: a
// profile embedded by a different/older model (a different vector dimension) must
// be SKIPPED, not fed to the matcher — whose strict contract errors on the first
// dimension mismatch, which would otherwise fail the whole speaker-matching step
// (and the embed job) for every meeting once the embedder model changes. The
// same-dimension profile must still auto-match.
func TestMatchSpeakers_SkipsStaleDimensionProfile(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	v := make([]float64, 192)
	for i := range v {
		v[i] = 0.5
	}
	stale := make([]float64, 256) // a profile from a different embedder model
	for i := range stale {
		stale[i] = 0.1
	}

	pr := &memorySpeakerProfileRepo{}
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{
		ID: "stale", DisplayName: "Old", EmbeddingVector: stale, EmbeddingDim: 256, EmbeddingCount: 3,
		CreatedAt: now, UpdatedAt: now,
	})
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{
		ID: "match", DisplayName: "Alice", EmbeddingVector: v, EmbeddingDim: 192, EmbeddingCount: 4,
		CreatedAt: now, UpdatedAt: now,
	})
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}

	tr := &artifacts.Transcript{
		MeetingID: "m1",
		Speakers:  []artifacts.Speaker{{ID: "spk_0", ProviderLabel: "A", DisplayName: "Alice"}},
		Segments:  []artifacts.Segment{{ID: "s0", SpeakerID: "spk_0", StartSeconds: 0, EndSeconds: 60}},
	}
	embeddings := map[string][]float64{"A": v} // 192-dim, matches "match", not "stale"

	// Must NOT error on the 256-dim stale profile.
	if err := svc.matchSpeakers(ctx, "m1", tr, embeddings); err != nil {
		t.Fatalf("matchSpeakers must skip a stale-dim profile, not fail: %v", err)
	}
	ms, _ := mr.ListByMeeting(ctx, "m1")
	if len(ms) != 1 || ms[0].MatchStatus != "auto" || ms[0].ProfileID == nil || *ms[0].ProfileID != "match" {
		t.Fatalf("expected an auto match to the 192-dim profile, got %+v", ms)
	}
}

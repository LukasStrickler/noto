package service

import (
	"context"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// unit192 builds a normalized-ish 192-d vector pointing mostly along axis ax,
// so cosine similarity between two such vectors is high when they share an axis.
func unit192(ax int) []float64 {
	v := make([]float64, 192)
	v[ax%192] = 1
	return v
}

func mixed192(ax int, leak float64) []float64 {
	v := unit192(ax)
	v[(ax+1)%192] = leak
	return v
}

func seedProfile(t *testing.T, pr *memorySpeakerProfileRepo, id, name string, emb []float64) {
	t.Helper()
	now := time.Now()
	_ = pr.Create(context.Background(), speakerstore.SpeakerProfile{
		ID: id, DisplayName: name, EmbeddingVector: emb, EmbeddingDim: len(emb),
		EmbeddingModel: "ecapa", CreatedAt: now, UpdatedAt: now,
	})
}

func TestEnrichMappings_RanksAndResolvesNames(t *testing.T) {
	pr := &memorySpeakerProfileRepo{}
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}
	ctx := context.Background()

	seedProfile(t, pr, "p-alice", "Alice", unit192(0))
	seedProfile(t, pr, "p-bob", "Bob", unit192(1))

	// One resolved speaker (linked to Alice) and one unresolved with a voiceprint
	// close to Bob.
	now := time.Now()
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: "m1", MeetingSpeakerID: "A", ProfileID: strPtrL("p-alice"),
		MatchStatus: "auto", EmbeddingVector: unit192(0), EmbeddingDim: 192, CreatedAt: now, UpdatedAt: now,
	})
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: "m1", MeetingSpeakerID: "B", ProfileID: nil,
		MatchStatus: "new", EmbeddingVector: mixed192(1, 0.2), EmbeddingDim: 192, CreatedAt: now, UpdatedAt: now,
	})

	out := svc.enrichMappings("m1", mustMappings(t, mr, "m1"))
	byID := map[string]int{}
	for i, m := range out {
		byID[m.MeetingSpeakerID] = i
	}
	a := out[byID["A"]]
	if a.ProfileName != "Alice" {
		t.Errorf("resolved name for A = %q, want Alice", a.ProfileName)
	}
	if len(a.Candidates) != 0 {
		t.Errorf("resolved speaker A should carry no suggestions, got %d", len(a.Candidates))
	}
	b := out[byID["B"]]
	if len(b.Candidates) == 0 {
		t.Fatalf("unresolved speaker B should carry suggestions")
	}
	if b.Candidates[0].ProfileID != "p-bob" {
		t.Errorf("top suggestion = %q, want p-bob", b.Candidates[0].ProfileID)
	}
	if b.Candidates[0].Rank != 1 {
		t.Errorf("top rank = %d, want 1", b.Candidates[0].Rank)
	}
}

func TestEnrichMappings_ContextPriorSurfacesCoAttendee(t *testing.T) {
	pr := &memorySpeakerProfileRepo{}
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}
	ctx := context.Background()

	// Two profiles whose voiceprints are EQUALLY close to the unknown speaker.
	seedProfile(t, pr, "p-anchor", "Anchor", unit192(0))
	seedProfile(t, pr, "p-regular", "Regular", unit192(5))
	seedProfile(t, pr, "p-stranger", "Stranger", unit192(5)) // identical voice score to Regular

	now := time.Now()
	// History: Regular and Anchor attended 3 past meetings together.
	for i, mid := range []string{"h1", "h2", "h3"} {
		_ = i
		_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{MeetingID: mid, MeetingSpeakerID: "A", ProfileID: strPtrL("p-anchor"), MatchStatus: "auto", EmbeddingVector: unit192(0), EmbeddingDim: 192, CreatedAt: now, UpdatedAt: now})
		_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{MeetingID: mid, MeetingSpeakerID: "B", ProfileID: strPtrL("p-regular"), MatchStatus: "auto", EmbeddingVector: unit192(5), EmbeddingDim: 192, CreatedAt: now, UpdatedAt: now})
	}

	// This meeting: Anchor identified, plus an unknown speaker whose voice ties
	// Regular and Stranger. The co-attendance prior should break the tie toward
	// Regular.
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{MeetingID: "now", MeetingSpeakerID: "A", ProfileID: strPtrL("p-anchor"), MatchStatus: "auto", EmbeddingVector: unit192(0), EmbeddingDim: 192, CreatedAt: now, UpdatedAt: now})
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{MeetingID: "now", MeetingSpeakerID: "C", ProfileID: nil, MatchStatus: "new", EmbeddingVector: unit192(5), EmbeddingDim: 192, CreatedAt: now, UpdatedAt: now})

	out := svc.enrichMappings("now", mustMappings(t, mr, "now"))
	var c []notoapi.SpeakerCandidate
	for _, m := range out {
		if m.MeetingSpeakerID == "C" {
			c = m.Candidates
		}
	}
	if len(c) == 0 {
		t.Fatal("expected suggestions for C")
	}
	if c[0].ProfileID != "p-regular" {
		t.Errorf("context prior should surface Regular first, got %q", c[0].ProfileID)
	}
	if c[0].Reason != "frequent co-attendee" {
		t.Errorf("expected co-attendee reason, got %q", c[0].Reason)
	}
	// The reported score must remain the raw voice score (prior never inflates it).
	if c[0].Score > 1.0001 {
		t.Errorf("reported score should be the voice cosine, got %v", c[0].Score)
	}
}

func mustMappings(t *testing.T, mr *memoryMeetingMappingRepo, meetingID string) []speakerstore.MeetingSpeakerMapping {
	t.Helper()
	m, err := mr.ListByMeeting(context.Background(), meetingID)
	if err != nil {
		t.Fatalf("ListByMeeting: %v", err)
	}
	return m
}

func strPtrL(s string) *string { return &s }

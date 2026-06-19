package service

import (
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Fixture meeting IDs the identity seed maps onto (see seed.go).
const (
	seedMeetingQ2       = "11111111-1111-4111-8111-111111111111"
	seedMeetingMobile   = "44444444-4444-4444-8444-444444444444"
	seedMeetingSecurity = "55555555-5555-4555-8555-555555555555"
	seedMeetingDesign   = "88888888-8888-4888-8888-888888888888"
)

// The identity seed must produce a populated directory AND honest, engine-ranked
// suggestions covering every triage state: auto-linked speakers show their name,
// the deliberately-unresolved Carol is surfaced first by the context prior (with
// a plausible runner-up), Bob reads as not-set, and the unknown voice (Sam) is an
// auto-minted provisional profile with no suggestions.
func TestSeedSpeakerIdentity_DirectoryAndRanking(t *testing.T) {
	pr := &memorySpeakerProfileRepo{}
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}
	ctx := context.Background()

	n, err := svc.seedSpeakerIdentity(ctx)
	if err != nil {
		t.Fatalf("seedSpeakerIdentity: %v", err)
	}
	// One provisional ("new") profile (Sam) is minted on top of the directory.
	if want := len(seedPeople) + 1; n != want {
		t.Fatalf("seeded %d people, want %d", n, want)
	}

	// Auto-linked speaker: named everywhere, no picker.
	q2, _ := svc.GetMeetingSpeakerMappings(ctx, seedMeetingQ2)
	alice := findSeedMapping(t, q2.Mappings, "spk_alice")
	if alice.MatchStatus != "auto" || alice.ProfileName != "Alice Nguyen" {
		t.Errorf("alice = %s / %q, want auto / Alice Nguyen", alice.MatchStatus, alice.ProfileName)
	}
	if len(alice.Candidates) != 0 {
		t.Errorf("auto-linked alice should carry no candidates, got %d", len(alice.Candidates))
	}

	// Unresolved-but-known: Carol is pending here, surfaced #1 by the prior
	// (she co-attends Q2 with the people identified here), Riya a runner-up.
	design, _ := svc.GetMeetingSpeakerMappings(ctx, seedMeetingDesign)
	carol := findSeedMapping(t, design.Mappings, "spk_carol")
	if carol.MatchStatus != "pending" {
		t.Errorf("carol status = %s, want pending", carol.MatchStatus)
	}
	if carol.ProfileID != nil {
		t.Errorf("pending carol should be unlinked")
	}
	if len(carol.Candidates) < 2 {
		t.Fatalf("carol should have >=2 candidates, got %d", len(carol.Candidates))
	}
	if carol.Candidates[0].DisplayName != "Carol Reyes" {
		t.Errorf("top candidate = %q, want Carol Reyes", carol.Candidates[0].DisplayName)
	}
	if carol.Candidates[0].Reason != "frequent co-attendee" {
		t.Errorf("top reason = %q, want frequent co-attendee", carol.Candidates[0].Reason)
	}
	if !hasSeedCandidate(carol.Candidates, "Riya Kapoor") {
		t.Errorf("expected Riya as a runner-up, got %+v", carol.Candidates)
	}

	// Not set: Bob shares this meeting but matched no one here → unmatched,
	// unlinked, no candidates (the search/create path).
	bob := findSeedMapping(t, design.Mappings, "spk_bob")
	if bob.MatchStatus != "unmatched" || bob.ProfileID != nil {
		t.Errorf("bob = %s linked=%v, want unmatched / unlinked", bob.MatchStatus, bob.ProfileID != nil)
	}

	// Likely: Marc is pending in the mobile-beta kickoff.
	mobile, _ := svc.GetMeetingSpeakerMappings(ctx, seedMeetingMobile)
	marc := findSeedMapping(t, mobile.Mappings, "spk_marc")
	if marc.MatchStatus != "pending" || marc.ProfileID != nil {
		t.Errorf("marc = %s linked=%v, want pending / unlinked", marc.MatchStatus, marc.ProfileID != nil)
	}

	// New voice: the third speaker in the security incident is linked to an
	// auto-minted provisional profile named only by its anonymous diarization
	// label ("Speaker C", no voiceprint) and carries no suggestions.
	security, _ := svc.GetMeetingSpeakerMappings(ctx, seedMeetingSecurity)
	sam := findSeedMapping(t, security.Mappings, "spk_sam")
	if sam.MatchStatus != "new" || sam.ProfileID == nil {
		t.Errorf("sam = %s linked=%v, want new / linked to provisional", sam.MatchStatus, sam.ProfileID != nil)
	}
	if sam.ProfileName != "Speaker C" {
		t.Errorf("sam provisional name = %q, want Speaker C (its diarization label)", sam.ProfileName)
	}
	if len(sam.Candidates) != 0 {
		t.Errorf("provisional sam should have no candidates, got %+v", sam.Candidates)
	}
}

// The seed must leave the dashboard with a visible triage spread: the top-bar
// "⚑ N to identify" badge non-zero and the per-meeting clusters covering every
// glyph (likely / new / not-set / all-set). This locks what the demo shows.
func TestSeedSpeakerIdentity_DashboardSpread(t *testing.T) {
	pr := &memorySpeakerProfileRepo{}
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}
	ctx := context.Background()
	if _, err := svc.seedSpeakerIdentity(ctx); err != nil {
		t.Fatalf("seedSpeakerIdentity: %v", err)
	}

	// Top-bar badge: Marc (pending) + Sam (new) + Carol (pending) + Bob (unmatched).
	unresolved, err := mr.CountUnresolved(ctx)
	if err != nil {
		t.Fatalf("CountUnresolved: %v", err)
	}
	if unresolved != 4 {
		t.Errorf("unresolved (top-bar badge) = %d, want 4", unresolved)
	}

	counts, err := mr.StatusCountsByMeeting(ctx)
	if err != nil {
		t.Fatalf("StatusCountsByMeeting: %v", err)
	}
	// Mobile beta → one likely (◐). Security → one new (✦). Design review →
	// a mixed likely + not-set (◐ ○). Q2 → fully resolved (✓).
	if got := counts[seedMeetingMobile]["pending"]; got != 1 {
		t.Errorf("mobile pending = %d, want 1", got)
	}
	if got := counts[seedMeetingSecurity]["new"]; got != 1 {
		t.Errorf("security new = %d, want 1", got)
	}
	if got := counts[seedMeetingDesign]["pending"]; got != 1 {
		t.Errorf("design pending = %d, want 1", got)
	}
	if got := counts[seedMeetingDesign]["unmatched"]; got != 1 {
		t.Errorf("design unmatched = %d, want 1", got)
	}
	q2 := counts[seedMeetingQ2]
	if q2["auto"] != 3 || q2["pending"]+q2["new"]+q2["unmatched"] != 0 {
		t.Errorf("Q2 should be fully resolved (auto:3), got %+v", q2)
	}

	// People-page "to review" signal: the provisional ("Speaker C") is
	// unconfirmed (1), while a confirmed person like Alice is not.
	profiles, err := svc.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles: %v", err)
	}
	byName := map[string]notoapi.SpeakerProfile{}
	for _, pr := range profiles {
		byName[pr.DisplayName] = pr
	}
	if got := byName["Speaker C"].UnconfirmedMeetings; got != 1 {
		t.Errorf("provisional (Speaker C) UnconfirmedMeetings = %d, want 1", got)
	}
	if got := byName["Alice Nguyen"].UnconfirmedMeetings; got != 0 {
		t.Errorf("Alice UnconfirmedMeetings = %d, want 0", got)
	}
}

// countPeopleToReview is the People nav pill's signal — provisional people
// (profiles with an unconfirmed mapping), person-centric and distinct from the
// meeting-side SpeakersToID. The seed mints exactly one: the security
// incident's unknown voice, the auto-named "Speaker C".
func TestSeedSpeakerIdentity_PeopleToReview(t *testing.T) {
	pr := &memorySpeakerProfileRepo{}
	mr := &memoryMeetingMappingRepo{}
	svc := &Service{speakerProfiles: pr, meetingMappings: mr}
	if _, err := svc.seedSpeakerIdentity(context.Background()); err != nil {
		t.Fatalf("seedSpeakerIdentity: %v", err)
	}
	if got := svc.countPeopleToReview(); got != 1 {
		t.Errorf("countPeopleToReview() = %d, want 1 (the provisional Speaker C)", got)
	}
}

func findSeedMapping(t *testing.T, mappings []notoapi.MeetingSpeakerMapping, speakerID string) notoapi.MeetingSpeakerMapping {
	t.Helper()
	for _, m := range mappings {
		if m.MeetingSpeakerID == speakerID {
			return m
		}
	}
	t.Fatalf("no mapping for speaker %q", speakerID)
	return notoapi.MeetingSpeakerMapping{}
}

func hasSeedCandidate(cands []notoapi.SpeakerCandidate, name string) bool {
	for _, c := range cands {
		if c.DisplayName == name {
			return true
		}
	}
	return false
}

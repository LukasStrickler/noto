package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
	"github.com/lukasstrickler/noto/internal/testutil"
)

// TestDeleteMeeting_RemovesOrphanedSpeakerMappings pins the cleanup contract:
// deleting a meeting must also drop its speaker mappings, which live in a
// SEPARATE store the artifact repo can't reach. Without it they orphan — a
// deleted meeting's unresolved speakers keep inflating the "to identify" badge
// and skew the cross-meeting identity priors. Mappings in OTHER meetings survive.
func TestDeleteMeeting_RemovesOrphanedSpeakerMappings(t *testing.T) {
	s := newJobsTestSvc(t) // jobsDB wired so DeleteMeeting's emitStatusBar is safe
	ctx := context.Background()

	fr := testutil.NewFakeRepo()
	mr := &memoryMeetingMappingRepo{}
	s.repo = fr
	s.meetingMappings = mr

	mid := uuid.New()
	if err := fr.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{Title: "Doomed"}); err != nil {
		t.Fatalf("create meeting: %v", err)
	}
	now := time.Now()
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: mid.String(), MeetingSpeakerID: "spk_0", MatchStatus: "new",
		CreatedAt: now, UpdatedAt: now,
	})
	// A mapping in a DIFFERENT meeting must survive the delete.
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: "other", MeetingSpeakerID: "spk_0", MatchStatus: "new",
		CreatedAt: now, UpdatedAt: now,
	})
	if before, _ := mr.CountUnresolved(ctx); before != 2 {
		t.Fatalf("precondition: want 2 unresolved, got %d", before)
	}

	if err := s.DeleteMeeting(ctx, mid.String()); err != nil {
		t.Fatalf("DeleteMeeting: %v", err)
	}

	if gone, _ := mr.ListByMeeting(ctx, mid.String()); len(gone) != 0 {
		t.Errorf("deleted meeting's speaker mappings should be removed, got %d", len(gone))
	}
	if after, _ := mr.CountUnresolved(ctx); after != 1 {
		t.Errorf("only the surviving meeting's unresolved mapping should remain, got %d", after)
	}
}

// TestDeleteSpeakerProfile_ReopensMappings pins the sibling cleanup: deleting a
// person must RE-OPEN their speaker mappings as unresolved (not leave them
// dangling at a deleted profile), so those speakers return to the identify queue.
func TestDeleteSpeakerProfile_ReopensMappings(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	pr := &memorySpeakerProfileRepo{}
	mr := &memoryMeetingMappingRepo{}
	s.speakerProfiles = pr
	s.meetingMappings = mr

	pid := uuid.New().String()
	now := time.Now()
	_ = pr.Create(ctx, speakerstore.SpeakerProfile{ID: pid, DisplayName: "Alice", CreatedAt: now, UpdatedAt: now})
	_ = mr.Upsert(ctx, speakerstore.MeetingSpeakerMapping{
		MeetingID: "m1", MeetingSpeakerID: "spk_0", ProfileID: &pid, MatchStatus: "manual",
		CreatedAt: now, UpdatedAt: now,
	})
	// Resolved (manual) → not counted as unresolved yet.
	if n, _ := mr.CountUnresolved(ctx); n != 0 {
		t.Fatalf("precondition: a manual mapping is resolved, want 0 unresolved, got %d", n)
	}

	if err := s.DeleteSpeakerProfile(ctx, pid); err != nil {
		t.Fatalf("DeleteSpeakerProfile: %v", err)
	}

	got, _ := mr.ListByMeeting(ctx, "m1")
	if len(got) != 1 || got[0].ProfileID != nil || got[0].MatchStatus != "new" {
		t.Fatalf("mapping should be re-opened (nil profile, status new), got %+v", got)
	}
	if n, _ := mr.CountUnresolved(ctx); n != 1 {
		t.Errorf("the re-opened speaker should now count as unresolved, got %d", n)
	}
}

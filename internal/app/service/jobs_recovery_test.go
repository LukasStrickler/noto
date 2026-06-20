package service

import (
	"context"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// insertRunningJob seeds a row that's stuck in "running" — the state a job is
// left in when the process dies mid-flight.
func insertRunningJob(t *testing.T, s *Service, id string, attempt int, priority notoapi.JobPriority) {
	t.Helper()
	now := time.Now().UnixMilli()
	_, err := s.jobsDB.Exec(
		`INSERT INTO jobs (id, kind, meeting_id, status, options_json, created_at, started_at, attempt, priority, progress)
		 VALUES (?, ?, '', ?, '{}', ?, ?, ?, ?, 0.5)`,
		id, string(notoapi.JobTranscribe), string(notoapi.JobRunning),
		now, now, attempt, int(priority),
	)
	if err != nil {
		t.Fatalf("insert running job %s: %v", id, err)
	}
}

// TestRecoverInterruptedJobsResumesUnderCap is the durability contract: a job
// still running at startup (its process died mid-flight) is RE-QUEUED to finish
// when it's under the attempt cap, and PARKED once it has exhausted its attempts
// so a poison-pill job can't crash-loop the server.
func TestRecoverInterruptedJobsResumesUnderCap(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	insertRunningJob(t, s, "j-under", 1, notoapi.JobPriorityInteractive)
	insertRunningJob(t, s, "j-cap", maxJobAttempts, notoapi.JobPriorityInteractive)

	if err := s.recoverInterruptedJobs(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	under, err := s.GetJob(ctx, "j-under")
	if err != nil {
		t.Fatalf("get j-under: %v", err)
	}
	if under.Status != notoapi.JobQueued {
		t.Errorf("under-cap job should resume (queued), got %s", under.Status)
	}
	if under.Priority != notoapi.JobPriorityInteractive {
		t.Errorf("priority must survive resume so it re-enters its tier; got %d", under.Priority)
	}
	if under.Progress != 0 {
		t.Errorf("progress should reset on resume, got %v", under.Progress)
	}

	capped, err := s.GetJob(ctx, "j-cap")
	if err != nil {
		t.Fatalf("get j-cap: %v", err)
	}
	if capped.Status != notoapi.JobInterrupted {
		t.Errorf("attempt-exhausted job should be parked (interrupted), got %s", capped.Status)
	}
}

// TestRecoverInterruptedJobThenClaimable ties resume to the scheduler: a resumed
// job is immediately claimable by the worker pool, and its attempt increments on
// re-claim (so it still counts against the cap on a subsequent crash).
func TestRecoverInterruptedJobThenClaimable(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	insertRunningJob(t, s, "j1", 1, notoapi.JobPriorityInteractive)
	if err := s.recoverInterruptedJobs(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	got, ok := s.claimNextJob()
	if !ok {
		t.Fatal("resumed job should be claimable")
	}
	if got.ID != "j1" {
		t.Fatalf("claimed %s; want resumed j1", got.ID)
	}
	if got.Attempt != 2 {
		t.Errorf("attempt should increment on re-claim (1 before crash + 1 now); got %d", got.Attempt)
	}
}

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

// TestFinalizeJobStatus pins the rule that decides a job's terminal state. Two
// cases matter: a graceful SHUTDOWN mid-job re-queues (so a deploy doesn't strand
// the meetings in flight), but a USER cancel is authoritative and wins even when a
// shutdown races it — a job the user canceled must never be resurrected by resume.
func TestFinalizeJobStatus(t *testing.T) {
	errBoom := context.Canceled
	cases := []struct {
		name             string
		err              error
		shutdownCanceled bool
		userCanceled     bool
		wantStatus       notoapi.JobStatus
		wantRequeue      bool
	}{
		{"success", nil, false, false, notoapi.JobSucceeded, false},
		{"success even during shutdown", nil, true, false, notoapi.JobSucceeded, false},
		{"success despite a late cancel", nil, false, true, notoapi.JobSucceeded, false},
		{"shutdown mid-job re-queues", errBoom, true, false, "", true},
		{"user cancel finalizes canceled", errBoom, false, true, notoapi.JobCanceled, false},
		{"user cancel WINS over a racing shutdown", errBoom, true, true, notoapi.JobCanceled, false},
		{"plain failure", errBoom, false, false, notoapi.JobFailed, false},
	}
	for _, c := range cases {
		gotStatus, gotRequeue := finalizeJobStatus(c.err, c.shutdownCanceled, c.userCanceled)
		if gotStatus != c.wantStatus || gotRequeue != c.wantRequeue {
			t.Errorf("%s: finalizeJobStatus = (%q,%v); want (%q,%v)",
				c.name, gotStatus, gotRequeue, c.wantStatus, c.wantRequeue)
		}
	}
}

// TestRecoverHonorsCancelRequested covers the durable-cancel path: if the process
// died after the user requested a cancel (cancel_requested=1) but before the
// worker finalized it, the job must be recovered as CANCELED — not resumed.
func TestRecoverHonorsCancelRequested(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	insertRunningJob(t, s, "j-canceling", 1, notoapi.JobPriorityInteractive)
	if _, err := s.jobsDB.Exec(`UPDATE jobs SET cancel_requested = 1 WHERE id = ?`, "j-canceling"); err != nil {
		t.Fatalf("set cancel_requested: %v", err)
	}
	// A normal in-flight job alongside it must still resume.
	insertRunningJob(t, s, "j-normal", 1, notoapi.JobPriorityInteractive)

	if err := s.recoverInterruptedJobs(ctx); err != nil {
		t.Fatalf("recover: %v", err)
	}

	canceling, _ := s.GetJob(ctx, "j-canceling")
	if canceling.Status != notoapi.JobCanceled {
		t.Errorf("a cancel requested before the crash must be honored, got %s", canceling.Status)
	}
	normal, _ := s.GetJob(ctx, "j-normal")
	if normal.Status != notoapi.JobQueued {
		t.Errorf("a non-canceled in-flight job should still resume, got %s", normal.Status)
	}
}

// TestRequeueInterruptedJobResumes covers the single-job shutdown path used by
// runJob: an in-flight job is returned to the queue (claimable again) with its
// priority preserved and progress reset, so a restart finishes it.
func TestRequeueInterruptedJobResumes(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	insertRunningJob(t, s, "j-shutdown", 1, notoapi.JobPriorityBackground)
	s.requeueInterruptedJob("j-shutdown")

	got, err := s.GetJob(ctx, "j-shutdown")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != notoapi.JobQueued {
		t.Errorf("shutdown-interrupted job should be re-queued, got %s", got.Status)
	}
	if got.Priority != notoapi.JobPriorityBackground {
		t.Errorf("priority must survive requeue so it re-enters its tier; got %d", got.Priority)
	}
	if got.Progress != 0 {
		t.Errorf("progress should reset on requeue, got %v", got.Progress)
	}
	if _, ok := s.claimNextJob(); !ok {
		t.Error("re-queued job should be immediately claimable")
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

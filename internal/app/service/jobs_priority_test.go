package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/db"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func newJobsTestSvc(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	jobsDB, err := db.Open(filepath.Join(dir, "jobs.sqlite"))
	if err != nil {
		t.Fatalf("jobs db: %v", err)
	}
	if err := jobsDB.Migrate(db.JobsSchema); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = jobsDB.Close() })
	return New(Deps{
		Config:   config.Config{ConfigDir: dir, ArtifactRoot: dir},
		Registry: providers.DefaultRegistry(),
		JobsDB:   jobsDB,
		Version:  "test",
	})
}

// TestJobKindPriority pins the kind→priority mapping (the single source of truth
// the queue orders by): the meeting-processing path is interactive, bulk and
// housekeeping work is background.
func TestJobKindPriority(t *testing.T) {
	interactive := []notoapi.JobKind{
		notoapi.JobIngest, notoapi.JobTranscribe, notoapi.JobSummarize,
		notoapi.JobPipeline, notoapi.JobIndex,
	}
	for _, k := range interactive {
		if got := k.Priority(); got != notoapi.JobPriorityInteractive {
			t.Errorf("%s priority = %d; want interactive %d", k, got, notoapi.JobPriorityInteractive)
		}
	}
	background := []notoapi.JobKind{notoapi.JobReindex, notoapi.JobVerify, notoapi.JobDownloadModel}
	for _, k := range background {
		if got := k.Priority(); got != notoapi.JobPriorityBackground {
			t.Errorf("%s priority = %d; want background %d", k, got, notoapi.JobPriorityBackground)
		}
	}
	if got := notoapi.JobKind("nonsense").Priority(); got != notoapi.JobPriorityNormal {
		t.Errorf("unknown kind priority = %d; want normal %d", got, notoapi.JobPriorityNormal)
	}
}

// TestClaimNextJobOrdersByPriority is the scheduler contract: a freshly-enqueued
// interactive job (a user waiting on their meeting) is claimed AHEAD of an
// already-queued bulk reindex, and a deferred idle-GPU job is claimed last —
// even though it was enqueued first.
func TestClaimNextJobOrdersByPriority(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	// Enqueue worst-priority-first to prove ordering isn't just insertion order:
	// idle (deferred, enqueued FIRST) → background reindex → interactive transcribe.
	idle, err := s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobIndex, Priority: notoapi.JobPriorityIdle})
	if err != nil {
		t.Fatalf("create idle: %v", err)
	}
	if idle.Priority != notoapi.JobPriorityIdle {
		t.Fatalf("explicit override lost: priority = %d", idle.Priority)
	}
	bg, err := s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobReindex})
	if err != nil {
		t.Fatalf("create reindex: %v", err)
	}
	inter, err := s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobTranscribe})
	if err != nil {
		t.Fatalf("create transcribe: %v", err)
	}

	wantOrder := []string{inter.ID, bg.ID, idle.ID}
	for i, want := range wantOrder {
		got, ok := s.claimNextJob()
		if !ok {
			t.Fatalf("claim %d: no job available", i)
		}
		if got.ID != want {
			t.Fatalf("claim %d = %s (%s, prio %d); want %s", i, got.ID, got.Kind, got.Priority, want)
		}
	}
	if _, ok := s.claimNextJob(); ok {
		t.Error("queue should be drained")
	}
}

// TestClaimNextJobFIFOWithinPriority confirms the age tiebreak: two equal-priority
// jobs are claimed oldest-first.
func TestClaimNextJobFIFOWithinPriority(t *testing.T) {
	s := newJobsTestSvc(t)
	ctx := context.Background()

	first, err := s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobTranscribe})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // distinct created_at for a deterministic tiebreak
	second, err := s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobTranscribe})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	got1, _ := s.claimNextJob()
	got2, _ := s.claimNextJob()
	if got1.ID != first.ID || got2.ID != second.ID {
		t.Errorf("FIFO within priority broken: got %s then %s; want %s then %s",
			got1.ID, got2.ID, first.ID, second.ID)
	}
}

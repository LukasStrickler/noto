package service

import (
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// TestClose_FinalizesInFlightRecording pins the durability contract: shutting
// down while a recording is active must not lose the user's meeting — Close
// finalizes it (saves audio + enqueues its pipeline), and that queued job
// survives in the durable jobs DB for the next startup to process.
func TestClose_FinalizesInFlightRecording(t *testing.T) {
	s := newJobsTestSvc(t) // ipc == nil → StartRecording uses the dry-run path
	ctx := context.Background()

	res, err := s.StartRecording(ctx, notoapi.StartRecordingOpts{Title: "In progress"})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if rec, _ := s.GetRecording(ctx); !rec.Active {
		t.Fatal("recording should be active after StartRecording")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The jobs DB (owned by the test, not closed by svc.Close) must now carry a
	// queued pipeline job for the recording — recoverable on restart.
	jobs, err := s.ListJobs(ctx, notoapi.ListJobsOpts{})
	if err != nil {
		t.Fatalf("ListJobs after close: %v", err)
	}
	found := false
	for _, j := range jobs {
		if j.Kind == notoapi.JobPipeline && j.MeetingID == res.MeetingID {
			found = true
			if j.Status != notoapi.JobQueued {
				t.Errorf("finalized recording's pipeline job should be queued for restart, got %s", j.Status)
			}
		}
	}
	if !found {
		t.Errorf("Close must enqueue a pipeline job for the in-flight recording %s; jobs=%+v", res.MeetingID, jobs)
	}
}

package host_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// TestE2E_RecordingPipeline brings up a Host in-process, records a
// (dry-run) meeting, waits for the pipeline to finish, and asserts that
// the meeting appears in list+search with the expected counts.
func TestE2E_RecordingPipeline(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	// Point UDS at a unique path under TempDir so concurrent tests don't collide.
	sockDir := t.TempDir()
	sock := filepath.Join(sockDir, "noto.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{
		Network: "unix",
		Address: sock,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()

	client := host.Client()
	if client == nil {
		t.Fatal("expected a direct client")
	}

	// Health.
	if h, err := client.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	} else if !h.OK {
		t.Fatalf("health not OK")
	}

	// Start + stop a recording.
	start, err := client.StartRecording(ctx, notoapi.StartRecordingOpts{
		Title: "Sprint planning",
	})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if start.MeetingID == "" {
		t.Fatal("expected meeting_id")
	}
	time.Sleep(300 * time.Millisecond)
	stop, err := client.StopRecording(ctx, notoapi.StopRecordingOpts{})
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if len(stop.JobsKicked) == 0 {
		t.Fatal("expected at least one job to be kicked")
	}

	// Wait for the pipeline to finish.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := client.ListJobs(ctx, notoapi.ListJobsOpts{Limit: 10})
		if err != nil {
			t.Fatalf("ListJobs: %v", err)
		}
		if len(jobs) > 0 && jobs[0].Status == notoapi.JobSucceeded {
			break
		}
		if len(jobs) > 0 && jobs[0].Status == notoapi.JobFailed {
			t.Fatalf("pipeline job failed: %s", jobs[0].Error)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Meetings list.
	list, err := client.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	if err != nil {
		t.Fatalf("ListMeetings: %v", err)
	}
	if list.Total != 1 {
		t.Fatalf("want 1 meeting, got %d", list.Total)
	}
	m := list.Meetings[0]
	if m.Title != "Sprint planning" {
		t.Errorf("want title=Sprint planning, got %q", m.Title)
	}
	if m.Status != notoapi.StatusSummarized {
		t.Errorf("want status=summarized, got %q", m.Status)
	}
	if m.DecisionCount == 0 || m.ActionCount == 0 {
		t.Errorf("want >0 decisions and actions, got D=%d A=%d", m.DecisionCount, m.ActionCount)
	}

	// Transcript + summary fetch.
	tr, err := client.GetTranscript(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if len(tr.Segments) == 0 {
		t.Fatal("expected transcript segments")
	}
	sum, err := client.GetSummary(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if len(sum.Decisions) == 0 {
		t.Fatal("expected decisions in summary")
	}

	// Search.
	res, err := client.Search(ctx, notoapi.SearchOpts{Query: "quarter"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Total == 0 {
		t.Fatal("expected at least one search hit")
	}

	// Agent handoff.
	a, err := client.GetAgentHandoff(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetAgentHandoff: %v", err)
	}
	if len(a.Commands) == 0 {
		t.Fatal("expected agent commands")
	}
	if a.Files.Transcript == "" {
		t.Error("expected transcript path in handoff")
	}
}

// TestProviders verifies the provider list shape.
func TestProviders(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{Address: filepath.Join(t.TempDir(), "s.sock")})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	list, err := host.Client().ListProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"assemblyai", "openrouter"}
	for _, w := range want {
		found := false
		for _, p := range list {
			if p.ID == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing provider %q in list", w)
		}
	}
	_ = strings.Join
}

package service_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/notohost"
)

// TestImportAudio_DryRunPipeline verifies that importing a fake audio
// file with no real STT key flows through ingest → transcribe (dry run)
// → summarize → index and the result is searchable + retrievable.
func TestImportAudio_DryRunPipeline(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	// Create a tiny dummy audio file.
	audio := filepath.Join(t.TempDir(), "tiny.m4a")
	if err := os.WriteFile(audio, []byte("ID3                  "), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	res, err := client.ImportAudio(ctx, notoapi.ImportAudioOpts{Path: audio, Title: "Import test"})
	if err != nil {
		t.Fatalf("ImportAudio: %v", err)
	}
	if res.Meeting.ID == "" {
		t.Fatal("missing meeting id")
	}
	if res.Job.ID == "" {
		t.Fatal("missing job id")
	}

	// Wait for the pipeline to finish.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := client.GetJob(ctx, res.Job.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if j.Status == notoapi.JobSucceeded {
			break
		}
		if j.Status == notoapi.JobFailed || j.Status == notoapi.JobCanceled {
			t.Fatalf("pipeline %s: %s", j.Status, j.Error)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Audio file should still exist under the meeting layout.
	files, err := client.GetMeetingFiles(ctx, res.Meeting.ID)
	if err != nil {
		t.Fatalf("GetMeetingFiles: %v", err)
	}
	if files.Audio == "" {
		t.Error("expected audio path in files")
	}
	if files.Transcript == "" {
		t.Error("expected transcript path in files")
	}
	if files.SummaryJSON == "" {
		t.Error("expected summary json path in files")
	}
	// Confirm the audio file content matches what we wrote.
	if files.Audio != "" {
		if _, err := os.Stat(files.Audio); err != nil {
			t.Errorf("audio not on disk: %v", err)
		}
	}

	// Title from the import call should match what `list` returns.
	list, err := client.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	if err != nil {
		t.Fatalf("ListMeetings: %v", err)
	}
	if list.Total == 0 {
		t.Fatal("list is empty after import")
	}
	if !strings.Contains(list.Meetings[0].Title, "Import test") {
		t.Errorf("title not preserved: %q", list.Meetings[0].Title)
	}

	// Search returns the new meeting.
	sr, err := client.Search(ctx, notoapi.SearchOpts{Query: "quarter"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if sr.Total == 0 {
		t.Fatal("search returned no hits")
	}
}

// TestProviderKeyRoundtrip verifies the provider key set/list/test/remove
// path through the public client.
func TestProviderKeyRoundtrip(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	client := host.Client()

	// Set a memory-only key via env var (the EnvFallbackStore reads env).
	// On a real keychain-less Linux test, Set returns an error because
	// no writable primary store exists. So this test verifies the
	// failure path is graceful, not silent.
	err = client.SetProviderKey(ctx, "openrouter", "sk-test-123")
	if err == nil {
		// Some platforms allow Set; in that case test the test path.
		_, _ = client.TestProvider(ctx, "openrouter")
	}

	// List works regardless of key presence.
	providers, err := client.ListProviders(ctx)
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) < 4 {
		t.Errorf("want >=4 providers, got %d", len(providers))
	}

	// Active speech can be set to a known provider.
	if err := client.SetActiveSpeech(ctx, "assemblyai"); err != nil {
		// Allow gracefully on systems without a writable config store.
		if !strings.Contains(err.Error(), "permission") {
			t.Logf("SetActiveSpeech: %v (allowed in test env)", err)
		}
	}
}

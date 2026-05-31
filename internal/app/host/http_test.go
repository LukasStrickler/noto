package host_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/transport/apiclient"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// TestE2E_HTTP runs the full pipeline through the HTTP transport (UDS).
// This proves the wire encoding matches what the server emits.
func TestE2E_HTTP(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{
		Network:    "unix",
		Address:    sock,
		ServerOnly: true, // force HTTP client path
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()

	client := apiclient.NewHTTP(apiclient.HTTPOptions{SocketPath: sock})

	// Health.
	if _, err := client.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	// Import an audio file and wait for the pipeline.
	audioPath := filepath.Join(t.TempDir(), "fake.m4a")
	if err := os.WriteFile(audioPath, []byte("fake-audio-content-for-testing"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	res, err := client.ImportAudio(ctx, notoapi.ImportAudioOpts{Path: audioPath, Title: "HTTP E2E"})
	if err != nil {
		t.Fatalf("ImportAudio: %v", err)
	}
	if res.Meeting.ID == "" {
		t.Fatal("expected meeting id")
	}
	if res.Job.ID == "" {
		t.Fatal("expected pipeline job id")
	}

	// Poll until terminal.
	for {
		job, err := client.GetJob(ctx, res.Job.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if job.Status == notoapi.JobSucceeded {
			break
		}
		if job.Status == notoapi.JobFailed {
			t.Fatalf("pipeline failed: %s", job.Error)
		}
		select {
		case <-ctx.Done():
			t.Fatal("timeout waiting for pipeline")
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Verify meeting + transcript + summary roundtrip the wire.
	m, err := client.GetMeeting(ctx, res.Meeting.ID)
	if err != nil {
		t.Fatalf("GetMeeting: %v", err)
	}
	if m.Status != notoapi.StatusSummarized {
		t.Errorf("want summarized, got %s", m.Status)
	}

	tr, err := client.GetTranscript(ctx, res.Meeting.ID)
	if err != nil {
		t.Fatalf("GetTranscript: %v", err)
	}
	if len(tr.Segments) == 0 {
		t.Fatal("transcript empty")
	}

	sum, err := client.GetSummary(ctx, res.Meeting.ID)
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}
	if sum.ShortSummary == "" {
		t.Fatal("summary empty")
	}

	// Storage status + verify job.
	st, err := client.GetStorage(ctx)
	if err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if st.MeetingCount < 1 {
		t.Fatalf("want >=1 meeting, got %d", st.MeetingCount)
	}

	if _, err := client.VerifyStorage(ctx); err != nil {
		t.Fatalf("VerifyStorage: %v", err)
	}
}

// TestE2E_TCPWithToken proves the TCP transport + bearer-token auth.
func TestE2E_TCPWithToken(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{
		Network:    "tcp",
		Address:    "127.0.0.1:0",
		ServerOnly: true,
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("Start tcp: %v", err)
	}
	defer host.Close()

	addr := host.Addr()
	token := host.Token()

	// Without a token, requests should fail.
	noAuth := apiclient.NewHTTP(apiclient.HTTPOptions{BaseURL: "http://" + addr})
	if _, err := noAuth.Health(ctx); err == nil {
		t.Fatal("expected auth failure without token")
	}

	// With the right token, requests work.
	good := apiclient.NewHTTP(apiclient.HTTPOptions{BaseURL: "http://" + addr, Token: token})
	if _, err := good.Health(ctx); err != nil {
		t.Fatalf("Health with token: %v", err)
	}
}

// TestEventStream verifies the SSE event stream emits job lifecycle
// events and that subscribers see the recorder state change.
func TestEventStream(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()

	client := host.Client()
	events, err := client.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}

	// Trigger a recording start so a recorder event fires.
	if _, err := client.StartRecording(ctx, notoapi.StartRecordingOpts{Title: "ev"}); err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	defer client.StopRecording(ctx, notoapi.StopRecordingOpts{})

	deadline := time.After(2 * time.Second)
	gotRecorder := false
	for !gotRecorder {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("events channel closed")
			}
			if ev.Kind == notoapi.EventRecorder && ev.Recorder != nil && ev.Recorder.Active {
				gotRecorder = true
			}
		case <-deadline:
			t.Fatal("did not receive recorder event")
		}
	}
}

// TestIdleQuiescence asserts that with no active recording or jobs,
// the service's background work is essentially zero — no allocations
// after a short settling period. This is a heuristic for "wasted CPU
// when idle".
func TestIdleQuiescence(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := host.Start(ctx, host.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()

	// Settle.
	time.Sleep(300 * time.Millisecond)
	runtime.GC()

	var s1 runtime.MemStats
	runtime.ReadMemStats(&s1)

	// Sleep without doing anything. Workers should park.
	time.Sleep(2 * time.Second)
	runtime.GC()

	var s2 runtime.MemStats
	runtime.ReadMemStats(&s2)

	// Heap should not balloon while idle. We allow a 256KB slack for
	// the SQLite connection's lazy buffers and the SSE subscriber map.
	delta := int64(s2.HeapAlloc) - int64(s1.HeapAlloc)
	if delta > 256*1024 {
		t.Errorf("idle heap grew by %d bytes (expected ~0)", delta)
	}

	// And confirm Goroutines didn't multiply.
	if runtime.NumGoroutine() > 30 {
		t.Errorf("too many goroutines while idle: %d", runtime.NumGoroutine())
	}
}

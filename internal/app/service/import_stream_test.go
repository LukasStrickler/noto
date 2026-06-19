package service_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/repo"
)

// TestImportAudioStream verifies the upload path: bytes streamed in are stored
// under the meeting layout and a pipeline job is queued — the server-side half
// of remote-mode capture.
func TestImportAudioStream(t *testing.T) {
	dir := t.TempDir()
	svc := newTestSvc(t, repo.NewLocal(dir))
	ctx := context.Background()

	data := []byte("ID3 fake uploaded audio bytes \x00\x01\x02")
	m, job, err := svc.ImportAudioStream(ctx, bytes.NewReader(data), "team-sync.m4a", "Remote upload")
	if err != nil {
		t.Fatalf("ImportAudioStream: %v", err)
	}
	if m.ID == "" || job.ID == "" {
		t.Fatal("missing meeting/job id")
	}
	if m.Title != "Remote upload" {
		t.Errorf("title = %q, want Remote upload", m.Title)
	}

	files, err := svc.GetMeetingFiles(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMeetingFiles: %v", err)
	}
	if files.Audio == "" {
		t.Fatal("no audio path recorded")
	}
	got, err := os.ReadFile(files.Audio)
	if err != nil {
		t.Fatalf("read stored audio: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("stored audio differs from uploaded bytes")
	}
}

func TestImportAudioStreamDefaultsTitleAndExt(t *testing.T) {
	dir := t.TempDir()
	svc := newTestSvc(t, repo.NewLocal(dir))
	ctx := context.Background()

	// Empty title → derived from filename; no extension → defaults to .m4a.
	m, _, err := svc.ImportAudioStream(ctx, bytes.NewReader([]byte("x")), "standup.wav", "")
	if err != nil {
		t.Fatalf("ImportAudioStream: %v", err)
	}
	if m.Title != "standup" {
		t.Errorf("derived title = %q, want standup", m.Title)
	}
	files, _ := svc.GetMeetingFiles(ctx, m.ID)
	if files.Audio == "" {
		t.Error("expected audio path")
	}
}

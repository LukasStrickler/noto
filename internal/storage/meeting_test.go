package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
)

func TestWriteManifest(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: "ver_001",
				CreatedAt: time.Now(),
				Reason:    "initial",
				Checksum:  "sha256:test",
			},
		},
	}

	if err := WriteManifest(layout, m); err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}

	if _, err := os.Stat(layout.ManifestPath); err != nil {
		t.Errorf("manifest.json was not created: %v", err)
	}

	if _, err := os.Stat(layout.ChecksumPath); err != nil {
		t.Errorf("checksum.sha256 was not created: %v", err)
	}
}

func TestReadManifest(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: "ver_001",
				CreatedAt: time.Now(),
				Reason:    "initial",
				Checksum:  "sha256:test",
			},
		},
	}

	if err := WriteManifest(layout, m); err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}

	read, err := ReadManifest(layout)
	if err != nil {
		t.Fatalf("ReadManifest failed: %v", err)
	}

	if read.MeetingID != m.MeetingID {
		t.Errorf("MeetingID = %q, want %q", read.MeetingID, m.MeetingID)
	}
	if read.CurrentVersionID != m.CurrentVersionID {
		t.Errorf("CurrentVersionID = %q, want %q", read.CurrentVersionID, m.CurrentVersionID)
	}
}

func TestReadManifestNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	_, err = ReadManifest(layout)
	if err == nil {
		t.Error("Expected error for missing manifest")
	}
}

func TestWriteTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	conf := 0.95
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       meetingID.String(),
		Provider:        artifacts.TranscriptProvider{ID: "test-provider"},
		Speakers:        []artifacts.Speaker{{ID: "spk_1", Label: "Speaker 1"}},
		Segments:        []artifacts.Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello", StartSeconds: 0, EndSeconds: 1, Confidence: &conf}},
		Capabilities:    artifacts.TranscriptCapabilities{WordTimestamps: true},
	}

	if err := WriteTranscript(layout, transcript); err != nil {
		t.Fatalf("WriteTranscript failed: %v", err)
	}

	if _, err := os.Stat(layout.TranscriptPath); err != nil {
		t.Errorf("transcript.diarized.json was not created: %v", err)
	}
}

func TestReadTranscript(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	conf := 0.95
	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       meetingID.String(),
		Provider:        artifacts.TranscriptProvider{ID: "test-provider"},
		Speakers:        []artifacts.Speaker{{ID: "spk_1", Label: "Speaker 1"}},
		Segments:        []artifacts.Segment{{ID: "seg_1", SpeakerID: "spk_1", Text: "Hello", StartSeconds: 0, EndSeconds: 1, Confidence: &conf}},
		Capabilities:    artifacts.TranscriptCapabilities{WordTimestamps: true},
	}

	if err := WriteTranscript(layout, transcript); err != nil {
		t.Fatalf("WriteTranscript failed: %v", err)
	}

	read, err := ReadTranscript(layout)
	if err != nil {
		t.Fatalf("ReadTranscript failed: %v", err)
	}

	if len(read.Segments) != 1 {
		t.Errorf("Segments count = %d, want 1", len(read.Segments))
	}
	if read.Segments[0].Text != "Hello" {
		t.Errorf("Segment text = %q, want %q", read.Segments[0].Text, "Hello")
	}
}

func TestWriteSummary(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	summaryMD := "# Summary\n\nThis is a test summary."
	if err := WriteSummary(layout, summaryMD); err != nil {
		t.Fatalf("WriteSummary failed: %v", err)
	}

	if _, err := os.Stat(layout.SummaryPath); err != nil {
		t.Errorf("summary.v1.md was not created: %v", err)
	}
}

func TestReadSummary(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	summaryMD := "# Summary\n\nThis is a test summary."
	if err := WriteSummary(layout, summaryMD); err != nil {
		t.Fatalf("WriteSummary failed: %v", err)
	}

	read, err := ReadSummary(layout)
	if err != nil {
		t.Fatalf("ReadSummary failed: %v", err)
	}

	if read != summaryMD {
		t.Errorf("Summary = %q, want %q", read, summaryMD)
	}
}

func TestAtomicWriteOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	m1 := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
		},
	}

	if err := WriteManifest(layout, m1); err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}

	data1, _ := os.ReadFile(layout.ManifestPath)

	m2 := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_002",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_002", CreatedAt: time.Now(), Reason: "updated"},
		},
	}

	if err := WriteManifest(layout, m2); err != nil {
		t.Fatalf("WriteManifest failed on second write: %v", err)
	}

	data2, _ := os.ReadFile(layout.ManifestPath)

	if string(data1) == string(data2) {
		t.Error("Expected different content after second write")
	}
}

func TestListMeetings(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")

	meetingID1 := uuid.New()
	layout1, _ := LayoutFor(recordingsDir, meetingID1)
	EnsureDirs(layout1)
	m1 := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID1.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
		},
	}
	WriteManifest(layout1, m1)

	versionDir := layout1.VersionDir("ver_001")
	os.MkdirAll(versionDir, 0755)
	versionManifest := map[string]any{
		"schema_version": "meeting.v1",
		"id":             meetingID1.String(),
		"title":          "Test Meeting 1",
	}
	vmData, _ := json.Marshal(versionManifest)
	os.WriteFile(layout1.VersionManifestPath("ver_001"), vmData, 0644)

	meetingID2 := uuid.New()
	layout2, _ := LayoutFor(recordingsDir, meetingID2)
	EnsureDirs(layout2)
	m2 := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID2.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now().Add(-time.Hour), Reason: "initial"},
		},
	}
	WriteManifest(layout2, m2)

	versionDir2 := layout2.VersionDir("ver_001")
	os.MkdirAll(versionDir2, 0755)
	versionManifest2 := map[string]any{
		"schema_version": "meeting.v1",
		"id":             meetingID2.String(),
		"title":          "Test Meeting 2",
	}
	vmData2, _ := json.Marshal(versionManifest2)
	os.WriteFile(layout2.VersionManifestPath("ver_001"), vmData2, 0644)

	refs, err := ListMeetings(recordingsDir)
	if err != nil {
		t.Fatalf("ListMeetings failed: %v", err)
	}

	if len(refs) != 2 {
		t.Errorf("Expected 2 meetings, got %d", len(refs))
	}

	if refs[0].MeetingID != meetingID1 {
		t.Errorf("Expected first meeting to be %s, got %s", meetingID1, refs[0].MeetingID)
	}
}

func TestGetMeeting(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial", Checksum: "sha256:test"},
		},
	}
	WriteManifest(layout, m)

	versionDir := layout.VersionDir("ver_001")
	os.MkdirAll(versionDir, 0755)
	versionManifest := map[string]any{
		"schema_version": "meeting.v1",
		"id":             meetingID.String(),
		"title":          "Test Meeting",
	}
	vmData, _ := json.Marshal(versionManifest)
	os.WriteFile(layout.VersionManifestPath("ver_001"), vmData, 0644)

	os.WriteFile(layout.SummaryPath, []byte("Short summary here"), 0644)

	meeting, err := GetMeeting(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("GetMeeting failed: %v", err)
	}

	if meeting.MeetingID != meetingID {
		t.Errorf("MeetingID = %v, want %v", meeting.MeetingID, meetingID)
	}
	if meeting.CurrentVersionID != "ver_001" {
		t.Errorf("CurrentVersionID = %q, want %q", meeting.CurrentVersionID, "ver_001")
	}
	if len(meeting.Versions) != 1 {
		t.Errorf("Versions count = %d, want 1", len(meeting.Versions))
	}
}

func TestCreateVersion(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	versionID, err := CreateVersion(layout, "test reason")
	if err != nil {
		t.Fatalf("CreateVersion failed: %v", err)
	}

	if versionID == "" {
		t.Error("Expected non-empty version ID")
	}

	vmPath := layout.VersionManifestPath(versionID)
	if _, err := os.Stat(vmPath); err != nil {
		t.Errorf("Version manifest was not created at %s: %v", vmPath, err)
	}
}

func TestVerifyMeetingChecksums(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
		},
	}
	WriteManifest(layout, m)

	if err := VerifyMeetingChecksums(layout); err != nil {
		t.Errorf("VerifyMeetingChecksums failed: %v", err)
	}
}

func TestDeleteMeeting(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "recordings")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	manifest := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []artifacts.ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
		},
	}
	WriteManifest(layout, manifest)

	if err := DeleteMeeting(layout); err != nil {
		t.Fatalf("DeleteMeeting failed: %v", err)
	}

	if _, err := os.Stat(layout.MeetingDir); !os.IsNotExist(err) {
		t.Error("Expected meeting directory to be deleted")
	}
}
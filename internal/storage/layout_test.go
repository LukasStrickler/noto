package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestLayoutFor(t *testing.T) {
	recordingsDir := "/tmp/noto-test"
	meetingID := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if layout.RecordingsDir != recordingsDir {
		t.Errorf("RecordingsDir = %q, want %q", layout.RecordingsDir, recordingsDir)
	}
	if layout.MeetingID != meetingID {
		t.Errorf("MeetingID = %v, want %v", layout.MeetingID, meetingID)
	}
	if layout.Year == "" {
		t.Error("Year is empty")
	}
	if layout.Month == "" {
		t.Error("Month is empty")
	}

	expectedMeetingDir := filepath.Join(recordingsDir, "meetings", layout.Year, layout.Month, meetingID.String())
	if layout.MeetingDir != expectedMeetingDir {
		t.Errorf("MeetingDir = %q, want %q", layout.MeetingDir, expectedMeetingDir)
	}

	if layout.VersionsDir != filepath.Join(expectedMeetingDir, "versions") {
		t.Errorf("VersionsDir incorrect")
	}
	if layout.TmpDir != filepath.Join(expectedMeetingDir, ".tmp") {
		t.Errorf("TmpDir incorrect")
	}

	if layout.ManifestPath != filepath.Join(expectedMeetingDir, "manifest.json") {
		t.Errorf("ManifestPath incorrect")
	}
	if layout.AudioPath != filepath.Join(expectedMeetingDir, "audio.m4a") {
		t.Errorf("AudioPath incorrect")
	}
	if layout.TranscriptPath != filepath.Join(expectedMeetingDir, "transcript.diarized.json") {
		t.Errorf("TranscriptPath incorrect")
	}
	if layout.SummaryPath != filepath.Join(expectedMeetingDir, "summary.v1.md") {
		t.Errorf("SummaryPath incorrect")
	}
	if layout.ChecksumPath != filepath.Join(expectedMeetingDir, "checksum.sha256") {
		t.Errorf("ChecksumPath incorrect")
	}
}

func TestLayoutForEmptyRecordingsDir(t *testing.T) {
	_, err := LayoutFor("", uuid.New())
	if err == nil {
		t.Error("Expected error for empty recordings_dir")
	}
}

func TestVersionDir(t *testing.T) {
	layout := DirectoryLayout{
		VersionsDir: "/tmp/noto/meetings/2026/04/id/versions",
	}
	versionID := "ver_20260424_142001_c932"
	got := layout.VersionDir(versionID)
	want := "/tmp/noto/meetings/2026/04/id/versions/ver_20260424_142001_c932"
	if got != want {
		t.Errorf("VersionDir = %q, want %q", got, want)
	}
}

func TestVersionPaths(t *testing.T) {
	layout := DirectoryLayout{
		VersionsDir: "/tmp/noto/meetings/2026/04/id/versions",
	}
	versionID := "ver_20260424_142001_c932"

	if got := layout.VersionManifestPath(versionID); got != filepath.Join(layout.VersionDir(versionID), "manifest.json") {
		t.Errorf("VersionManifestPath incorrect")
	}
	if got := layout.VersionAudioPath(versionID); got != filepath.Join(layout.VersionDir(versionID), "audio.m4a") {
		t.Errorf("VersionAudioPath incorrect")
	}
	if got := layout.VersionTranscriptPath(versionID); got != filepath.Join(layout.VersionDir(versionID), "transcript.diarized.json") {
		t.Errorf("VersionTranscriptPath incorrect")
	}
	if got := layout.VersionSummaryPath(versionID); got != filepath.Join(layout.VersionDir(versionID), "summary.v1.md") {
		t.Errorf("VersionSummaryPath incorrect")
	}
	if got := layout.VersionChecksumPath(versionID); got != filepath.Join(layout.VersionDir(versionID), "checksum.sha256") {
		t.Errorf("VersionChecksumPath incorrect")
	}
}

func TestEnsureDirs(t *testing.T) {
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

	for _, dir := range []string{layout.MeetingDir, layout.VersionsDir, layout.TmpDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("Directory %q does not exist: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("Path %q is not a directory", dir)
		}
	}

	info, err := os.Stat(layout.MeetingDir)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Errorf("MeetingDir permissions incorrect, got %o, want 0755", info.Mode().Perm())
	}
}

func TestEnsureDirsCreatesParentDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "a", "b", "c")
	meetingID := uuid.New()

	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if err := EnsureDirs(layout); err != nil {
		t.Fatalf("EnsureDirs failed: %v", err)
	}

	if _, err := os.Stat(layout.MeetingDir); err != nil {
		t.Errorf("MeetingDir does not exist: %v", err)
	}
}

func TestValidateLayout(t *testing.T) {
	validLayout := DirectoryLayout{
		RecordingsDir: "/tmp/noto",
		MeetingID:     uuid.New(),
		MeetingDir:   "/tmp/noto/meetings/2026/04/id",
	}

	if err := ValidateLayout(validLayout); err != nil {
		t.Errorf("Expected valid layout, got error: %v", err)
	}

	emptyRecordingsDir := validLayout
	emptyRecordingsDir.RecordingsDir = ""
	if err := ValidateLayout(emptyRecordingsDir); err == nil {
		t.Error("Expected error for empty recordings_dir")
	}

	nilUUID := validLayout
	nilUUID.MeetingID = uuid.Nil
	if err := ValidateLayout(nilUUID); err == nil {
		t.Error("Expected error for nil meeting_id")
	}

	emptyMeetingDir := validLayout
	emptyMeetingDir.MeetingDir = ""
	if err := ValidateLayout(emptyMeetingDir); err == nil {
		t.Error("Expected error for empty meeting_dir")
	}
}

func TestParseMeetingID(t *testing.T) {
	meetingID := uuid.MustParse("123e4567-e89b-12d3-a456-426614174000")
	dir := filepath.Join("meetings", "2026", "04", meetingID.String())

	parsed, err := ParseMeetingID(dir)
	if err != nil {
		t.Fatalf("ParseMeetingID failed: %v", err)
	}
	if parsed != meetingID {
		t.Errorf("Parsed ID = %v, want %v", parsed, meetingID)
	}

	_, err = ParseMeetingID("invalid-uuid")
	if err == nil {
		t.Error("Expected error for invalid UUID")
	}
}

func TestExtractDateFromPath(t *testing.T) {
	meetingPath := "/tmp/Noto/meetings/2026/04/123e4567-e89b-12d3-a456-426614174000"

	year, month, err := ExtractDateFromPath(meetingPath)
	if err != nil {
		t.Fatalf("ExtractDateFromPath failed: %v", err)
	}
	if year != "2026" {
		t.Errorf("year = %q, want %q", year, "2026")
	}
	if month != "04" {
		t.Errorf("month = %q, want %q", month, "04")
	}

	_, _, err = ExtractDateFromPath("/tmp/invalid")
	if err == nil {
		t.Error("Expected error for invalid path")
	}
}
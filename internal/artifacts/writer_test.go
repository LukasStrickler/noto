package artifacts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/storage"
)

func TestManifestWriter_WriteManifest(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "noto")

	writer := NewManifestWriter(recordingsDir)

	meetingID := uuid.New()
	manifest := &MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []ManifestVersion{
			{
				VersionID: "ver_001",
				CreatedAt: time.Now(),
				Reason:    "initial",
			},
		},
	}

	err := writer.WriteManifest(meetingID, manifest)
	if err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}

	layout, err := storage.LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if _, err := os.Stat(layout.ManifestPath); err != nil {
		t.Errorf("manifest.json not created: %v", err)
	}

	if _, err := os.Stat(layout.ChecksumPath); err != nil {
		t.Errorf("checksum.sha256 not created: %v", err)
	}
}

func TestWritePipeline_WriteAll(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "noto")

	pipeline := NewWritePipeline(recordingsDir)

	meetingID := uuid.New()

	transcriptJSON := []byte(`{"schema_version":"transcript.v1","meeting_id":"test"}`)
	summaryJSON := []byte(`{"schema_version":"summary.v1","meeting_id":"test"}`)

	artifacts := []ArtifactToWrite{
		{Kind: KindTranscript, Content: transcriptJSON, Path: "transcript.diarized.json"},
		{Kind: KindSummary, Content: summaryJSON, Path: "summary.v1.md"},
	}

	manifest := &MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []ManifestVersion{
			{
				VersionID: "ver_001",
				CreatedAt: time.Now(),
				Reason:    "initial",
			},
		},
	}

	err := pipeline.WriteAll(meetingID, artifacts, manifest)
	if err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}

	layout, err := storage.LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(layout.MeetingDir, "transcript.diarized.json")); err != nil {
		t.Errorf("transcript.diarized.json not created: %v", err)
	}

	if _, err := os.Stat(filepath.Join(layout.MeetingDir, "summary.v1.md")); err != nil {
		t.Errorf("summary.v1.md not created: %v", err)
	}

	if _, err := os.Stat(layout.ChecksumPath); err != nil {
		t.Errorf("checksums.json not created: %v", err)
	}

	if _, err := os.Stat(layout.ManifestPath); err != nil {
		t.Errorf("manifest.json not created: %v", err)
	}
}

func TestVersionArtifact_CreateVersion(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "noto")

	saver := NewManifestWriter(recordingsDir)
	versioner := NewVersionArtifact(recordingsDir)

	meetingID := uuid.New()

	manifest := &MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []ManifestVersion{
			{
				VersionID: "ver_001",
				CreatedAt: time.Now(),
				Reason:    "initial",
			},
		},
	}

	err := saver.WriteManifest(meetingID, manifest)
	if err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}

	layout, err := storage.LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	now := time.Now()
	versionDir := layout.VersionDir("ver_001")
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	transcriptPath := layout.VersionTranscriptPath("ver_001")
	if err := os.WriteFile(transcriptPath, []byte(`{"test": "transcript"}`), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	summaryPath := layout.VersionSummaryPath("ver_001")
	if err := os.WriteFile(summaryPath, []byte("# Summary"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	newVersionID, err := versioner.CreateVersion(meetingID, ReasonSummaryCreated)
	if err != nil {
		t.Fatalf("CreateVersion failed: %v", err)
	}

	if newVersionID == "" {
		t.Error("expected non-empty version ID")
	}

	newLayout, err := storage.LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	if _, err := os.Stat(newLayout.VersionManifestPath(newVersionID)); err != nil {
		t.Errorf("version manifest.json not created: %v", err)
	}

	manifestData, err := os.ReadFile(layout.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var updatedManifest MeetingManifest
	if err := json.Unmarshal(manifestData, &updatedManifest); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if updatedManifest.CurrentVersionID != newVersionID {
		t.Errorf("expected current version %s, got %s", newVersionID, updatedManifest.CurrentVersionID)
	}

	if len(updatedManifest.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(updatedManifest.Versions))
	}
}

func TestVerifyChecksums_VerifyAll(t *testing.T) {
	t.Run("valid checksums", func(t *testing.T) {
		tmpDir := t.TempDir()
		recordingsDir := filepath.Join(tmpDir, "noto")

		pipeline := NewWritePipeline(recordingsDir)

		meetingID := uuid.New()

		artifacts := []ArtifactToWrite{
			{Kind: KindTranscript, Content: []byte(`{"test": "data"}`), Path: "transcript.diarized.json"},
		}

		manifest := &MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        meetingID.String(),
			CurrentVersionID: "ver_001",
			Versions: []ManifestVersion{
				{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
			},
		}

		err := pipeline.WriteAll(meetingID, artifacts, manifest)
		if err != nil {
			t.Fatalf("WriteAll failed: %v", err)
		}

		verifier := NewVerifyChecksums(recordingsDir)
		result, err := verifier.VerifyAll(meetingID)
		if err != nil {
			t.Fatalf("VerifyAll failed: %v", err)
		}

		if !result.Valid {
			t.Errorf("expected valid result, got errors: %v", result.Errors)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		tmpDir := t.TempDir()
		recordingsDir := filepath.Join(tmpDir, "noto")

		writer := NewManifestWriter(recordingsDir)
		verifier := NewVerifyChecksums(recordingsDir)

		meetingID := uuid.New()

		manifest := &MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        meetingID.String(),
			CurrentVersionID: "ver_001",
			Versions: []ManifestVersion{
				{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
			},
		}

		err := writer.WriteManifest(meetingID, manifest)
		if err != nil {
			t.Fatalf("WriteManifest failed: %v", err)
		}

		layout, err := storage.LayoutFor(recordingsDir, meetingID)
		if err != nil {
			t.Fatalf("LayoutFor failed: %v", err)
		}

		originalData, err := os.ReadFile(layout.ManifestPath)
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}

		if err := os.WriteFile(layout.ManifestPath, []byte(`{"tampered": true}`), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		result, err := verifier.VerifyAll(meetingID)
		if err != nil {
			t.Fatalf("VerifyAll failed: %v", err)
		}

		if result.Valid {
			t.Error("expected invalid result after tampering")
		}

		if len(result.Errors) == 0 {
			t.Error("expected at least one error")
		}

		os.WriteFile(layout.ManifestPath, originalData, 0644)
	})
}

func TestImportAudio_Import(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "noto")

	audioFile := filepath.Join(tmpDir, "test.m4a")
	audioContent := []byte("fake audio content")
	if err := os.WriteFile(audioFile, audioContent, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	importer := NewImportAudio(recordingsDir)
	meetingID := uuid.New()

	result, err := importer.Import(meetingID, audioFile)
	if err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	if result.AudioMetadata == nil {
		t.Error("expected non-nil AudioMetadata")
	}

	if result.AudioMetadata.SHA256 == "" {
		t.Error("expected non-empty SHA256")
	}

	if result.VersionID == "" {
		t.Error("expected non-empty VersionID")
	}

	expectedChecksum := "sha256:" + computeSHA256Hex(audioContent)
	if result.AudioMetadata.SHA256 != expectedChecksum {
		t.Errorf("expected checksum %s, got %s", expectedChecksum, result.AudioMetadata.SHA256)
	}

	layout, err := storage.LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	versionAudioPath := filepath.Join(layout.VersionDir(result.VersionID), "audio", "recording.m4a")
	if _, err := os.Stat(versionAudioPath); err != nil {
		t.Errorf("audio file not copied to version directory: %v", err)
	}

	audioMetaPath := filepath.Join(layout.VersionDir(result.VersionID), "audio.json")
	if _, err := os.Stat(audioMetaPath); err != nil {
		t.Errorf("audio.json not created: %v", err)
	}
}

func TestWritePipeline_ManifestWrittenLast(t *testing.T) {
	tmpDir := t.TempDir()
	recordingsDir := filepath.Join(tmpDir, "noto")

	pipeline := NewWritePipeline(recordingsDir)

	meetingID := uuid.New()

	artifacts := []ArtifactToWrite{
		{Kind: KindTranscript, Content: []byte(`{"test": "data"}`), Path: "transcript.diarized.json"},
	}

	manifest := &MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: "ver_001",
		Versions: []ManifestVersion{
			{VersionID: "ver_001", CreatedAt: time.Now(), Reason: "initial"},
		},
	}

	err := pipeline.WriteAll(meetingID, artifacts, manifest)
	if err != nil {
		t.Fatalf("WriteAll failed: %v", err)
	}

	layout, err := storage.LayoutFor(recordingsDir, meetingID)
	if err != nil {
		t.Fatalf("LayoutFor failed: %v", err)
	}

	checksumData, err := os.ReadFile(layout.ChecksumPath)
	if err != nil {
		t.Fatalf("ReadFile checksum failed: %v", err)
	}

	var checksums struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(checksumData, &checksums); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	manifestData, err := os.ReadFile(layout.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile manifest failed: %v", err)
	}

	manifestChecksum := ComputeChecksum(manifestData)

	checksumMatchesManifest := false
	for _, checksum := range checksums.Files {
		if checksum == manifestChecksum {
			checksumMatchesManifest = true
			break
		}
	}

	if checksumMatchesManifest {
		t.Error("manifest checksum should not be in checksums.json files")
	}
}

func computeSHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func TestArtifactToWrite_Path(t *testing.T) {
	artifact := ArtifactToWrite{
		Kind:    KindTranscript,
		Content: []byte(`{"test": true}`),
		Path:    "transcript.diarized.json",
	}

	if artifact.Path != "transcript.diarized.json" {
		t.Errorf("expected path 'transcript.diarized.json', got '%s'", artifact.Path)
	}
}

func TestVersionReason_Values(t *testing.T) {
	reasons := []VersionReason{
		ReasonSummaryCreated,
		ReasonTranscriptCreated,
		ReasonAudioImported,
		ReasonSpeakerRenamed,
		ReasonEdited,
	}

	expected := []string{
		"summary_created",
		"transcript_created",
		"audio_imported",
		"speaker_renamed",
		"edited",
	}

	for i, reason := range reasons {
		if string(reason) != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], reason)
		}
	}
}

func TestVerificationResult_Valid(t *testing.T) {
	result := &VerificationResult{
		Valid: true,
		Errors: []VerificationError{},
	}

	if !result.Valid {
		t.Error("expected result to be valid")
	}
}

func TestVerificationError_Fields(t *testing.T) {
	err := VerificationError{
		Path:     "/path/to/file",
		Expected: "sha256:abc123",
		Actual:   "sha256:def456",
	}

	if err.Path != "/path/to/file" {
		t.Errorf("expected path '/path/to/file', got '%s'", err.Path)
	}

	if err.Expected != "sha256:abc123" {
		t.Errorf("expected expected 'sha256:abc123', got '%s'", err.Expected)
	}

	if err.Actual != "sha256:def456" {
		t.Errorf("expected actual 'sha256:def456', got '%s'", err.Actual)
	}
}

func TestImportResult_Fields(t *testing.T) {
	audioMeta := &AudioMetadata{
		SchemaVersion: "audio-asset.v1",
		MeetingID:     "mtg_123",
		AssetID:       "aud_456",
	}

	result := &ImportResult{
		AudioMetadata: audioMeta,
		VersionID:     "ver_789",
	}

	if result.AudioMetadata.MeetingID != "mtg_123" {
		t.Errorf("expected meeting_id 'mtg_123', got '%s'", result.AudioMetadata.MeetingID)
	}

	if result.VersionID != "ver_789" {
		t.Errorf("expected version_id 'ver_789', got '%s'", result.VersionID)
	}
}
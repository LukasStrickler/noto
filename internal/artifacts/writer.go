package artifacts

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/storage"
)

type ManifestWriter struct {
	recordingsDir string
}

func NewManifestWriter(recordingsDir string) *ManifestWriter {
	return &ManifestWriter{recordingsDir: recordingsDir}
}

func (mw *ManifestWriter) WriteManifest(meetingID uuid.UUID, manifest *MeetingManifest) error {
	layout, err := storage.LayoutFor(mw.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	if err := storage.EnsureDirs(layout); err != nil {
		return err
	}

	return mw.writeManifestAtomic(layout, manifest)
}

func (mw *ManifestWriter) writeManifestAtomic(layout storage.DirectoryLayout, manifest *MeetingManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return storage.ErrWriteFailed(layout.ManifestPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "manifest.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return storage.ErrWriteFailed(tmpPath, err)
	}

	checksum := ComputeChecksum(data)
	checksumPath := layout.ChecksumPath
	if err := os.WriteFile(checksumPath, []byte(checksum), 0644); err != nil {
		os.Remove(tmpPath)
		return storage.ErrWriteFailed(checksumPath, err)
	}

	if err := os.Rename(tmpPath, layout.ManifestPath); err != nil {
		os.Remove(tmpPath)
		return storage.ErrAtomicWrite(layout.ManifestPath, err)
	}

	return nil
}

type WritePipeline struct {
	recordingsDir string
}

func NewWritePipeline(recordingsDir string) *WritePipeline {
	return &WritePipeline{recordingsDir: recordingsDir}
}

type ArtifactToWrite struct {
	Kind    ArtifactKind
	Content []byte
	Path    string
}

func (wp *WritePipeline) WriteAll(meetingID uuid.UUID, artifacts []ArtifactToWrite, manifest *MeetingManifest) error {
	layout, err := storage.LayoutFor(wp.recordingsDir, meetingID)
	if err != nil {
		return err
	}

	if err := storage.EnsureDirs(layout); err != nil {
		return err
	}

	for _, artifact := range artifacts {
		tmpPath := filepath.Join(layout.TmpDir, artifact.Path+".tmp")
		if err := os.WriteFile(tmpPath, artifact.Content, 0644); err != nil {
			return storage.ErrWriteFailed(tmpPath, err)
		}
	}

	for _, artifact := range artifacts {
		tmpPath := filepath.Join(layout.TmpDir, artifact.Path+".tmp")
		finalPath := filepath.Join(layout.MeetingDir, artifact.Path)

		if err := os.MkdirAll(filepath.Dir(finalPath), 0755); err != nil {
			return storage.ErrDirCreate(filepath.Dir(finalPath), err)
		}

		if err := os.Rename(tmpPath, finalPath); err != nil {
			return storage.ErrAtomicWrite(finalPath, err)
		}
	}

	checksums := make(map[string]string)
	for _, artifact := range artifacts {
		checksum, err := wp.computeChecksumForFile(filepath.Join(layout.MeetingDir, artifact.Path))
		if err != nil {
			return err
		}
		checksums[artifact.Path] = checksum
	}

	if err := wp.writeChecksumManifest(layout, checksums); err != nil {
		return err
	}

	return wp.writeManifestAtomic(layout, manifest)
}

func (wp *WritePipeline) computeChecksumForFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", storage.ErrReadFailed(path, err)
	}
	return ComputeChecksum(data), nil
}

func (wp *WritePipeline) writeChecksumManifest(layout storage.DirectoryLayout, checksums map[string]string) error {
	checksumData := map[string]interface{}{
		"schema_version": "checksums.v1",
		"algorithm":      "sha256",
		"files":          checksums,
	}

	data, err := json.MarshalIndent(checksumData, "", "  ")
	if err != nil {
		return storage.ErrWriteFailed(layout.ChecksumPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "checksums.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return storage.ErrWriteFailed(tmpPath, err)
	}

	if err := os.Rename(tmpPath, layout.ChecksumPath); err != nil {
		os.Remove(tmpPath)
		return storage.ErrAtomicWrite(layout.ChecksumPath, err)
	}

	return nil
}

func (wp *WritePipeline) writeManifestAtomic(layout storage.DirectoryLayout, manifest *MeetingManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return storage.ErrWriteFailed(layout.ManifestPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "manifest.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return storage.ErrWriteFailed(tmpPath, err)
	}

	checksum := ComputeChecksum(data)
	checksumPath := layout.ChecksumPath
	if err := os.WriteFile(checksumPath, []byte(checksum), 0644); err != nil {
		os.Remove(tmpPath)
		return storage.ErrWriteFailed(checksumPath, err)
	}

	if err := os.Rename(tmpPath, layout.ManifestPath); err != nil {
		os.Remove(tmpPath)
		return storage.ErrAtomicWrite(layout.ManifestPath, err)
	}

	return nil
}

type VersionArtifact struct {
	recordingsDir string
}

func NewVersionArtifact(recordingsDir string) *VersionArtifact {
	return &VersionArtifact{recordingsDir: recordingsDir}
}

type VersionReason string

const (
	ReasonSummaryCreated    VersionReason = "summary_created"
	ReasonTranscriptCreated VersionReason = "transcript_created"
	ReasonAudioImported     VersionReason = "audio_imported"
	ReasonSpeakerRenamed    VersionReason = "speaker_renamed"
	ReasonEdited            VersionReason = "edited"
)

func (va *VersionArtifact) CreateVersion(meetingID uuid.UUID, reason VersionReason) (string, error) {
	layout, err := storage.LayoutFor(va.recordingsDir, meetingID)
	if err != nil {
		return "", err
	}

	manifest, err := storage.ReadManifest(layout)
	if err != nil {
		return "", err
	}

	currentVersionID := manifest.CurrentVersionID

	now := time.Now()
	newVersionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), randomSuffix4())

	versionDir := layout.VersionDir(newVersionID)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return "", storage.ErrDirCreate(versionDir, err)
	}

	artifactsToCopy := []struct {
		srcPath string
		dstPath string
	}{
		{layout.ManifestPath, "manifest.json"},
		{layout.VersionManifestPath(currentVersionID), "manifest.json"},
		{layout.VersionTranscriptPath(currentVersionID), "transcript.diarized.json"},
		{layout.VersionSummaryPath(currentVersionID), "summary.v1.md"},
		{layout.VersionChecksumPath(currentVersionID), "checksum.sha256"},
	}

	for _, artifact := range artifactsToCopy {
		srcPath := artifact.srcPath
		dstPath := filepath.Join(versionDir, artifact.dstPath)

		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			continue
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return "", err
		}
	}

	newVersion := ManifestVersion{
		VersionID: newVersionID,
		CreatedAt: now,
		Reason:    string(reason),
		Checksum:  fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(newVersionID))),
	}

	manifest.Versions = append(manifest.Versions, newVersion)
	manifest.CurrentVersionID = newVersionID

	if err := va.writeManifestAtomic(layout, manifest); err != nil {
		return "", err
	}

	versionManifest := &MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        manifest.MeetingID,
		CurrentVersionID: newVersionID,
		Versions:         []ManifestVersion{newVersion},
	}

	versionManifestPath := layout.VersionManifestPath(newVersionID)
	vmData, err := json.MarshalIndent(versionManifest, "", "  ")
	if err != nil {
		return "", storage.ErrWriteFailed(versionManifestPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "version_manifest.tmp")
	if err := os.WriteFile(tmpPath, vmData, 0644); err != nil {
		return "", storage.ErrWriteFailed(tmpPath, err)
	}

	if err := os.Rename(tmpPath, versionManifestPath); err != nil {
		os.Remove(tmpPath)
		return "", storage.ErrAtomicWrite(versionManifestPath, err)
	}

	return newVersionID, nil
}

func (va *VersionArtifact) writeManifestAtomic(layout storage.DirectoryLayout, manifest *MeetingManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return storage.ErrWriteFailed(layout.ManifestPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "manifest.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return storage.ErrWriteFailed(tmpPath, err)
	}

	checksum := ComputeChecksum(data)
	checksumPath := layout.ChecksumPath
	if err := os.WriteFile(checksumPath, []byte(checksum), 0644); err != nil {
		os.Remove(tmpPath)
		return storage.ErrWriteFailed(checksumPath, err)
	}

	if err := os.Rename(tmpPath, layout.ManifestPath); err != nil {
		os.Remove(tmpPath)
		return storage.ErrAtomicWrite(layout.ManifestPath, err)
	}

	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return storage.ErrReadFailed(src, err)
	}

	if err := os.WriteFile(dst, data, 0644); err != nil {
		return storage.ErrWriteFailed(dst, err)
	}

	return nil
}

func randomSuffix4() string {
	b := make([]byte, 4)
	for i := range b {
		b[i] = byte(uuid.New().ID() % 256)
	}
	return fmt.Sprintf("%x", b)
}

type VerifyChecksums struct {
	recordingsDir string
}

func NewVerifyChecksums(recordingsDir string) *VerifyChecksums {
	return &VerifyChecksums{recordingsDir: recordingsDir}
}

type VerificationResult struct {
	MeetingID uuid.UUID
	Valid     bool
	Errors    []VerificationError
	CheckedAt time.Time
}

type VerificationError struct {
	Path     string
	Expected string
	Actual   string
}

func (vc *VerifyChecksums) VerifyAll(meetingID uuid.UUID) (*VerificationResult, error) {
	layout, err := storage.LayoutFor(vc.recordingsDir, meetingID)
	if err != nil {
		return nil, err
	}

	result := &VerificationResult{
		MeetingID: meetingID,
		CheckedAt: time.Now(),
	}

	if err := vc.verifyManifestChecksum(layout); err != nil {
		result.Errors = append(result.Errors, err)
	}

	checksumPath := layout.ChecksumPath
	data, err := os.ReadFile(checksumPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, storage.ErrReadFailed(checksumPath, err)
		}
		result.Valid = len(result.Errors) == 0
		return result, nil
	}

	var checksums struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &checksums); err != nil {
		return nil, storage.ErrReadFailed(checksumPath, err)
	}

	for relPath, expectedChecksum := range checksums.Files {
		absPath := filepath.Join(layout.MeetingDir, relPath)
		if err := vc.verifyFileChecksum(absPath, expectedChecksum); err != nil {
			result.Errors = append(result.Errors, err)
		}
	}

	result.Valid = len(result.Errors) == 0
	return result, nil
}

func (vc *VerifyChecksums) verifyManifestChecksum(layout storage.DirectoryLayout) error {
	manifestPath := layout.ManifestPath
	checksumPath := layout.ChecksumPath

	expectedData, err := os.ReadFile(checksumPath)
	if err != nil {
		return VerificationError{Path: checksumPath, Expected: "", Actual: ""}
	}
	expected := string(expectedData)

	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return VerificationError{Path: manifestPath, Expected: expected, Actual: ""}
	}

	if err := VerifyChecksum(manifestData, expected); err != nil {
		return VerificationError{
			Path:     manifestPath,
			Expected: expected,
			Actual:   ComputeChecksum(manifestData),
		}
	}

	return nil
}

func (vc *VerifyChecksums) verifyFileChecksum(path, expected string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return VerificationError{Path: path, Expected: expected, Actual: ""}
	}

	if err := VerifyChecksum(data, expected); err != nil {
		return VerificationError{
			Path:     path,
			Expected: expected,
			Actual:   ComputeChecksum(data),
		}
	}

	return nil
}

type ImportAudio struct {
	recordingsDir string
}

func NewImportAudio(recordingsDir string) *ImportAudio {
	return &ImportAudio{recordingsDir: recordingsDir}
}

type ImportResult struct {
	AudioMetadata *AudioMetadata
	VersionID     string
}

func (ia *ImportAudio) Import(meetingID uuid.UUID, audioPath string) (*ImportResult, error) {
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, storage.ErrReadFailed(audioPath, err)
	}

	hash := sha256.Sum256(data)
	sha256Hex := fmt.Sprintf("%x", hash)
	fullChecksum := "sha256:" + sha256Hex

	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		return nil, storage.ErrReadFailed(audioPath, err)
	}

	ext := filepath.Ext(audioPath)
	format := ext
	if ext != "" {
		format = ext[1:]
	}

	layout, err := storage.LayoutFor(ia.recordingsDir, meetingID)
	if err != nil {
		return nil, err
	}

	if err := storage.EnsureDirs(layout); err != nil {
		return nil, err
	}

	assetID := fmt.Sprintf("aud_%s", uuid.New().String()[:12])

	audioMeta := &AudioMetadata{
		SchemaVersion:   "audio-asset.v1",
		MeetingID:       meetingID.String(),
		AssetID:         assetID,
		Path:            "audio/recording.m4a",
		Format:          format,
		Codec:           "aac",
		DurationSeconds: 0,
		Channels:        2,
		SampleRateHz:    48000,
		Sources: []AudioSource{
			{ID: "src_mic", Role: "local_speaker", Label: "Microphone", Channel: 0},
			{ID: "src_system", Role: "participants", Label: "System Audio", Channel: 1},
		},
		SizeBytes: fileInfo.Size(),
		SHA256:    fullChecksum,
		Retention: AudioRetention{
			Policy:   "delete_after_valid_transcript",
			Retained: true,
		},
	}

	now := time.Now()
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), randomSuffix4())
	versionAudioDir := filepath.Join(layout.VersionDir(versionID), "audio")
	if err := os.MkdirAll(versionAudioDir, 0755); err != nil {
		return nil, storage.ErrDirCreate(versionAudioDir, err)
	}

	versionAudioPath := filepath.Join(versionAudioDir, "recording.m4a")
	if err := os.WriteFile(versionAudioPath, data, 0644); err != nil {
		return nil, storage.ErrWriteFailed(versionAudioPath, err)
	}

	audioMetaPath := filepath.Join(layout.VersionDir(versionID), "audio.json")
	audioMetaData, err := json.MarshalIndent(audioMeta, "", "  ")
	if err != nil {
		return nil, storage.ErrWriteFailed(audioMetaPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "audio_meta.tmp")
	if err := os.WriteFile(tmpPath, audioMetaData, 0644); err != nil {
		return nil, storage.ErrWriteFailed(tmpPath, err)
	}

	if err := os.Rename(tmpPath, audioMetaPath); err != nil {
		os.Remove(tmpPath)
		return nil, storage.ErrAtomicWrite(audioMetaPath, err)
	}

	return &ImportResult{
		AudioMetadata: audioMeta,
		VersionID:     versionID,
	}, nil
}
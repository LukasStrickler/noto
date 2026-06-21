package storage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

func WriteManifest(layout DirectoryLayout, m *artifacts.MeetingManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return ErrWriteFailed(layout.ManifestPath, err)
	}

	checksum := artifacts.ComputeChecksum(data)
	checksumPath := layout.ChecksumPath

	tmpChecksumPath := filepath.Join(layout.TmpDir, "manifest_checksum.tmp")
	if err := os.WriteFile(tmpChecksumPath, []byte(checksum), 0644); err != nil {
		return ErrWriteFailed(tmpChecksumPath, err)
	}
	if err := fsyncFile(tmpChecksumPath); err != nil {
		os.Remove(tmpChecksumPath)
		return ErrWriteFailed(tmpChecksumPath, err)
	}
	if err := os.Rename(tmpChecksumPath, checksumPath); err != nil {
		os.Remove(tmpChecksumPath)
		return ErrAtomicWrite(checksumPath, err)
	}
	if err := fsyncDir(filepath.Dir(checksumPath)); err != nil {
		return err
	}

	tmpPath := filepath.Join(layout.TmpDir, "manifest.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return ErrWriteFailed(tmpPath, err)
	}
	if err := fsyncFile(tmpPath); err != nil {
		os.Remove(tmpPath)
		return ErrWriteFailed(tmpPath, err)
	}
	if err := os.Rename(tmpPath, layout.ManifestPath); err != nil {
		os.Remove(tmpPath)
		return ErrAtomicWrite(layout.ManifestPath, err)
	}
	if err := fsyncDir(filepath.Dir(layout.ManifestPath)); err != nil {
		return err
	}

	return nil
}

func fsyncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func fsyncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func ReadManifest(layout DirectoryLayout) (*artifacts.MeetingManifest, error) {
	data, err := os.ReadFile(layout.ManifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrMeetingNotFound(layout.MeetingID.String())
		}
		return nil, ErrReadFailed(layout.ManifestPath, err)
	}

	checksumPath := layout.ChecksumPath
	expectedChecksum, err := os.ReadFile(checksumPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrReadFailed(checksumPath, err)
		}
		return nil, ErrReadFailed(checksumPath, err)
	}

	if err := artifacts.VerifyChecksum(data, string(expectedChecksum)); err != nil {
		return nil, ErrChecksumMismatch(string(expectedChecksum), artifacts.ComputeChecksum(data), layout.ManifestPath)
	}

	var m artifacts.MeetingManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, ErrReadFailed(layout.ManifestPath, err)
	}

	return &m, nil
}

func WriteTranscript(layout DirectoryLayout, t *artifacts.Transcript) error {
	// Validate BEFORE persisting. ReadTranscript validates on the way out, so a
	// transcript that fails validation here would be written to disk yet be
	// permanently unreadable — silently corrupt while the job that wrote it reports
	// success (an empty-speaker or empty-segment-text transcript hits exactly this).
	// Refusing the write surfaces the defect at its source. Symmetry rule: if it
	// can't be read back, it must not be written.
	if t == nil {
		return ErrWriteFailed(layout.TranscriptPath, fmt.Errorf("nil transcript"))
	}
	if verr := artifacts.ValidateTranscript(*t); verr != nil {
		return ErrWriteFailed(layout.TranscriptPath, verr)
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return ErrWriteFailed(layout.TranscriptPath, err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "transcript.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return ErrWriteFailed(tmpPath, err)
	}
	if err := fsyncFile(tmpPath); err != nil {
		os.Remove(tmpPath)
		return ErrWriteFailed(tmpPath, err)
	}
	if err := os.Rename(tmpPath, layout.TranscriptPath); err != nil {
		os.Remove(tmpPath)
		return ErrAtomicWrite(layout.TranscriptPath, err)
	}
	if err := fsyncDir(filepath.Dir(layout.TranscriptPath)); err != nil {
		return err
	}

	return nil
}

func ReadTranscript(layout DirectoryLayout) (*artifacts.Transcript, error) {
	data, err := os.ReadFile(layout.TranscriptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrArtifactNotFound("transcript", layout.MeetingID.String())
		}
		return nil, ErrReadFailed(layout.TranscriptPath, err)
	}

	var t artifacts.Transcript
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, ErrReadFailed(layout.TranscriptPath, err)
	}

	if err := artifacts.ValidateTranscript(t); err != nil {
		return nil, ErrReadFailed(layout.TranscriptPath, err)
	}

	return &t, nil
}

func WriteSummary(layout DirectoryLayout, summaryMD string) error {
	tmpPath := filepath.Join(layout.TmpDir, "summary.tmp")
	if err := os.WriteFile(tmpPath, []byte(summaryMD), 0644); err != nil {
		return ErrWriteFailed(tmpPath, err)
	}
	if err := fsyncFile(tmpPath); err != nil {
		os.Remove(tmpPath)
		return ErrWriteFailed(tmpPath, err)
	}
	if err := os.Rename(tmpPath, layout.SummaryPath); err != nil {
		os.Remove(tmpPath)
		return ErrAtomicWrite(layout.SummaryPath, err)
	}
	if err := fsyncDir(filepath.Dir(layout.SummaryPath)); err != nil {
		return err
	}

	return nil
}

func ReadSummary(layout DirectoryLayout) (string, error) {
	data, err := os.ReadFile(layout.SummaryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrArtifactNotFound("summary", layout.MeetingID.String())
		}
		return "", ErrReadFailed(layout.SummaryPath, err)
	}
	return string(data), nil
}

func WriteAudioMetadata(layout DirectoryLayout, audio *artifacts.AudioMetadata) error {
	// Same write/read symmetry as WriteTranscript: ReadAudioMetadata validates on the
	// way out, so an invalid metadata written here would be persisted yet permanently
	// unreadable. Refuse it at the source instead — if it can't be read back, don't write it.
	if audio == nil {
		return ErrWriteFailed(layout.AudioPath+".json", fmt.Errorf("nil audio metadata"))
	}
	if verr := audio.Validate(); verr != nil {
		return ErrWriteFailed(layout.AudioPath+".json", verr)
	}
	data, err := json.MarshalIndent(audio, "", "  ")
	if err != nil {
		return ErrWriteFailed(layout.AudioPath+".json", err)
	}

	tmpPath := filepath.Join(layout.TmpDir, "audio.json.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return ErrWriteFailed(tmpPath, err)
	}
	if err := fsyncFile(tmpPath); err != nil {
		os.Remove(tmpPath)
		return ErrWriteFailed(tmpPath, err)
	}

	filename := filepath.Base(layout.AudioPath)
	finalPath := filepath.Join(layout.MeetingDir, filename+".json")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return ErrAtomicWrite(finalPath, err)
	}
	if err := fsyncDir(filepath.Dir(finalPath)); err != nil {
		return err
	}

	return nil
}

func ReadAudioMetadata(layout DirectoryLayout) (*artifacts.AudioMetadata, error) {
	audioPath := layout.AudioPath + ".json"
	data, err := os.ReadFile(audioPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrArtifactNotFound("audio", layout.MeetingID.String())
		}
		return nil, ErrReadFailed(audioPath, err)
	}

	var audio artifacts.AudioMetadata
	if err := json.Unmarshal(data, &audio); err != nil {
		return nil, ErrReadFailed(audioPath, err)
	}

	if err := audio.Validate(); err != nil {
		return nil, ErrReadFailed(audioPath, err)
	}

	return &audio, nil
}

func CopyAudioToVersion(layout DirectoryLayout, versionID string, srcAudioPath string) error {
	versionAudioPath := layout.VersionAudioPath(versionID)

	if err := os.MkdirAll(filepath.Dir(versionAudioPath), 0755); err != nil {
		return ErrDirCreate(filepath.Dir(versionAudioPath), err)
	}

	srcFile, err := os.Open(srcAudioPath)
	if err != nil {
		return ErrReadFailed(srcAudioPath, err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(versionAudioPath)
	if err != nil {
		return ErrWriteFailed(versionAudioPath, err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(versionAudioPath)
		return ErrWriteFailed(versionAudioPath, err)
	}

	if err := dstFile.Close(); err != nil {
		os.Remove(versionAudioPath)
		return ErrWriteFailed(versionAudioPath, err)
	}

	if err := fsyncFile(versionAudioPath); err != nil {
		os.Remove(versionAudioPath)
		return ErrWriteFailed(versionAudioPath, err)
	}

	if err := fsyncDir(filepath.Dir(versionAudioPath)); err != nil {
		return err
	}

	return nil
}

func WriteVersionManifest(layout DirectoryLayout, versionID string, m *artifacts.MeetingManifest) error {
	versionDir := layout.VersionDir(versionID)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return ErrDirCreate(versionDir, err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return ErrWriteFailed(layout.VersionManifestPath(versionID), err)
	}

	checksum := artifacts.ComputeChecksum(data)
	checksumPath := layout.VersionChecksumPath(versionID)

	tmpChecksumPath := filepath.Join(layout.TmpDir, "version_checksum.tmp")
	if err := os.WriteFile(tmpChecksumPath, []byte(checksum), 0644); err != nil {
		return ErrWriteFailed(tmpChecksumPath, err)
	}
	if err := fsyncFile(tmpChecksumPath); err != nil {
		os.Remove(tmpChecksumPath)
		return ErrWriteFailed(tmpChecksumPath, err)
	}
	if err := os.Rename(tmpChecksumPath, checksumPath); err != nil {
		os.Remove(tmpChecksumPath)
		return ErrAtomicWrite(checksumPath, err)
	}
	if err := fsyncDir(filepath.Dir(checksumPath)); err != nil {
		return err
	}

	tmpPath := filepath.Join(layout.TmpDir, "version_manifest.tmp")
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return ErrWriteFailed(tmpPath, err)
	}
	if err := fsyncFile(tmpPath); err != nil {
		os.Remove(tmpPath)
		return ErrWriteFailed(tmpPath, err)
	}

	finalPath := layout.VersionManifestPath(versionID)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return ErrAtomicWrite(finalPath, err)
	}
	if err := fsyncDir(filepath.Dir(finalPath)); err != nil {
		return err
	}

	return nil
}

func ReadVersionManifest(layout DirectoryLayout, versionID string) (*artifacts.MeetingManifest, error) {
	path := layout.VersionManifestPath(versionID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrArtifactNotFound("version_manifest", layout.MeetingID.String())
		}
		return nil, ErrReadFailed(path, err)
	}

	var m artifacts.MeetingManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, ErrReadFailed(path, err)
	}

	return &m, nil
}

func ComputeFileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", ErrReadFailed(path, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", ErrReadFailed(path, err)
	}

	return "sha256:" + fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func CreateVersion(layout DirectoryLayout, reason string) (string, error) {
	now := time.Now()
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), randomSuffix())

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        layout.MeetingID.String(),
		CurrentVersionID: versionID,
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: versionID,
				CreatedAt: now,
				Reason:    reason,
			},
		},
	}

	if err := WriteVersionManifest(layout, versionID, m); err != nil {
		return "", err
	}

	return versionID, nil
}

func randomSuffix() string {
	b := make([]byte, 4)
	for i := range b {
		b[i] = byte(uuid.New().ID() % 256)
	}
	return fmt.Sprintf("%x", b)
}

func VerifyMeetingChecksums(layout DirectoryLayout) error {
	manifest, err := ReadManifest(layout)
	if err != nil {
		return err
	}

	manifestData, err := os.ReadFile(layout.ManifestPath)
	if err != nil {
		return ErrReadFailed(layout.ManifestPath, err)
	}

	manifestChecksumPath := layout.ChecksumPath
	expectedManifestChecksum, err := os.ReadFile(manifestChecksumPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("manifest checksum file not found: %s", manifestChecksumPath)
		}
		return ErrReadFailed(manifestChecksumPath, err)
	}

	if err := artifacts.VerifyChecksum(manifestData, string(expectedManifestChecksum)); err != nil {
		return ErrChecksumMismatch(string(expectedManifestChecksum), artifacts.ComputeChecksum(manifestData), layout.ManifestPath)
	}

	// checksums.json is only present once WritePipeline has run a full
	// multi-file commit. A manifest-only meeting (recording just made,
	// not yet through ingest) doesn't have it — that's not an error.
	checksumsFile := filepath.Join(layout.MeetingDir, "checksums.json")
	checksumData, err := os.ReadFile(checksumsFile)
	if err != nil {
		if os.IsNotExist(err) {
			_ = manifest
			return nil
		}
		return ErrReadFailed(checksumsFile, err)
	}

	var checksums struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(checksumData, &checksums); err != nil {
		return fmt.Errorf("failed to parse checksums.json: %w", err)
	}

	for relPath, expectedChecksum := range checksums.Files {
		absPath := filepath.Join(layout.MeetingDir, relPath)
		fileData, err := os.ReadFile(absPath)
		if err != nil {
			return ErrReadFailed(absPath, err)
		}
		if err := artifacts.VerifyChecksum(fileData, expectedChecksum); err != nil {
			return ErrChecksumMismatch(expectedChecksum, artifacts.ComputeChecksum(fileData), absPath)
		}
	}

	_ = manifest

	return nil
}

func DeleteMeeting(layout DirectoryLayout) error {
	if err := os.RemoveAll(layout.MeetingDir); err != nil {
		return ErrWriteFailed(layout.MeetingDir, err)
	}
	return nil
}

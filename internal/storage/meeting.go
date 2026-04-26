package storage

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
)

type MeetingStore struct {
	RecordingsDir string
}

func NewMeetingStore(recordingsDir string) *MeetingStore {
	return &MeetingStore{RecordingsDir: recordingsDir}
}

func (s *MeetingStore) LayoutFor(meetingID uuid.UUID) (DirectoryLayout, error) {
	return LayoutFor(s.RecordingsDir, meetingID)
}

func (s *MeetingStore) EnsureDirs(layout DirectoryLayout) error {
	return EnsureDirs(layout)
}

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
	f, err := os.OpenFile(path, os.O_RDWR|os.O_DIRECTORY, 0644)
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
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ErrReadFailed(path, err)
	}
	hash := sha256.Sum256(data)
	return "sha256:" + fmt.Sprintf("%x", hash), nil
}

func ListMeetings(recordingsDir string) ([]MeetingRef, error) {
	meetingsDir := filepath.Join(recordingsDir, "meetings")

	entries, err := os.ReadDir(meetingsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, ErrReadFailed(meetingsDir, err)
	}

	var refs []MeetingRef

	for _, yearEntry := range entries {
		if !yearEntry.IsDir() {
			continue
		}
		year := yearEntry.Name()

		yearDir := filepath.Join(meetingsDir, year)
		monthEntries, err := os.ReadDir(yearDir)
		if err != nil {
			continue
		}

		for _, monthEntry := range monthEntries {
			if !monthEntry.IsDir() {
				continue
			}
			month := monthEntry.Name()

			monthDir := filepath.Join(yearDir, month)
			meetingEntries, err := os.ReadDir(monthDir)
			if err != nil {
				continue
			}

			for _, meetingEntry := range meetingEntries {
				if !meetingEntry.IsDir() {
					continue
				}
				meetingIDStr := meetingEntry.Name()

				meetingID, err := uuid.Parse(meetingIDStr)
				if err != nil {
					continue
				}

				manifestPath := filepath.Join(monthDir, meetingIDStr, "manifest.json")
				if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
					continue
				}

				layout, err := LayoutFor(recordingsDir, meetingID)
				if err != nil {
					continue
				}

				manifest, err := ReadManifest(layout)
				if err != nil {
					continue
				}

				refs = append(refs, MeetingRef{
					MeetingID:        meetingID,
					Title:            extractTitle(manifest),
					Year:             year,
					Month:            month,
					CurrentVersionID: manifest.CurrentVersionID,
					CreatedAt:        extractCreatedAt(manifest),
				})
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		return refs[i].CreatedAt.After(refs[j].CreatedAt)
	})

	return refs, nil
}

func GetMeeting(recordingsDir string, meetingID uuid.UUID) (*Meeting, error) {
	layout, err := LayoutFor(recordingsDir, meetingID)
	if err != nil {
		return nil, err
	}

	manifest, err := ReadManifest(layout)
	if err != nil {
		return nil, err
	}

	var versions []VersionInfo
	for _, v := range manifest.Versions {
		versions = append(versions, VersionInfo{
			VersionID: v.VersionID,
			CreatedAt: v.CreatedAt,
			Reason:    v.Reason,
			Checksum:  v.Checksum,
		})
	}

	shortSummary := ""
	summaryPath := layout.SummaryPath
	if data, err := os.ReadFile(summaryPath); err == nil {
		lines := splitLines(string(data))
		if len(lines) > 0 {
			shortSummary = lines[0]
		}
	}

	return &Meeting{
		MeetingID:        meetingID,
		Title:           extractTitle(manifest),
		CurrentVersionID: manifest.CurrentVersionID,
		Versions:        versions,
		ShortSummary:    shortSummary,
	}, nil
}

type MeetingRef struct {
	MeetingID        uuid.UUID
	Title            string
	Year             string
	Month            string
	CurrentVersionID string
	CreatedAt        time.Time
}

type Meeting struct {
	MeetingID        uuid.UUID
	Title            string
	CurrentVersionID string
	Versions         []VersionInfo
	ShortSummary     string
}

type VersionInfo struct {
	VersionID string
	CreatedAt time.Time
	Reason    string
	Checksum  string
}

func extractTitle(m *artifacts.MeetingManifest) string {
	return m.Metadata.Title
}

func extractCreatedAt(m *artifacts.MeetingManifest) time.Time {
	if len(m.Versions) == 0 {
		return time.Time{}
	}
	for _, v := range m.Versions {
		if v.VersionID == m.CurrentVersionID {
			return v.CreatedAt
		}
	}
	return m.Versions[0].CreatedAt
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
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

	checksumsFile := filepath.Join(layout.MeetingDir, "checksums.json")
	checksumData, err := os.ReadFile(checksumsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("checksums file not found: %s", checksumsFile)
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
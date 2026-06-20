package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DirectoryLayout struct {
	RecordingsDir string

	MeetingID uuid.UUID
	Year      string
	Month     string

	MeetingDir  string
	VersionsDir string
	TmpDir      string

	ManifestPath   string
	AudioPath      string
	TranscriptPath string
	SummaryPath    string
	ChecksumPath   string
}

func LayoutFor(recordingsDir string, meetingID uuid.UUID) (DirectoryLayout, error) {
	if recordingsDir == "" {
		return DirectoryLayout{}, ErrInvalidLayout("recordings_dir is required")
	}

	now := time.Now()
	year := now.Format("2006")
	month := now.Format("01")

	baseDir := filepath.Join(recordingsDir, "meetings", year, month, meetingID.String())

	layout := DirectoryLayout{
		RecordingsDir: recordingsDir,

		MeetingID: meetingID,
		Year:      year,
		Month:     month,

		MeetingDir:  baseDir,
		VersionsDir: filepath.Join(baseDir, "versions"),
		TmpDir:      filepath.Join(baseDir, ".tmp"),

		ManifestPath:   filepath.Join(baseDir, "manifest.json"),
		AudioPath:      filepath.Join(baseDir, "audio.m4a"),
		TranscriptPath: filepath.Join(baseDir, "transcript.diarized.json"),
		SummaryPath:    filepath.Join(baseDir, "summary.v1.md"),
		ChecksumPath:   filepath.Join(baseDir, "checksum.sha256"),
	}

	return layout, nil
}

func (l DirectoryLayout) VersionDir(versionID string) string {
	return filepath.Join(l.VersionsDir, versionID)
}

func (l DirectoryLayout) VersionManifestPath(versionID string) string {
	return filepath.Join(l.VersionDir(versionID), "manifest.json")
}

func (l DirectoryLayout) VersionAudioPath(versionID string) string {
	return filepath.Join(l.VersionDir(versionID), "audio.m4a")
}

func (l DirectoryLayout) VersionTranscriptPath(versionID string) string {
	return filepath.Join(l.VersionDir(versionID), "transcript.diarized.json")
}

func (l DirectoryLayout) VersionSummaryPath(versionID string) string {
	return filepath.Join(l.VersionDir(versionID), "summary.v1.md")
}

func (l DirectoryLayout) VersionChecksumPath(versionID string) string {
	return filepath.Join(l.VersionDir(versionID), "checksum.sha256")
}

func EnsureDirs(layout DirectoryLayout) error {
	dirs := []string{
		layout.MeetingDir,
		layout.VersionsDir,
		layout.TmpDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return ErrDirCreate(dir, err)
		}
		if err := os.Chmod(dir, 0755); err != nil {
			return ErrDirCreate(dir, err)
		}
	}

	return nil
}

func ValidateLayout(layout DirectoryLayout) error {
	if layout.RecordingsDir == "" {
		return ErrInvalidLayout("recordings_dir is required")
	}
	if layout.MeetingID == uuid.Nil {
		return ErrInvalidLayout("meeting_id is required")
	}
	if layout.MeetingDir == "" {
		return ErrInvalidLayout("meeting_dir is required")
	}
	return nil
}

func ParseMeetingID(dir string) (uuid.UUID, error) {
	base := filepath.Base(dir)
	return uuid.Parse(base)
}

// ExtractDateFromPath pulls the YYYY/MM segments out of a noto meeting
// path. Accepts absolute or relative paths; finds the first "meetings"
// segment and returns the two directory levels immediately after it.
func ExtractDateFromPath(meetingPath string) (year, month string, err error) {
	parts := strings.Split(filepath.ToSlash(meetingPath), "/")
	for i, p := range parts {
		if p == "meetings" && i+2 < len(parts) {
			return parts[i+1], parts[i+2], nil
		}
	}
	return "", "", fmt.Errorf("path does not contain meetings/YYYY/MM: %s", meetingPath)
}

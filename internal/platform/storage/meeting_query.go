package storage

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

// This file holds the meeting listing/query side of the store: walking the
// year/month/meeting tree to enumerate or count meetings, loading a single
// meeting's display aggregate, and the read-model DTO types these return.
// The per-artifact file IO and versioning live in meeting.go.

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

// CountMeetings counts stored meetings by walking the year/month/meeting
// directory tree and checking only for a manifest.json (os.Stat, no parse).
// It deliberately avoids ReadManifest so it stays cheap on hot paths.
func CountMeetings(recordingsDir string) (int, error) {
	meetingsDir := filepath.Join(recordingsDir, "meetings")
	yearEntries, err := os.ReadDir(meetingsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, ErrReadFailed(meetingsDir, err)
	}

	count := 0
	for _, yearEntry := range yearEntries {
		if !yearEntry.IsDir() {
			continue
		}
		yearDir := filepath.Join(meetingsDir, yearEntry.Name())
		monthEntries, err := os.ReadDir(yearDir)
		if err != nil {
			continue
		}
		for _, monthEntry := range monthEntries {
			if !monthEntry.IsDir() {
				continue
			}
			monthDir := filepath.Join(yearDir, monthEntry.Name())
			meetingEntries, err := os.ReadDir(monthDir)
			if err != nil {
				continue
			}
			for _, meetingEntry := range meetingEntries {
				if !meetingEntry.IsDir() {
					continue
				}
				if _, err := uuid.Parse(meetingEntry.Name()); err != nil {
					continue
				}
				manifestPath := filepath.Join(monthDir, meetingEntry.Name(), "manifest.json")
				if _, err := os.Stat(manifestPath); err == nil {
					count++
				}
			}
		}
	}
	return count, nil
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
		Title:            extractTitle(manifest),
		CurrentVersionID: manifest.CurrentVersionID,
		Versions:         versions,
		ShortSummary:     shortSummary,
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

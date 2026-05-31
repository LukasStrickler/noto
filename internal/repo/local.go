package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
	"github.com/lukasstrickler/noto/internal/storage"
)

// LocalArtifactRepository persists meeting artifacts on the local
// filesystem backed by the existing storage package conventions.
// It satisfies repo.ArtifactRepository.
type LocalArtifactRepository struct {
	recordingsDir string
}

// NewLocal returns a LocalArtifactRepository rooted at recordingsDir.
func NewLocal(recordingsDir string) *LocalArtifactRepository {
	return &LocalArtifactRepository{recordingsDir: recordingsDir}
}

func (r *LocalArtifactRepository) CreateMeeting(_ context.Context, id uuid.UUID, opts CreateMeetingOpts) error {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return err
	}
	if err := storage.EnsureDirs(layout); err != nil {
		return err
	}
	now := opts.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), id.String()[:8])
	manifest := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        id.String(),
		CurrentVersionID: versionID,
		Metadata:         artifacts.ManifestMetadata{Title: opts.Title},
		Versions: []artifacts.ManifestVersion{
			{VersionID: versionID, CreatedAt: now, Reason: opts.Reason, Checksum: "n/a"},
		},
	}
	return storage.WriteManifest(layout, manifest)
}

func (r *LocalArtifactRepository) GetMeeting(_ context.Context, id uuid.UUID) (*StoredMeeting, error) {
	m, err := storage.GetMeeting(r.recordingsDir, id)
	if err != nil {
		if isStorageNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sm := &StoredMeeting{
		ID:               id,
		Title:            m.Title,
		CurrentVersionID: m.CurrentVersionID,
	}
	if sm.Title == "" {
		sm.Title = "Untitled meeting"
	}
	for _, v := range m.Versions {
		sm.Versions = append(sm.Versions, StoredVersion{
			ID:        v.VersionID,
			CreatedAt: v.CreatedAt,
			Reason:    v.Reason,
			Checksum:  v.Checksum,
		})
		if v.VersionID == m.CurrentVersionID {
			sm.CreatedAt = v.CreatedAt
		}
	}
	if sm.CreatedAt.IsZero() && len(m.Versions) > 0 {
		sm.CreatedAt = m.Versions[0].CreatedAt
	}
	r.enrich(id, sm)
	return sm, nil
}

func (r *LocalArtifactRepository) ListMeetings(_ context.Context) ([]*StoredMeeting, error) {
	refs, err := storage.ListMeetings(r.recordingsDir)
	if err != nil {
		return nil, err
	}
	out := make([]*StoredMeeting, 0, len(refs))
	for _, ref := range refs {
		sm := &StoredMeeting{
			ID:               ref.MeetingID,
			Title:            ref.Title,
			CurrentVersionID: ref.CurrentVersionID,
			CreatedAt:        ref.CreatedAt,
		}
		if sm.Title == "" {
			sm.Title = "Untitled meeting"
		}
		r.enrich(ref.MeetingID, sm)
		out = append(out, sm)
	}
	return out, nil
}

func (r *LocalArtifactRepository) DeleteMeeting(_ context.Context, id uuid.UUID) error {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return err
	}
	return storage.DeleteMeeting(layout)
}

func (r *LocalArtifactRepository) CountMeetings(_ context.Context) (int, error) {
	return storage.CountMeetings(r.recordingsDir)
}

func (r *LocalArtifactRepository) SaveTranscript(_ context.Context, id uuid.UUID, t *artifacts.Transcript) error {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return err
	}
	return storage.WriteTranscript(layout, t)
}

func (r *LocalArtifactRepository) LoadTranscript(_ context.Context, id uuid.UUID) (*artifacts.Transcript, error) {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return nil, err
	}
	return storage.ReadTranscript(layout)
}

func (r *LocalArtifactRepository) SaveSummary(_ context.Context, id uuid.UUID, md string, summary *artifacts.Summary) error {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return err
	}
	if err := storage.WriteSummary(layout, md); err != nil {
		return err
	}
	if summary != nil {
		data, err := json.MarshalIndent(summary, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(layout.MeetingDir, "summary.json"), data, 0o644)
	}
	return nil
}

func (r *LocalArtifactRepository) LoadSummary(_ context.Context, id uuid.UUID) (string, *artifacts.Summary, error) {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return "", nil, err
	}
	md, readErr := storage.ReadSummary(layout)
	if readErr != nil {
		// "not found" is normal — the meeting hasn't been summarized yet.
		if isStorageNotFound(readErr) {
			return "", nil, nil
		}
		return "", nil, readErr
	}
	jsonPath := filepath.Join(layout.MeetingDir, "summary.json")
	var summary *artifacts.Summary
	if data, err := os.ReadFile(jsonPath); err == nil {
		var s artifacts.Summary
		if json.Unmarshal(data, &s) == nil {
			summary = &s
		}
	}
	return md, summary, nil
}

func (r *LocalArtifactRepository) PrepareAudio(_ context.Context, id uuid.UUID, ext string) (string, error) {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return "", err
	}
	if err := storage.EnsureDirs(layout); err != nil {
		return "", err
	}
	audioPath := layout.AudioPath
	if ext != "" {
		audioPath = strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ext
	}
	return audioPath, nil
}

// AudioPath checks for audio at the default path and common extension
// variants in case the file was imported with a non-default extension.
func (r *LocalArtifactRepository) AudioPath(id uuid.UUID) (string, bool) {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(layout.AudioPath); err == nil {
		return layout.AudioPath, true
	}
	base := strings.TrimSuffix(layout.AudioPath, filepath.Ext(layout.AudioPath))
	for _, ext := range []string{".wav", ".flac", ".mp3", ".ogg", ".opus"} {
		p := base + ext
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

func (r *LocalArtifactRepository) VerifyIntegrity(_ context.Context, id uuid.UUID) error {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return err
	}
	return storage.VerifyMeetingChecksums(layout)
}

func (r *LocalArtifactRepository) FilePaths(id uuid.UUID) *MeetingFilePaths {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return nil
	}
	fp := &MeetingFilePaths{MeetingDir: layout.MeetingDir}
	if _, err := os.Stat(layout.ManifestPath); err == nil {
		fp.Manifest = layout.ManifestPath
	}
	if _, err := os.Stat(layout.TranscriptPath); err == nil {
		fp.Transcript = layout.TranscriptPath
	}
	if _, err := os.Stat(layout.SummaryPath); err == nil {
		fp.SummaryMD = layout.SummaryPath
	}
	jsonPath := filepath.Join(layout.MeetingDir, "summary.json")
	if _, err := os.Stat(jsonPath); err == nil {
		fp.SummaryJSON = jsonPath
	}
	if path, ok := r.AudioPath(id); ok {
		fp.Audio = path
	}
	return fp
}

// isStorageNotFound returns true for "artifact not found" errors from the
// storage package (meeting not yet summarized/transcribed, or missing).
func isStorageNotFound(err error) bool {
	if err == nil {
		return false
	}
	var ne *notoerr.Error
	if errors.As(err, &ne) {
		return ne.Code == storage.ErrCodeStorageNotFound
	}
	return os.IsNotExist(err)
}

// enrich reads transcript and summary artifacts from disk and fills in
// the derived count fields on sm. Errors are silently ignored so that
// a missing artifact does not prevent the meeting from appearing in lists.
func (r *LocalArtifactRepository) enrich(id uuid.UUID, sm *StoredMeeting) {
	layout, err := storage.LayoutFor(r.recordingsDir, id)
	if err != nil {
		return
	}
	if data, err := os.ReadFile(filepath.Join(layout.MeetingDir, "summary.json")); err == nil {
		var s artifacts.Summary
		if json.Unmarshal(data, &s) == nil {
			sm.HasSummary = true
			sm.DecisionCount = len(s.Decisions)
			sm.ActionCount = len(s.ActionItems)
			sm.RiskCount = len(s.Risks)
			sm.QuestionCount = len(s.OpenQuestions)
			sm.ShortSummary = s.ShortSummary
		}
	}
	if data, err := os.ReadFile(layout.TranscriptPath); err == nil {
		var t artifacts.Transcript
		if json.Unmarshal(data, &t) == nil {
			sm.HasTranscript = true
			sm.SpeakerCount = len(t.Speakers)
			if len(t.Segments) > 0 {
				sm.DurationSeconds = int(t.Segments[len(t.Segments)-1].EndSeconds)
			}
		}
	}
}

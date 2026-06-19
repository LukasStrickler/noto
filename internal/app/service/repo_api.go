package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
)

// This file exposes the artifact store across the service boundary so a remote
// edge node (compute local, data off-site — item 4) can write its pipeline
// output through to this backend over the /v1/repo/* API and read it back.
//
// Writes deliberately go through the service, not raw repo, so this node ALSO
// indexes them: a write-through transcript/summary is immediately queryable
// here, keeping the remote data plane the single source of truth exactly as if
// the pipeline had run on it. Reads proxy the repo directly.

// RepoCreateMeeting initialises storage for a meeting on this backend.
func (s *Service) RepoCreateMeeting(ctx context.Context, id uuid.UUID, opts repo.CreateMeetingOpts) error {
	return s.repo.CreateMeeting(ctx, id, opts)
}

// RepoGetMeeting returns the enriched meeting record.
func (s *Service) RepoGetMeeting(ctx context.Context, id uuid.UUID) (*repo.StoredMeeting, error) {
	return s.repo.GetMeeting(ctx, id)
}

// RepoListMeetings returns all stored meetings (most-recent first).
func (s *Service) RepoListMeetings(ctx context.Context) ([]*repo.StoredMeeting, error) {
	return s.repo.ListMeetings(ctx)
}

// RepoCountMeetings returns the number of stored meetings.
func (s *Service) RepoCountMeetings(ctx context.Context) (int, error) {
	return s.repo.CountMeetings(ctx)
}

// RepoDeleteMeeting removes a meeting and its search-index entries.
func (s *Service) RepoDeleteMeeting(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteMeeting(ctx, id); err != nil {
		return err
	}
	if s.search != nil {
		_ = s.search.DeleteFromIndex(id.String())
	}
	return nil
}

// RepoSaveTranscript persists a write-through transcript and re-indexes the
// meeting so it stays searchable on this backend.
func (s *Service) RepoSaveTranscript(ctx context.Context, id uuid.UUID, t *artifacts.Transcript) error {
	if err := s.repo.SaveTranscript(ctx, id, t); err != nil {
		return err
	}
	_ = s.indexOneMeeting(ctx, id, "") // best-effort: an index hiccup must not fail the store
	return nil
}

// RepoLoadTranscript reads a stored transcript.
func (s *Service) RepoLoadTranscript(ctx context.Context, id uuid.UUID) (*artifacts.Transcript, error) {
	return s.repo.LoadTranscript(ctx, id)
}

// RepoSaveSummary persists a write-through summary (markdown + structured) and
// re-indexes the meeting.
func (s *Service) RepoSaveSummary(ctx context.Context, id uuid.UUID, md string, summary *artifacts.Summary) error {
	if err := s.repo.SaveSummary(ctx, id, md, summary); err != nil {
		return err
	}
	_ = s.indexOneMeeting(ctx, id, "")
	return nil
}

// RepoLoadSummary reads a stored summary (markdown + structured).
func (s *Service) RepoLoadSummary(ctx context.Context, id uuid.UUID) (string, *artifacts.Summary, error) {
	return s.repo.LoadSummary(ctx, id)
}

// RepoVerify checks stored checksums for a meeting's artifacts.
func (s *Service) RepoVerify(ctx context.Context, id uuid.UUID) error {
	return s.repo.VerifyIntegrity(ctx, id)
}

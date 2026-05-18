package service

import (
	"context"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// GetStorage returns the current storage summary.
func (s *Service) GetStorage(ctx context.Context) (notoapi.Storage, error) {
	list, _ := s.ListMeetings(ctx, notoapi.ListMeetingsOpts{})
	return notoapi.Storage{
		SchemaVersion: s.cfg.SchemaVersion,
		RecordingsDir: s.recordingsDir,
		MeetingCount:  list.Total,
		IndexState:    "clean",
	}, nil
}

// VerifyStorage enqueues a verify-all job and returns it.
func (s *Service) VerifyStorage(ctx context.Context) (notoapi.Job, error) {
	return s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobVerify})
}

// ReindexStorage enqueues a reindex job and returns it.
func (s *Service) ReindexStorage(ctx context.Context) (notoapi.Job, error) {
	return s.CreateJob(ctx, notoapi.CreateJobOpts{Kind: notoapi.JobReindex})
}

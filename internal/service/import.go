package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/repo"
)

// ImportAudio brings an existing audio file under noto's storage layout
// and queues a pipeline job to ingest/transcribe/summarize/index it.
// `srcPath` may be absolute or relative to the caller's CWD.
func (s *Service) ImportAudio(ctx context.Context, srcPath, title string) (notoapi.Meeting, notoapi.Job, error) {
	srcPath = strings.TrimSpace(srcPath)
	if srcPath == "" {
		return notoapi.Meeting{}, notoapi.Job{},
			notoapi.NewError(notoapi.CodeInvalidRequest, "path is required", nil)
	}
	abs, err := filepath.Abs(srcPath)
	if err != nil {
		return notoapi.Meeting{}, notoapi.Job{},
			notoapi.NewError(notoapi.CodeInvalidRequest, "could not resolve path: "+err.Error(), nil)
	}
	if _, err := os.Stat(abs); err != nil {
		return notoapi.Meeting{}, notoapi.Job{},
			notoapi.NewError(notoapi.CodeNotFound, "audio file not found: "+abs, nil)
	}

	mid := uuid.New()

	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}

	// Create the meeting manifest before touching the filesystem so that
	// if anything below fails the meeting is still visible (and deletable)
	// rather than leaving an orphaned audio file with no manifest.
	if err := s.repo.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{
		Title:  title,
		Reason: "audio_imported",
	}); err != nil {
		return notoapi.Meeting{}, notoapi.Job{}, err
	}

	ext := filepath.Ext(abs)
	dst, err := s.repo.PrepareAudio(ctx, mid, ext)
	if err != nil {
		_ = s.repo.DeleteMeeting(ctx, mid)
		return notoapi.Meeting{}, notoapi.Job{}, err
	}
	if err := copyFile(abs, dst); err != nil {
		_ = s.repo.DeleteMeeting(ctx, mid)
		return notoapi.Meeting{}, notoapi.Job{},
			notoapi.NewError(notoapi.CodeInternal, "copy audio: "+err.Error(), nil)
	}

	job, err := s.CreateJob(ctx, notoapi.CreateJobOpts{
		Kind:      notoapi.JobPipeline,
		MeetingID: mid.String(),
		Options: map[string]any{
			"title":       title,
			"output_path": dst,
			"source_path": abs,
		},
	})
	if err != nil {
		return notoapi.Meeting{}, notoapi.Job{}, err
	}

	return notoapi.Meeting{
		ID:    mid.String(),
		Title: title,
	}, job, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ensure fmt is used in case of future error wrapping.
var _ = fmt.Sprintf

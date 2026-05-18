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
	"github.com/lukasstrickler/noto/internal/storage"
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
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return notoapi.Meeting{}, notoapi.Job{}, err
	}
	if err := storage.EnsureDirs(layout); err != nil {
		return notoapi.Meeting{}, notoapi.Job{}, err
	}

	// Copy the audio to the meeting dir so subsequent jobs find it at
	// a stable path even if the caller deletes the source.
	dst := layout.AudioPath
	if ext := filepath.Ext(abs); ext != "" {
		// Replace the extension on the layout-provided default if the
		// source uses something else (.wav, .mp3, .flac).
		dst = strings.TrimSuffix(dst, filepath.Ext(dst)) + ext
	}
	if err := copyFile(abs, dst); err != nil {
		return notoapi.Meeting{}, notoapi.Job{},
			notoapi.NewError(notoapi.CodeInternal, "copy audio: "+err.Error(), nil)
	}

	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
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

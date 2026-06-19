package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// ImportAudio brings an existing audio file (referenced by a path on the
// SERVER's filesystem) under noto's storage layout and queues a pipeline job.
// This is the local / same-filesystem path (direct + UDS clients), where a
// zero-copy reference is correct. Remote (TCP) clients can't reach the server's
// filesystem — they stream bytes via ImportAudioStream instead.
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
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	return s.ingestAudio(ctx, filepath.Ext(abs), title, abs, func(dst string) error {
		return copyFile(abs, dst)
	})
}

// ImportAudioStream ingests audio from a byte stream (a remote client uploading
// over TCP). filename is used only to derive the extension + a default title;
// the bytes are streamed straight to the meeting's audio slot.
func (s *Service) ImportAudioStream(ctx context.Context, r io.Reader, filename, title string) (notoapi.Meeting, notoapi.Job, error) {
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".m4a" // sane default for noto captures
	}
	if strings.TrimSpace(title) == "" {
		base := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		if base == "" || base == "." {
			base = "Imported audio"
		}
		title = base
	}
	return s.ingestAudio(ctx, ext, title, "", func(dst string) error {
		return streamToFile(r, dst)
	})
}

// ingestAudio is the shared import path: create the meeting manifest first (so
// a failure leaves a visible, deletable meeting rather than an orphaned file),
// prepare the audio slot, write the bytes via `write`, then queue the pipeline.
// sourcePath is recorded for lineage when ingesting from a local path ("" for
// uploads).
func (s *Service) ingestAudio(ctx context.Context, ext, title, sourcePath string, write func(dst string) error) (notoapi.Meeting, notoapi.Job, error) {
	mid := uuid.New()
	if err := s.repo.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{
		Title:  title,
		Reason: "audio_imported",
	}); err != nil {
		return notoapi.Meeting{}, notoapi.Job{}, err
	}
	dst, err := s.repo.PrepareAudio(ctx, mid, ext)
	if err != nil {
		_ = s.repo.DeleteMeeting(ctx, mid)
		return notoapi.Meeting{}, notoapi.Job{}, err
	}
	if err := write(dst); err != nil {
		_ = s.repo.DeleteMeeting(ctx, mid)
		return notoapi.Meeting{}, notoapi.Job{},
			notoapi.NewError(notoapi.CodeInternal, "store audio: "+err.Error(), nil)
	}
	opts := map[string]any{
		"title":       title,
		"output_path": dst,
	}
	if sourcePath != "" {
		opts["source_path"] = sourcePath
	}
	job, err := s.CreateJob(ctx, notoapi.CreateJobOpts{
		Kind:      notoapi.JobPipeline,
		MeetingID: mid.String(),
		Options:   opts,
	})
	if err != nil {
		return notoapi.Meeting{}, notoapi.Job{}, err
	}
	return notoapi.Meeting{ID: mid.String(), Title: title}, job, nil
}

// streamToFile writes r to dst atomically (.tmp then rename).
func streamToFile(r io.Reader, dst string) error {
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
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
	return os.Rename(tmp, dst)
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

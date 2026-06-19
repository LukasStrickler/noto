package service

import (
	"context"
	"fmt"

	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// DownloadModel enqueues a JobDownloadModel that fetches a model (or the
// runtime) into the data dir, streaming progress over the event hub. Callers
// (CLI, TUI, or the lazy "model missing" trigger in transcribe) get a job whose
// progress they can watch via /v1/jobs/events — identically local and remote.
//
// modelID may be a manifest model id or the literal "runtime" to fetch the
// onnxruntime/sherpa shared library for the active backend. backend/tier empty
// → the host's resolved compute plan.
func (s *Service) DownloadModel(ctx context.Context, modelID string, backend compute.Backend, tier compute.Tier) (notoapi.Job, error) {
	if modelID == "" {
		return notoapi.Job{}, notoapi.NewError(notoapi.CodeInvalidRequest, "model id is required", nil)
	}
	if backend == "" {
		backend = s.compute.Backend
	}
	if tier == "" {
		tier = s.compute.Tier
	}
	return s.CreateJob(ctx, notoapi.CreateJobOpts{
		Kind: notoapi.JobDownloadModel,
		Options: map[string]any{
			"model_id": modelID,
			"backend":  string(backend),
			"tier":     string(tier),
		},
	})
}

// runDownloadModel executes a JobDownloadModel, reporting byte progress.
func (s *Service) runDownloadModel(ctx context.Context, job *notoapi.Job) error {
	modelID := jobOptString(job.Options, "model_id", "")
	if modelID == "" {
		return fmt.Errorf("download_model: missing model_id")
	}
	backend := compute.Backend(jobOptString(job.Options, "backend", string(s.compute.Backend)))
	tier := compute.Tier(jobOptString(job.Options, "tier", string(s.compute.Tier)))

	mgr := s.newModelManager(s.currentCfg().ConfigDir)
	mgr.OnProgress(func(asset string, downloaded, total int64) {
		frac := 0.0
		detail := fmt.Sprintf("%s %d MB", asset, downloaded>>20)
		if total > 0 {
			frac = float64(downloaded) / float64(total)
			detail = fmt.Sprintf("%s %d/%d MB", asset, downloaded>>20, total>>20)
		}
		s.publishProgress(job, "downloading "+modelID, frac, detail)
	})

	logf := func(format string, args ...any) {
		s.publishProgress(job, "downloading "+modelID, job.Progress, fmt.Sprintf(format, args...))
	}

	s.publishProgress(job, "downloading "+modelID, 0, "starting")
	if modelID == "runtime" {
		return mgr.EnsureRuntime(ctx, backend, logf)
	}
	return mgr.Download(ctx, modelID, backend, tier, logf)
}

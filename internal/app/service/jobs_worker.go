package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// --- worker pool ---

const workerPollInterval = 500 * time.Millisecond

func (s *Service) startWorkers(ctx context.Context) {
	// Pool size is posture-aware (ComputeConfig.JobWorkers): a small local pool
	// (heavy in-process models), or the GPU batch optimum when STT+diar are
	// offloaded so hosted-batch throughput isn't capped below what the remote GPU
	// can pack (jobs=10). Each worker blocks on its compute call, so N workers =
	// at most N concurrent meetings in flight.
	n := s.currentCfg().Compute.JobWorkers()
	if n < 1 {
		n = config.DefaultLocalJobWorkers
	}
	for i := 0; i < n; i++ {
		s.goBG(func() { s.workerLoop(ctx) })
	}
}

func (s *Service) notifyWorkers() {
	select {
	case s.workerWake <- struct{}{}:
	default:
	}
}

func (s *Service) workerLoop(ctx context.Context) {
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.workerWake:
		}
		for {
			// Stop claiming new work the moment shutdown begins, so Close's
			// wait drains promptly instead of running the whole queue down.
			if ctx.Err() != nil {
				return
			}
			job, ok := s.claimNextJob()
			if !ok {
				break
			}
			s.runJob(ctx, job)
		}
	}
}

// claimNextJob atomically picks the highest-priority queued job (lowest
// priority value; oldest first within a priority) and marks it running. This is
// the scheduler: interactive meeting-processing is claimed ahead of bulk reindex
// / model downloads, and deferred idle-GPU work (JobPriorityIdle) only after
// everything else drains. Returns ok=false if none are available.
func (s *Service) claimNextJob() (notoapi.Job, bool) {
	tx, err := s.jobsDB.Begin()
	if err != nil {
		return notoapi.Job{}, false
	}
	defer tx.Rollback()
	row := tx.QueryRow(
		`SELECT `+jobColumns+` FROM jobs WHERE status = ? ORDER BY priority, created_at LIMIT 1`,
		string(notoapi.JobQueued),
	)
	job, err := scanJob(row)
	if err != nil {
		return notoapi.Job{}, false
	}
	now := time.Now().UnixMilli()
	if _, err := tx.Exec(
		`UPDATE jobs SET status = ?, started_at = ?, attempt = attempt + 1 WHERE id = ? AND status = ?`,
		string(notoapi.JobRunning), now, job.ID, string(notoapi.JobQueued),
	); err != nil {
		return notoapi.Job{}, false
	}
	if err := tx.Commit(); err != nil {
		return notoapi.Job{}, false
	}
	job.Status = notoapi.JobRunning
	t := time.UnixMilli(now)
	job.StartedAt = &t
	job.Attempt++
	return job, true
}

func (s *Service) runJob(parentCtx context.Context, job notoapi.Job) {
	ctx, cancel := context.WithCancel(parentCtx)
	s.jobsMu.Lock()
	s.jobCancels[job.ID] = cancel
	s.jobsMu.Unlock()
	defer func() {
		cancel()
		s.jobsMu.Lock()
		delete(s.jobCancels, job.ID)
		s.jobsMu.Unlock()
	}()

	s.publishJob(job)

	var err error
	switch job.Kind {
	case notoapi.JobIngest:
		err = s.runIngest(ctx, &job)
	case notoapi.JobTranscribe:
		err = s.runTranscribe(ctx, &job)
	case notoapi.JobSummarize:
		err = s.runSummarize(ctx, &job)
	case notoapi.JobIndex, notoapi.JobReindex:
		err = s.runIndex(ctx, &job)
	case notoapi.JobVerify:
		err = s.runVerify(ctx, &job)
	case notoapi.JobPipeline:
		err = s.runPipeline(ctx, &job)
	case notoapi.JobDownloadModel:
		err = s.runDownloadModel(ctx, &job)
	default:
		err = fmt.Errorf("unknown job kind %q", job.Kind)
	}

	now := time.Now().UnixMilli()
	final := notoapi.JobSucceeded
	errStr := ""
	if err != nil {
		if ctx.Err() != nil {
			final = notoapi.JobCanceled
		} else {
			final = notoapi.JobFailed
		}
		errStr = err.Error()
	}
	_, _ = s.jobsDB.Exec(
		`UPDATE jobs SET status = ?, finished_at = ?, error = ?, progress = ? WHERE id = ?`,
		string(final), now, errStr, 1.0, job.ID,
	)
	job.Status = final
	t := time.UnixMilli(now)
	job.FinishedAt = &t
	job.Error = errStr
	job.Progress = 1.0
	s.publishJob(job)
}

// publishProgress is what workers call to report progress mid-job.
func (s *Service) publishProgress(job *notoapi.Job, phase string, progress float64, detail string) {
	job.Phase = phase
	job.Progress = progress
	job.Detail = detail
	_, _ = s.jobsDB.Exec(
		`UPDATE jobs SET phase = ?, progress = ?, detail = ? WHERE id = ?`,
		phase, progress, detail, job.ID,
	)
	s.publishJob(*job)
}

func (s *Service) publishJob(job notoapi.Job) {
	s.events.publish(notoapi.Event{Kind: notoapi.EventJob, Job: &job})
	// Job state changes affect the status bar counts; emit once per
	// publishJob rather than on a timer.
	switch job.Status {
	case notoapi.JobRunning, notoapi.JobQueued, notoapi.JobSucceeded,
		notoapi.JobFailed, notoapi.JobCanceled, notoapi.JobInterrupted:
		s.emitStatusBar()
	}
}

// statusBarLoop emits a status_bar event once at startup so a freshly
// subscribed client sees the current numbers, then only on demand.
// Subsequent emissions are driven by publishJob (after a job state
// change) and publishRecorder (on start/stop). This keeps idle CPU at
// zero — no global tick.
func (s *Service) statusBarLoop(ctx context.Context) {
	// Initial emit (with a tiny delay to let subscribers attach).
	select {
	case <-ctx.Done():
		return
	case <-time.After(200 * time.Millisecond):
	}
	s.emitStatusBar()
}

func (s *Service) emitStatusBar() {
	bar := notoapi.StatusBar{
		IndexState: "clean",
	}
	rec, _ := s.GetRecording(context.Background())
	bar.RecordingActive = rec.Active
	bar.RecordingTitle = rec.Title
	bar.ElapsedSec = rec.ElapsedSec
	// Cheap directory count — emitStatusBar runs on every job event, so it
	// must not re-read and JSON-parse every meeting's artifacts.
	if n, err := s.repo.CountMeetings(context.Background()); err == nil {
		bar.MeetingCount = n
	} else {
		bar.IndexState = "error"
	}
	row := s.jobsDB.QueryRow(`SELECT
		SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END),
		SUM(CASE WHEN status = 'queued'  THEN 1 ELSE 0 END)
	FROM jobs`)
	var running, queued sql.NullInt64
	_ = row.Scan(&running, &queued)
	bar.JobsRunning = int(running.Int64)
	bar.JobsQueued = int(queued.Int64)
	if s.meetingMappings != nil {
		if n, err := s.meetingMappings.CountUnresolved(context.Background()); err == nil {
			bar.SpeakersToID = n
		}
	}
	// Per-screen attention: the nav strip flags each page that owns work, so
	// these route to People (provisional profiles) and Config (broken routes).
	bar.PeopleToReview = s.countPeopleToReview()
	bar.ConfigIssues = s.countConfigIssues(context.Background())
	s.events.publish(notoapi.Event{Kind: notoapi.EventStatusBar, StatusBar: &bar})
}

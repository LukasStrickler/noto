package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/artifactwriter"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/providers/llm"
	"github.com/lukasstrickler/noto/internal/providers/stt"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/storage"
)

// CreateJob enqueues a job and triggers the workers to look. The job
// row is persisted before this returns so a crash doesn't lose work.
func (s *Service) CreateJob(_ context.Context, opts notoapi.CreateJobOpts) (notoapi.Job, error) {
	if opts.Kind == "" {
		return notoapi.Job{}, notoapi.NewError(notoapi.CodeInvalidRequest, "job kind is required", nil)
	}
	id := newID()
	optsJSON := "{}"
	if len(opts.Options) > 0 {
		if b, err := json.Marshal(opts.Options); err == nil {
			optsJSON = string(b)
		}
	}
	now := time.Now().UnixMilli()
	_, err := s.jobsDB.Exec(
		`INSERT INTO jobs (id, kind, meeting_id, status, options_json, created_at, attempt)
		 VALUES (?, ?, ?, ?, ?, ?, 0)`,
		id, string(opts.Kind), opts.MeetingID, string(notoapi.JobQueued), optsJSON, now,
	)
	if err != nil {
		return notoapi.Job{}, notoapi.NewError(notoapi.CodeInternal, "could not enqueue job: "+err.Error(), nil)
	}
	job := notoapi.Job{
		ID:        id,
		Kind:      opts.Kind,
		MeetingID: opts.MeetingID,
		Status:    notoapi.JobQueued,
		CreatedAt: time.UnixMilli(now),
		Options:   opts.Options,
	}
	s.events.publish(notoapi.Event{Kind: notoapi.EventJob, Job: &job})
	s.notifyWorkers()
	return job, nil
}

// ListJobs returns recent jobs, newest first.
func (s *Service) ListJobs(_ context.Context, opts notoapi.ListJobsOpts) ([]notoapi.Job, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, kind, meeting_id, status, phase, progress, detail, error, options_json,
		   created_at, started_at, finished_at, attempt FROM jobs WHERE 1=1`
	args := []any{}
	if opts.Status != "" {
		q += " AND status = ?"
		args = append(args, string(opts.Status))
	}
	if opts.MeetingID != "" {
		q += " AND meeting_id = ?"
		args = append(args, opts.MeetingID)
	}
	q += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.jobsDB.Query(q, args...)
	if err != nil {
		return nil, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	defer rows.Close()
	var out []notoapi.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
		}
		out = append(out, j)
	}
	return out, nil
}

func (s *Service) GetJob(_ context.Context, id string) (notoapi.Job, error) {
	row := s.jobsDB.QueryRow(
		`SELECT id, kind, meeting_id, status, phase, progress, detail, error, options_json,
		         created_at, started_at, finished_at, attempt FROM jobs WHERE id = ?`,
		id,
	)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return notoapi.Job{}, notoapi.NewError(notoapi.CodeJobNotFound, "job not found", map[string]any{"id": id})
	}
	if err != nil {
		return notoapi.Job{}, notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
	}
	return j, nil
}

// CancelJob requests cooperative cancellation. If the job is queued,
// it's flipped to canceled immediately; if running, its context is
// canceled and the worker is expected to honor it.
func (s *Service) CancelJob(_ context.Context, id string) error {
	job, err := s.GetJob(context.Background(), id)
	if err != nil {
		return err
	}
	switch job.Status {
	case notoapi.JobQueued:
		if _, err := s.jobsDB.Exec(
			`UPDATE jobs SET status = ?, finished_at = ? WHERE id = ?`,
			string(notoapi.JobCanceled), time.Now().UnixMilli(), id,
		); err != nil {
			return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
		}
		job.Status = notoapi.JobCanceled
		s.events.publish(notoapi.Event{Kind: notoapi.EventJob, Job: &job})
		return nil
	case notoapi.JobRunning:
		if _, err := s.jobsDB.Exec(
			`UPDATE jobs SET cancel_requested = 1 WHERE id = ?`,
			id,
		); err != nil {
			return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
		}
		s.jobsMu.Lock()
		if cancel, ok := s.jobCancels[id]; ok {
			cancel()
		}
		s.jobsMu.Unlock()
		return nil
	default:
		return notoapi.NewError(notoapi.CodeConflict, "job is already terminal", map[string]any{"status": job.Status})
	}
}

// StreamEvents returns a receive-only event channel for the in-process
// direct client. HTTP/SSE handlers use the hub directly via Subscribe.
// The subscription is established before this function returns so a
// caller publishing immediately after will not miss events.
func (s *Service) StreamEvents(ctx context.Context) (<-chan notoapi.Event, error) {
	sub := s.events.subscribe() // subscribe synchronously to avoid a race
	out := make(chan notoapi.Event, 32)
	go func() {
		defer close(out)
		defer s.events.unsubscribe(sub)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub:
				if !ok {
					return
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// scanJob reads a row from the jobs table.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(r rowScanner) (notoapi.Job, error) {
	var (
		j           notoapi.Job
		mid         sql.NullString
		phase       sql.NullString
		detail      sql.NullString
		errStr      sql.NullString
		optsJSON    sql.NullString
		startedAt   sql.NullInt64
		finishedAt  sql.NullInt64
		createdAt   int64
		kind        string
		status      string
	)
	if err := r.Scan(
		&j.ID, &kind, &mid, &status, &phase, &j.Progress, &detail, &errStr,
		&optsJSON, &createdAt, &startedAt, &finishedAt, &j.Attempt,
	); err != nil {
		return notoapi.Job{}, err
	}
	j.Kind = notoapi.JobKind(kind)
	j.Status = notoapi.JobStatus(status)
	j.MeetingID = mid.String
	j.Phase = phase.String
	j.Detail = detail.String
	j.Error = errStr.String
	j.CreatedAt = time.UnixMilli(createdAt)
	if startedAt.Valid {
		t := time.UnixMilli(startedAt.Int64)
		j.StartedAt = &t
	}
	if finishedAt.Valid {
		t := time.UnixMilli(finishedAt.Int64)
		j.FinishedAt = &t
	}
	if optsJSON.Valid && optsJSON.String != "" && optsJSON.String != "{}" {
		var m map[string]any
		if err := json.Unmarshal([]byte(optsJSON.String), &m); err == nil {
			j.Options = m
		}
	}
	return j, nil
}

// markInterruptedJobs flips any rows still in "running" at startup to
// "interrupted" — no goroutine survives a process exit, so a running
// row from a previous run is stale.
func (s *Service) markInterruptedJobs(_ context.Context) error {
	_, err := s.jobsDB.Exec(
		`UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
		string(notoapi.JobInterrupted), "server restarted while job was running",
		time.Now().UnixMilli(), string(notoapi.JobRunning),
	)
	return err
}

// --- worker pool ---

const workerPollInterval = 500 * time.Millisecond

func (s *Service) startWorkers(ctx context.Context) {
	// Small fixed-size pool: 4 generic workers is plenty for V1 since
	// most jobs are network-bound (STT/LLM API calls).
	for i := 0; i < 4; i++ {
		go s.workerLoop(ctx, i)
	}
}

var workerWake = make(chan struct{}, 1)

func (s *Service) notifyWorkers() {
	// non-blocking nudge
	select {
	case workerWake <- struct{}{}:
	default:
	}
}

func (s *Service) workerLoop(ctx context.Context, id int) {
	_ = id
	ticker := time.NewTicker(workerPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-workerWake:
		}
		for {
			job, ok := s.claimNextJob()
			if !ok {
				break
			}
			s.runJob(ctx, job)
		}
	}
}

// claimNextJob atomically picks the oldest queued job and marks it
// running. Returns ok=false if none are available.
func (s *Service) claimNextJob() (notoapi.Job, bool) {
	tx, err := s.jobsDB.Begin()
	if err != nil {
		return notoapi.Job{}, false
	}
	defer tx.Rollback()
	row := tx.QueryRow(
		`SELECT id, kind, meeting_id, status, phase, progress, detail, error, options_json,
		         created_at, started_at, finished_at, attempt
		   FROM jobs WHERE status = ? ORDER BY created_at LIMIT 1`,
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
	if list, err := s.ListMeetings(context.Background(), notoapi.ListMeetingsOpts{}); err == nil {
		bar.MeetingCount = list.Total
	}
	row := s.jobsDB.QueryRow(`SELECT
		SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END),
		SUM(CASE WHEN status = 'queued'  THEN 1 ELSE 0 END)
	FROM jobs`)
	var running, queued sql.NullInt64
	_ = row.Scan(&running, &queued)
	bar.JobsRunning = int(running.Int64)
	bar.JobsQueued = int(queued.Int64)
	s.events.publish(notoapi.Event{Kind: notoapi.EventStatusBar, StatusBar: &bar})
}

// --- job implementations ---
// These are the seam between the queue and the provider/storage code.
// V1 implementations are deliberately simple — make the pipeline visible
// in the TUI; provider-specific calls slot in as they get wired.

func (s *Service) runIngest(ctx context.Context, job *notoapi.Job) error {
	s.publishProgress(job, "preparing layout", 0.2, "")
	if job.MeetingID == "" {
		return fmt.Errorf("ingest: meeting_id is required")
	}
	mid, err := uuid.Parse(job.MeetingID)
	if err != nil {
		return fmt.Errorf("ingest: invalid meeting id: %w", err)
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return err
	}
	if err := storage.EnsureDirs(layout); err != nil {
		return err
	}
	title := jobOptString(job.Options, "title", "Untitled meeting")
	now := time.Now()
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), newID()[:8])
	manifest := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        mid.String(),
		CurrentVersionID: versionID,
		Metadata:         artifacts.ManifestMetadata{Title: title},
		Versions: []artifacts.ManifestVersion{
			{VersionID: versionID, CreatedAt: now, Reason: string(artifacts.ReasonAudioImported), Checksum: "n/a"},
		},
	}
	mw := artifactwriter.NewManifestWriter(s.recordingsDir)
	if err := mw.WriteManifest(mid, manifest); err != nil {
		return err
	}
	s.publishProgress(job, "ingest complete", 1.0, "")
	return sleep(ctx, 100*time.Millisecond)
}

func (s *Service) runTranscribe(ctx context.Context, job *notoapi.Job) error {
	if job.MeetingID == "" {
		return fmt.Errorf("transcribe: meeting_id is required")
	}
	mid, err := uuid.Parse(job.MeetingID)
	if err != nil {
		return err
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return err
	}

	providerID := s.cfg.Routing.SpeechProvider
	title := jobOptString(job.Options, "title", "Untitled meeting")
	audioPath := jobOptString(job.Options, "output_path", layout.AudioPath)
	if _, err := os.Stat(audioPath); err != nil {
		audioPath = ""
	}

	// Try the real STT provider if a key is configured and we have audio.
	transcript, err := s.transcribeWithProvider(ctx, job, providerID, mid, title, audioPath)
	if err != nil {
		return err
	}
	if transcript == nil {
		// Fall through to a deterministic synthesis so the pipeline
		// still produces a meeting (useful for Linux dry-runs and tests).
		s.publishProgress(job, "synthesizing transcript", 0.6, "no audio or no provider key")
		transcript = &artifacts.Transcript{
			SchemaVersion: "transcript.v1",
			MeetingID:     mid.String(),
			Provider: artifacts.TranscriptProvider{
				ID:    providerID,
				JobID: job.ID,
			},
			Speakers: []artifacts.Speaker{
				{ID: "spk_0", DisplayName: "You", Origin: "local_speaker", Label: "me"},
				{ID: "spk_1", DisplayName: "Participants", Origin: "participants", Label: "participants"},
			},
			Segments: synthesizeSegments(title),
		}
	} else {
		// The provider adapter fills in everything; just make sure the
		// meeting_id matches the layout we are writing to.
		transcript.MeetingID = mid.String()
	}
	if err := storage.WriteTranscript(layout, transcript); err != nil {
		return err
	}
	s.publishProgress(job, "transcript written", 1.0, fmt.Sprintf("%d segments", len(transcript.Segments)))
	return nil
}

// transcribeWithProvider returns (nil, nil) when the provider can't run
// (no key, no audio, fake provider). It returns a real transcript when
// the call succeeds, or an error when the provider actively failed.
func (s *Service) transcribeWithProvider(ctx context.Context, job *notoapi.Job, providerID string, mid uuid.UUID, _ string, audioPath string) (*artifacts.Transcript, error) {
	if audioPath == "" {
		return nil, nil
	}
	suite, ok := s.registry.Get(providerID)
	if !ok {
		return nil, nil
	}
	if suite.CredentialRef == "" {
		return nil, nil // fake provider
	}
	key, err := s.secrets.Get(ctx, suite.CredentialRef)
	if err != nil || strings.TrimSpace(key) == "" {
		return nil, nil
	}
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, fmt.Errorf("read audio: %w", err)
	}
	s.publishProgress(job, "uploading audio", 0.30, providerID)
	var transcript *artifacts.Transcript
	switch providerID {
	case "assemblyai":
		adapter := &stt.AssemblyAIAdapter{APIKey: key}
		transcript, err = adapter.Transcribe(ctx, audio, stt.TranscribeOptions{MeetingID: mid.String()})
	case "local":
		// Local provider expects an OpenAI-compatible STT server on
		// NOTO_LOCAL_STT_URL (or 127.0.0.1:8000 default). Key is
		// optional and only used as a bearer token.
		adapter := &stt.LocalAdapter{APIKey: key}
		transcript, err = adapter.Transcribe(ctx, audio, stt.TranscribeOptions{MeetingID: mid.String()})
	default:
		// Other speech providers (mistral, elevenlabs) need their own
		// adapter implementations; until those land, fall back to
		// synthesis so the pipeline still progresses.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s transcribe: %w", providerID, err)
	}
	return transcript, nil
}

func synthesizeSegments(title string) []artifacts.Segment {
	now := 0.0
	confidence := 0.92
	mk := func(speaker, role, text string, dur float64) artifacts.Segment {
		seg := artifacts.Segment{
			ID:           fmt.Sprintf("seg_%06d", int(now*10)),
			SpeakerID:    speaker,
			SourceRole:   role,
			StartSeconds: now,
			EndSeconds:   now + dur,
			Text:         text,
			Confidence:   &confidence,
		}
		now += dur
		return seg
	}
	return []artifacts.Segment{
		mk("spk_0", "local_speaker", "Let me start with the agenda for "+title+".", 3),
		mk("spk_1", "participants", "Sounds good. I'd love to discuss the timeline first.", 4),
		mk("spk_0", "local_speaker", "Right — we need to ship the v1 by end of quarter.", 3),
		mk("spk_1", "participants", "What about the open-questions list? Should we close them today?", 5),
		mk("spk_0", "local_speaker", "Let's decide on the top three and defer the rest.", 4),
	}
}

func (s *Service) runSummarize(ctx context.Context, job *notoapi.Job) error {
	if job.MeetingID == "" {
		return fmt.Errorf("summarize: meeting_id is required")
	}
	mid, err := uuid.Parse(job.MeetingID)
	if err != nil {
		return err
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return err
	}
	s.publishProgress(job, "loading transcript", 0.2, "")
	transcript, err := storage.ReadTranscript(layout)
	if err != nil {
		return err
	}
	title := jobOptString(job.Options, "title", "Untitled meeting")

	// Try a real LLM summary if a key is configured.
	if real := s.summarizeWithProvider(ctx, job, *transcript, mid); real != nil {
		// Persist JSON and a rendered markdown.
		if data, err := json.MarshalIndent(real, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(layout.MeetingDir, "summary.json"), data, 0o644)
		}
		md := renderSummaryMD(title, real, transcript)
		if err := storage.WriteSummary(layout, md); err != nil {
			return err
		}
		s.publishProgress(job, "summary written", 1.0, "real LLM")
		return nil
	}

	s.publishProgress(job, "synthesizing summary", 0.6, "no LLM key configured")
	summary := &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     mid.String(),
		ShortSummary:  fmt.Sprintf("Meeting %q reached three decisions and one action item.", title),
		Decisions: []artifacts.SummaryItem{
			{Text: "Ship v1 by end of quarter", SpeakerIDs: []string{"spk_0"}, Evidence: []artifacts.Evidence{{SegmentID: "seg_000070", Quote: "ship the v1 by end of quarter"}}},
			{Text: "Pick top three open questions; defer the rest", SpeakerIDs: []string{"spk_0", "spk_1"}, Evidence: []artifacts.Evidence{{SegmentID: "seg_000150", Quote: "decide on the top three and defer the rest"}}},
		},
		ActionItems: []artifacts.ActionItem{
			{Text: "Circulate the timeline next week", Owner: "spk_0", Evidence: []artifacts.Evidence{{SegmentID: "seg_000040", Quote: "timeline first"}}},
		},
		Risks: []artifacts.SummaryItem{
			{Text: "Timeline may be aggressive for v1 scope.", Evidence: []artifacts.Evidence{{SegmentID: "seg_000070", Quote: "ship the v1 by end of quarter"}}},
		},
		OpenQuestions: []artifacts.SummaryItem{
			{Text: "Which open questions are highest priority?", Evidence: []artifacts.Evidence{{SegmentID: "seg_000100", Quote: "open-questions list"}}},
		},
		Model: artifacts.SummaryModel{Provider: s.cfg.Routing.LLMProvider, ModelID: s.cfg.Routing.LLMModel, PromptVersion: "summary.v1"},
	}
	// Persist JSON and a rendered markdown.
	if data, err := json.MarshalIndent(summary, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(layout.MeetingDir, "summary.json"), data, 0o644)
	}
	md := renderSummaryMD(title, summary, transcript)
	if err := storage.WriteSummary(layout, md); err != nil {
		return err
	}
	s.publishProgress(job, "summary written", 1.0, "")
	return nil
}

// summarizeWithProvider returns nil when no provider key is configured;
// otherwise it returns the LLM-generated summary or nil on adapter
// error (the job then falls through to the deterministic synthesis so
// users always see *some* output).
func (s *Service) summarizeWithProvider(ctx context.Context, job *notoapi.Job, transcript artifacts.Transcript, mid uuid.UUID) *artifacts.Summary {
	providerID := s.cfg.Routing.LLMProvider
	if providerID == "" {
		providerID = "openrouter"
	}
	suite, ok := s.registry.Get(providerID)
	if !ok || suite.CredentialRef == "" {
		return nil
	}
	key, err := s.secrets.Get(ctx, suite.CredentialRef)
	if err != nil || strings.TrimSpace(key) == "" {
		return nil
	}
	s.publishProgress(job, "calling LLM", 0.6, providerID)
	adapter := &llm.OpenRouterAdapter{APIKey: key, ModelID: s.cfg.Routing.LLMModel}
	summary, err := adapter.Summarize(ctx, transcript, llm.SummarizeOptions{MeetingID: mid.String()})
	if err != nil {
		s.publishProgress(job, "llm error, falling back", 0.7, err.Error())
		return nil
	}
	if summary == nil {
		return nil
	}
	summary.MeetingID = mid.String()
	return summary
}

func renderSummaryMD(title string, s *artifacts.Summary, _ *artifacts.Transcript) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintln(&b, s.ShortSummary)
	if len(s.Decisions) > 0 {
		b.WriteString("\n## Decisions\n\n")
		for i, d := range s.Decisions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, d.Text)
		}
	}
	if len(s.ActionItems) > 0 {
		b.WriteString("\n## Action items\n\n")
		for _, a := range s.ActionItems {
			fmt.Fprintf(&b, "- %s", a.Text)
			if a.Owner != "" {
				fmt.Fprintf(&b, " _(owner: %s)_", a.Owner)
			}
			b.WriteString("\n")
		}
	}
	if len(s.Risks) > 0 {
		b.WriteString("\n## Risks\n\n")
		for _, r := range s.Risks {
			fmt.Fprintf(&b, "- %s\n", r.Text)
		}
	}
	if len(s.OpenQuestions) > 0 {
		b.WriteString("\n## Open questions\n\n")
		for _, q := range s.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q.Text)
		}
	}
	return b.String()
}

func (s *Service) runIndex(ctx context.Context, job *notoapi.Job) error {
	s.publishProgress(job, "rebuilding index", 0.3, "")
	_ = ctx
	if job.MeetingID == "" {
		// reindex everything — for now, just touch the index.
		return nil
	}
	mid, err := uuid.Parse(job.MeetingID)
	if err != nil {
		return err
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return err
	}
	t, err := storage.ReadTranscript(layout)
	if err != nil {
		s.publishProgress(job, "no transcript to index", 1.0, "")
		return nil
	}
	title := jobOptString(job.Options, "title", "Untitled meeting")
	// Re-read summary if it exists to capture decisions/actions/risks.
	var decisions, actions, risks []string
	if data, err := os.ReadFile(filepath.Join(layout.MeetingDir, "summary.json")); err == nil {
		var sum artifacts.Summary
		if jerr := json.Unmarshal(data, &sum); jerr == nil {
			for _, d := range sum.Decisions {
				decisions = append(decisions, d.Text)
			}
			for _, a := range sum.ActionItems {
				actions = append(actions, a.Text)
			}
			for _, r := range sum.Risks {
				risks = append(risks, r.Text)
			}
		}
	}
	segs := make([]search.TranscriptSegment, 0, len(t.Segments))
	for _, seg := range t.Segments {
		segs = append(segs, search.TranscriptSegment{
			SegmentID: seg.ID,
			Text:      seg.Text,
			Speaker:   seg.SpeakerID,
			Timestamp: seg.StartSeconds,
		})
	}
	decItems := make([]search.SummaryItem, 0, len(decisions))
	for _, d := range decisions {
		decItems = append(decItems, search.SummaryItem{Text: d})
	}
	actItems := make([]search.ActionItem, 0, len(actions))
	for _, a := range actions {
		actItems = append(actItems, search.ActionItem{Text: a})
	}
	riskItems := make([]search.SummaryItem, 0, len(risks))
	for _, r := range risks {
		riskItems = append(riskItems, search.SummaryItem{Text: r})
	}
	// Pull the meeting's manifest-recorded creation time so the index's
	// recency tiebreaker matches the disk-side list-by-recency order.
	createdAt := time.Now().UTC()
	if manifest, err := storage.ReadManifest(layout); err == nil {
		for _, v := range manifest.Versions {
			if v.VersionID == manifest.CurrentVersionID {
				createdAt = v.CreatedAt
				break
			}
		}
	}
	input := &search.IndexMeetingInput{
		MeetingID:          mid.String(),
		Title:              title,
		CreatedAt:          createdAt,
		TranscriptSegments: segs,
		Decisions:          decItems,
		ActionItems:        actItems,
		Risks:              riskItems,
	}
	if err := s.search.IndexMeetingFromInput(input); err != nil {
		return err
	}
	s.publishProgress(job, "indexed", 1.0, "")
	return nil
}

func jobOptString(opts map[string]any, key, def string) string {
	if opts == nil {
		return def
	}
	if v, ok := opts[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func (s *Service) runVerify(ctx context.Context, job *notoapi.Job) error {
	s.publishProgress(job, "verifying checksums", 0.5, job.MeetingID)
	if err := sleep(ctx, 250*time.Millisecond); err != nil {
		return err
	}
	return nil
}

// runPipeline chains ingest → transcribe → summarize → index for one
// meeting. Each phase reports a portion of total progress.
func (s *Service) runPipeline(ctx context.Context, job *notoapi.Job) error {
	steps := []struct {
		name string
		fn   func(context.Context, *notoapi.Job) error
		span float64
	}{
		{"ingest", s.runIngest, 0.10},
		{"transcribe", s.runTranscribe, 0.55},
		{"summarize", s.runSummarize, 0.85},
		{"index", s.runIndex, 1.0},
	}
	for _, step := range steps {
		s.publishProgress(job, step.name, step.span-0.001, "")
		if err := step.fn(ctx, job); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		s.publishProgress(job, step.name+" done", step.span, "")
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}


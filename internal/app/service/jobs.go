package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
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
	// Priority is the caller's explicit override, else the kind's default. This is
	// the scheduler's ordering key: the worker pool claims the lowest value first,
	// so interactive meeting-processing runs ahead of bulk/deferred work.
	priority := opts.Priority
	if priority == 0 {
		priority = opts.Kind.Priority()
	}
	now := time.Now().UnixMilli()
	_, err := s.jobsDB.Exec(
		`INSERT INTO jobs (id, kind, meeting_id, status, options_json, created_at, attempt, priority)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		id, string(opts.Kind), opts.MeetingID, string(notoapi.JobQueued), optsJSON, now, int(priority),
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
		Priority:  priority,
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
	q := `SELECT ` + jobColumns + ` FROM jobs WHERE 1=1`
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
	if err := rows.Err(); err != nil {
		return nil, notoapi.NewError(notoapi.CodeInternal, "list jobs: "+err.Error(), nil)
	}
	return out, nil
}

func (s *Service) GetJob(_ context.Context, id string) (notoapi.Job, error) {
	row := s.jobsDB.QueryRow(
		`SELECT `+jobColumns+` FROM jobs WHERE id = ?`,
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
			cancel(errJobCanceledByUser)
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

// jobColumns is the column list every job SELECT shares; it must stay in
// the order scanJob reads them. Keeping it in one place stops the queries
// from drifting out of sync with the scan.
const jobColumns = `id, kind, meeting_id, status, phase, progress, detail, error, options_json,
	created_at, started_at, finished_at, attempt, priority`

// scanJob reads a row from the jobs table.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(r rowScanner) (notoapi.Job, error) {
	var (
		j          notoapi.Job
		mid        sql.NullString
		phase      sql.NullString
		detail     sql.NullString
		errStr     sql.NullString
		optsJSON   sql.NullString
		startedAt  sql.NullInt64
		finishedAt sql.NullInt64
		createdAt  int64
		kind       string
		status     string
		priority   int
	)
	if err := r.Scan(
		&j.ID, &kind, &mid, &status, &phase, &j.Progress, &detail, &errStr,
		&optsJSON, &createdAt, &startedAt, &finishedAt, &j.Attempt, &priority,
	); err != nil {
		return notoapi.Job{}, err
	}
	j.Kind = notoapi.JobKind(kind)
	j.Status = notoapi.JobStatus(status)
	j.Priority = notoapi.JobPriority(priority)
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

// maxJobAttempts caps how many times a job may be (re-)claimed before it's given
// up. claimNextJob increments attempt on every claim, so a job whose work crashes
// the process is resumed across restarts only up to this limit — past it, a
// poison-pill job is parked rather than crash-looping the server on every startup.
const maxJobAttempts = 3

// recoverInterruptedJobs handles rows still "running" at startup — no goroutine
// survives a process exit, so such a row is from a previous run that died
// mid-flight. Rather than stranding the user's meeting, a job still under the
// attempt cap is RE-QUEUED to finish: every pipeline stage overwrites its artifact
// (transcribe/summarize/index are idempotent), so re-running from the start is
// safe, and the row keeps its priority so it re-enters the queue at its original
// tier. A job that has already exhausted its attempts is parked as "interrupted"
// (the poison-pill guard) so a reliably-crashing job can't crash-loop the server.
func (s *Service) recoverInterruptedJobs(_ context.Context) error {
	// Honor a cancellation that was in flight when the process died: a job the
	// user explicitly canceled (cancel_requested=1) must NOT be resumed — finalize
	// it as canceled. Runs first so the resume step below can't resurrect it. (The
	// live path honors this via the cancel cause; this covers a crash between the
	// CancelJob write and the worker finalizing.)
	if _, err := s.jobsDB.Exec(
		`UPDATE jobs SET status = ?, finished_at = ?, error = '' WHERE status = ? AND cancel_requested = 1`,
		string(notoapi.JobCanceled), time.Now().UnixMilli(), string(notoapi.JobRunning),
	); err != nil {
		return err
	}
	// Resume the resumable ones: back to queued, progress/phase reset so the UI
	// shows a fresh run. attempt is preserved (it increments again on re-claim).
	if _, err := s.jobsDB.Exec(
		`UPDATE jobs SET status = ?, progress = 0, phase = '', detail = ?, error = '',
			started_at = NULL, finished_at = NULL
		 WHERE status = ? AND attempt < ?`,
		string(notoapi.JobQueued), "resuming after restart",
		string(notoapi.JobRunning), maxJobAttempts,
	); err != nil {
		return err
	}
	// Park the rest (attempt cap reached): resuming again would just repeat the
	// crash each startup.
	_, err := s.jobsDB.Exec(
		`UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
		string(notoapi.JobInterrupted), "exceeded retry limit after server restart",
		time.Now().UnixMilli(), string(notoapi.JobRunning),
	)
	return err
}

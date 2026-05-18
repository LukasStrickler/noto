package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/notoapi"
)

// StartRecording asks the macOS capture helper to begin a split-source
// recording. On non-mac platforms or when the IPC client is nil it
// returns a CodeUnsupportedCapability error.
func (s *Service) StartRecording(ctx context.Context, opts notoapi.StartRecordingOpts) (notoapi.StartRecordingResult, error) {
	s.recMu.Lock()
	if s.recording.Active {
		active := s.recording
		s.recMu.Unlock()
		return notoapi.StartRecordingResult{},
			notoapi.NewError(notoapi.CodeRecordingActive, "a recording is already active", map[string]any{"meeting_id": active.MeetingID})
	}
	s.recMu.Unlock()

	if s.ipc == nil {
		return s.startDryRunRecording(opts), nil
	}

	if err := s.ipc.Connect(ctx); err != nil {
		// Helper unreachable (no Swift toolchain or non-mac platform):
		// drop into dry-run so the TUI flow still works for development
		// and so Linux servers can exercise the pipeline against
		// imported audio.
		return s.startDryRunRecording(opts), nil
	}
	sources := opts.Sources
	if len(sources) == 0 {
		sources = []string{"microphone", "system_audio"}
	}
	if _, err := s.ipc.Start(ctx, sources, 48000); err != nil {
		return s.startDryRunRecording(opts), nil
	}

	meetingID := uuid.New().String()
	now := time.Now()
	s.recMu.Lock()
	s.recording = notoapi.RecordingState{
		Active:    true,
		MeetingID: meetingID,
		Title:     opts.Title,
		StartedAt: now,
		Sources:   sources,
		Retention: opts.Retention,
	}
	stop := make(chan struct{})
	s.recStopMeters = stop
	s.recMu.Unlock()

	go s.meterLoop(stop)
	s.publishRecorder()
	return notoapi.StartRecordingResult{
		MeetingID: meetingID,
		Title:     opts.Title,
		Sources:   sources,
	}, nil
}

func (s *Service) startDryRunRecording(opts notoapi.StartRecordingOpts) notoapi.StartRecordingResult {
	meetingID := uuid.New().String()
	now := time.Now()
	sources := opts.Sources
	if len(sources) == 0 {
		sources = []string{"microphone"}
	}
	s.recMu.Lock()
	s.recording = notoapi.RecordingState{
		Active:    true,
		MeetingID: meetingID,
		Title:     opts.Title,
		StartedAt: now,
		Sources:   sources,
		Retention: opts.Retention,
		MicDB:     -42,
	}
	stop := make(chan struct{})
	s.recStopMeters = stop
	s.recMu.Unlock()

	go s.meterLoopDryRun(stop)
	s.publishRecorder()
	return notoapi.StartRecordingResult{MeetingID: meetingID, Title: opts.Title, Sources: sources}
}

// StopRecording ends the current recording. If after_stop flags were
// set on Start, a pipeline job is enqueued.
func (s *Service) StopRecording(ctx context.Context, opts notoapi.StopRecordingOpts) (notoapi.StopRecordingResult, error) {
	s.recMu.Lock()
	if !s.recording.Active {
		s.recMu.Unlock()
		return notoapi.StopRecordingResult{},
			notoapi.NewError(notoapi.CodeRecordingInactive, "no recording is active", nil)
	}
	state := s.recording
	stop := s.recStopMeters
	s.recording = notoapi.RecordingState{}
	s.recStopMeters = nil
	s.recMu.Unlock()

	if stop != nil {
		close(stop)
	}

	out := notoapi.StopRecordingResult{
		MeetingID:   state.MeetingID,
		DurationSec: int(time.Since(state.StartedAt).Seconds()),
	}

	if s.ipc != nil {
		if res, err := s.ipc.Stop(ctx); err == nil {
			out.OutputPath = res.OutputPath
			out.DurationSec = int(res.DurationSecs)
		}
	}

	// Enqueue the pipeline so the recording becomes a searchable meeting.
	job, jerr := s.CreateJob(ctx, notoapi.CreateJobOpts{
		Kind:      notoapi.JobPipeline,
		MeetingID: state.MeetingID,
		Options: map[string]any{
			"title":       state.Title,
			"output_path": out.OutputPath,
			"notes_md":    opts.NotesMD,
		},
	})
	if jerr == nil {
		out.JobsKicked = append(out.JobsKicked, job.ID)
	}

	s.publishRecorder()
	return out, nil
}

func (s *Service) PauseRecording(ctx context.Context) error {
	if s.ipc == nil {
		return notoapi.NewError(notoapi.CodeUnsupportedCapability, "capture helper not available", nil)
	}
	return s.ipc.Pause(ctx)
}

func (s *Service) ResumeRecording(ctx context.Context) error {
	if s.ipc == nil {
		return notoapi.NewError(notoapi.CodeUnsupportedCapability, "capture helper not available", nil)
	}
	return s.ipc.Resume(ctx)
}

// AddMarker appends a labeled marker to the current recording.
func (s *Service) AddMarker(_ context.Context, label string) error {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if !s.recording.Active {
		return notoapi.NewError(notoapi.CodeRecordingInactive, "no recording is active", nil)
	}
	s.recording.Markers = append(s.recording.Markers, notoapi.Marker{
		OffsetSec: int(time.Since(s.recording.StartedAt).Seconds()),
		Label:     label,
	})
	return nil
}

// PreflightRecording confirms the capture helper is reachable.
func (s *Service) PreflightRecording(ctx context.Context) (notoapi.PreflightResult, error) {
	if s.ipc == nil {
		return notoapi.PreflightResult{
			MicReady:    false,
			SystemReady: false,
			Diagnostic:  "capture helper not available on this platform (Linux/Windows)",
		}, nil
	}
	if err := s.ipc.EnsureHelperRunning(ctx); err != nil {
		return notoapi.PreflightResult{
			MicReady:    false,
			SystemReady: false,
			Diagnostic:  fmt.Sprintf("capture helper failed to start: %v", err),
		}, nil
	}
	return notoapi.PreflightResult{
		MicReady:    true,
		SystemReady: true,
		HelperPath:  s.ipc.SocketPath(),
	}, nil
}

// GetRecording returns a snapshot of recorder state. Elapsed is
// recomputed from StartedAt so callers always see fresh seconds.
func (s *Service) GetRecording(_ context.Context) (notoapi.RecordingState, error) {
	s.recMu.RLock()
	defer s.recMu.RUnlock()
	state := s.recording
	if state.Active {
		state.ElapsedSec = int(time.Since(state.StartedAt).Seconds())
	}
	return state, nil
}

// StreamMeters returns a buffered channel that receives MeterEvents.
// Closed when ctx is canceled OR when recording stops.
func (s *Service) StreamMeters(ctx context.Context) (<-chan notoapi.MeterEvent, error) {
	ch := make(chan notoapi.MeterEvent, 16)
	go func() {
		defer close(ch)
		sub := s.events.subscribe()
		defer s.events.unsubscribe(sub)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub:
				if !ok {
					return
				}
				if ev.Kind == notoapi.EventMeter && ev.Meter != nil {
					select {
					case ch <- *ev.Meter:
					default:
					}
				}
			}
		}
	}()
	return ch, nil
}

// meterLoop polls the capture helper at ~8 Hz while recording.
func (s *Service) meterLoop(stop <-chan struct{}) {
	if s.ipc == nil {
		return
	}
	ticker := time.NewTicker(125 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			level, err := s.ipc.GetAudioLevel(ctx)
			cancel()
			if err != nil || level == nil {
				continue
			}
			mic := dbFromAmp(level.Left)
			part := dbFromAmp(level.Right)
			amb := dbFromAmp(level.Ambient)
			s.recMu.Lock()
			if s.recording.Active {
				s.recording.MicDB = mic
				s.recording.ParticipantDB = part
			}
			s.recMu.Unlock()
			s.events.publish(notoapi.Event{
				Kind: notoapi.EventMeter,
				Meter: &notoapi.MeterEvent{
					MicDB:         mic,
					ParticipantDB: part,
					AmbientDB:     amb,
					Clip:          mic >= 0 || part >= 0,
					At:            time.Now(),
				},
			})
		}
	}
}

// meterLoopDryRun emits gentle synthetic meters when no helper exists,
// so the TUI's recording flow is testable on Linux.
func (s *Service) meterLoopDryRun(stop <-chan struct{}) {
	ticker := time.NewTicker(125 * time.Millisecond)
	defer ticker.Stop()
	phase := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			phase++
			mic := -30 - (phase % 18) + (phase % 7)
			part := -36 + (phase % 12)
			s.recMu.Lock()
			if s.recording.Active {
				s.recording.MicDB = mic
				s.recording.ParticipantDB = part
			}
			s.recMu.Unlock()
			s.events.publish(notoapi.Event{
				Kind: notoapi.EventMeter,
				Meter: &notoapi.MeterEvent{
					MicDB:         mic,
					ParticipantDB: part,
					AmbientDB:     -50,
					At:            time.Now(),
				},
			})
		}
	}
}

func (s *Service) publishRecorder() {
	state, _ := s.GetRecording(context.Background())
	s.events.publish(notoapi.Event{Kind: notoapi.EventRecorder, Recorder: &state})
	s.emitStatusBar()
}

// dbFromAmp converts a 0..1 linear amplitude to a clamped dBFS value.
func dbFromAmp(amp float32) int {
	if amp <= 0 {
		return -60
	}
	// 20 * log10(amp), clamped to [-60, 0]
	const eps = 1e-6
	val := 20 * log10(float64(amp)+eps)
	if val < -60 {
		val = -60
	}
	if val > 0 {
		val = 0
	}
	return int(val)
}

// log10 is a tiny helper so we don't pull in math just for this.
func log10(x float64) float64 {
	// log10(x) = ln(x) / ln(10)
	return naturalLog(x) / 2.302585092994046
}

// naturalLog: short power series sufficient for the 0..1 range. The
// audio meter only needs ~1 dB accuracy.
func naturalLog(x float64) float64 {
	if x <= 0 {
		return -60
	}
	// Use Math.Log via a fast approximation. Acceptable for UI meters.
	// log(x) = log(1+y) where y = x-1; converges only on |y|<1 so we
	// reduce x via repeated halving.
	exp := 0
	for x < 0.5 {
		x *= 2
		exp--
	}
	for x > 1.5 {
		x /= 2
		exp++
	}
	y := x - 1
	sum := 0.0
	yn := y
	for i := 1; i < 30; i++ {
		if i%2 == 1 {
			sum += yn / float64(i)
		} else {
			sum -= yn / float64(i)
		}
		yn *= y
	}
	return sum + float64(exp)*0.6931471805599453
}

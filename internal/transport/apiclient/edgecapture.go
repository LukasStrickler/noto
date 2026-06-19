package apiclient

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// LocalCapturer is the minimal local audio-capture surface the edge recorder
// drives. The production implementation wraps the macOS Swift helper
// (appsocket.IPCClient); tests inject a fake. Keeping it an interface keeps this
// package free of the capture transport and makes the decorator testable on any
// OS.
type LocalCapturer interface {
	Start(ctx context.Context, sources []string) error
	// Stop ends capture and returns the finished local audio file.
	Stop(ctx context.Context) (audioPath string, durationSec int, err error)
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	// Level reports current input levels (dB-ish) for the meters.
	Level(ctx context.Context) (micDB, participantDB int, err error)
	Close() error
}

// NewEdgeCapture wraps a notoapi.Client so recording happens on THIS machine
// while everything else (storage, search, pipeline, agent API) stays on the
// connected backend. It is the missing edge of the three-plane split: a TUI on a
// Mac talking to a headless Linux data plane can still record — capture runs
// locally, and on stop the finished audio is uploaded into the backend's
// pipeline via ImportAudio.
//
// Only the recording methods are overridden; every other call passes straight
// through to inner. The factory (host.Connect) wraps only when the backend
// reports it cannot capture (System.CaptureAvailable=false) AND a local helper
// exists, so the common "backend captures" path is untouched.
func NewEdgeCapture(inner notoapi.Client, cap LocalCapturer, logf func(string, ...any)) notoapi.Client {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &edgeCaptureClient{Client: inner, cap: cap, logf: logf}
}

type edgeCaptureClient struct {
	notoapi.Client // embedded: passthrough for every non-recording method
	cap            LocalCapturer
	logf           func(string, ...any)

	mu    sync.Mutex
	state notoapi.RecordingState
}

func (e *edgeCaptureClient) StartRecording(ctx context.Context, opts notoapi.StartRecordingOpts) (notoapi.StartRecordingResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state.Active {
		return notoapi.StartRecordingResult{}, notoapi.NewError(notoapi.CodeRecordingActive, "a recording is already active", map[string]any{"meeting_id": e.state.MeetingID})
	}
	sources := opts.Sources
	if len(sources) == 0 {
		sources = []string{"microphone", "system_audio"}
	}
	if err := e.cap.Start(ctx, sources); err != nil {
		return notoapi.StartRecordingResult{}, notoapi.NewError(notoapi.CodeUnsupportedCapability, "edge capture failed to start: "+err.Error(), nil)
	}
	// A placeholder meeting id labels the in-progress recording; the real
	// meeting is minted on the backend at stop time (ImportAudio), since the
	// data plane owns identity.
	mid := uuid.NewString()
	e.state = notoapi.RecordingState{
		Active:    true,
		MeetingID: mid,
		Title:     opts.Title,
		StartedAt: time.Now(),
		Sources:   sources,
		Retention: opts.Retention,
		MicDB:     -42,
	}
	return notoapi.StartRecordingResult{MeetingID: mid, Title: opts.Title, Sources: sources}, nil
}

func (e *edgeCaptureClient) StopRecording(ctx context.Context, _ notoapi.StopRecordingOpts) (notoapi.StopRecordingResult, error) {
	e.mu.Lock()
	if !e.state.Active {
		e.mu.Unlock()
		return notoapi.StopRecordingResult{}, notoapi.NewError(notoapi.CodeRecordingInactive, "no recording is active", nil)
	}
	st := e.state
	e.state = notoapi.RecordingState{}
	e.mu.Unlock()

	out := notoapi.StopRecordingResult{MeetingID: st.MeetingID}
	path, dur, err := e.cap.Stop(ctx)
	out.DurationSec = dur
	if err != nil {
		e.logf("edge capture: stop failed: %v", err)
		return out, nil
	}
	out.OutputPath = path
	if path == "" {
		return out, nil
	}
	// Upload the finished audio into the backend's pipeline. For a remote
	// client ImportAudio streams the bytes; the backend creates the meeting and
	// kicks the pipeline, becoming the source of truth.
	res, ierr := e.Client.ImportAudio(ctx, notoapi.ImportAudioOpts{Path: path, Title: st.Title})
	if ierr != nil {
		e.logf("edge capture: upload to backend failed: %v", ierr)
		return out, ierr
	}
	out.MeetingID = res.Meeting.ID
	if res.Job.ID != "" {
		out.JobsKicked = []string{res.Job.ID}
	}
	return out, nil
}

func (e *edgeCaptureClient) PauseRecording(ctx context.Context) error  { return e.cap.Pause(ctx) }
func (e *edgeCaptureClient) ResumeRecording(ctx context.Context) error { return e.cap.Resume(ctx) }

func (e *edgeCaptureClient) AddMarker(_ context.Context, label string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.state.Active {
		return notoapi.NewError(notoapi.CodeRecordingInactive, "no recording is active", nil)
	}
	e.state.Markers = append(e.state.Markers, notoapi.Marker{
		OffsetSec: int(time.Since(e.state.StartedAt).Seconds()),
		Label:     label,
	})
	return nil
}

func (e *edgeCaptureClient) GetRecording(_ context.Context) (notoapi.RecordingState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.state
	if st.Active {
		st.ElapsedSec = int(time.Since(st.StartedAt).Seconds())
	}
	return st, nil
}

func (e *edgeCaptureClient) PreflightRecording(_ context.Context) (notoapi.PreflightResult, error) {
	return notoapi.PreflightResult{MicReady: true, SystemReady: true, Diagnostic: "edge capture (local device)"}, nil
}

func (e *edgeCaptureClient) StreamMeters(ctx context.Context) (<-chan notoapi.MeterEvent, error) {
	out := make(chan notoapi.MeterEvent, 16)
	go func() {
		defer close(out)
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.mu.Lock()
				active := e.state.Active
				e.mu.Unlock()
				if !active {
					return
				}
				mic, part, err := e.cap.Level(ctx)
				if err != nil {
					continue
				}
				select {
				case out <- notoapi.MeterEvent{MicDB: mic, ParticipantDB: part, At: time.Now()}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func (e *edgeCaptureClient) Close() error {
	_ = e.cap.Close()
	return e.Client.Close()
}

// IsRemote delegates to the wrapped client — an edge-capture client only ever
// wraps a remote backend.
func (e *edgeCaptureClient) IsRemote() bool {
	if r, ok := e.Client.(interface{ IsRemote() bool }); ok {
		return r.IsRemote()
	}
	return true
}

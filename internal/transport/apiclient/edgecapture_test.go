package apiclient

import (
	"context"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// fakeInner is a notoapi.Client where only the methods the edge decorator
// delegates to (ImportAudio, Close) are real; everything else is never called in
// these tests (the embedded nil interface would panic if it were).
type fakeInner struct {
	notoapi.Client
	imported notoapi.ImportAudioOpts
	result   notoapi.ImportAudioResult
	err      error
}

func (f *fakeInner) ImportAudio(_ context.Context, opts notoapi.ImportAudioOpts) (notoapi.ImportAudioResult, error) {
	f.imported = opts
	return f.result, f.err
}
func (f *fakeInner) Close() error { return nil }

type fakeCapturer struct {
	started, paused, resumed, closed bool
	sources                          []string
	stopPath                         string
	stopDur                          int
}

func (f *fakeCapturer) Start(_ context.Context, sources []string) error {
	f.started, f.sources = true, sources
	return nil
}
func (f *fakeCapturer) Stop(_ context.Context) (string, int, error) {
	f.started = false
	return f.stopPath, f.stopDur, nil
}
func (f *fakeCapturer) Pause(_ context.Context) error             { f.paused = true; return nil }
func (f *fakeCapturer) Resume(_ context.Context) error            { f.resumed = true; return nil }
func (f *fakeCapturer) Level(_ context.Context) (int, int, error) { return -20, -30, nil }
func (f *fakeCapturer) Close() error                              { f.closed = true; return nil }

func TestEdgeCaptureRecordsLocallyAndUploadsOnStop(t *testing.T) {
	ctx := context.Background()
	inner := &fakeInner{result: notoapi.ImportAudioResult{
		Meeting: notoapi.Meeting{ID: "m-real"},
		Job:     notoapi.Job{ID: "job-1"},
	}}
	cap := &fakeCapturer{stopPath: "/tmp/rec.m4a", stopDur: 42}
	c := NewEdgeCapture(inner, cap, nil)

	start, err := c.StartRecording(ctx, notoapi.StartRecordingOpts{Title: "Standup"})
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	if !cap.started {
		t.Error("local capturer was not started")
	}
	if start.MeetingID == "" {
		t.Error("start should mint a placeholder meeting id")
	}
	if len(cap.sources) != 2 {
		t.Errorf("sources should default to mic+system, got %v", cap.sources)
	}

	if st, _ := c.GetRecording(ctx); !st.Active {
		t.Error("GetRecording should report active during edge capture")
	}

	stop, err := c.StopRecording(ctx, notoapi.StopRecordingOpts{})
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if inner.imported.Path != "/tmp/rec.m4a" || inner.imported.Title != "Standup" {
		t.Errorf("finished audio not uploaded to backend: %+v", inner.imported)
	}
	if stop.MeetingID != "m-real" {
		t.Errorf("stop should return the backend's real meeting id, got %q", stop.MeetingID)
	}
	if len(stop.JobsKicked) != 1 || stop.JobsKicked[0] != "job-1" {
		t.Errorf("stop should report the kicked pipeline job, got %v", stop.JobsKicked)
	}
	if st, _ := c.GetRecording(ctx); st.Active {
		t.Error("recording should be inactive after stop")
	}
}

func TestEdgeCaptureRejectsDoubleStart(t *testing.T) {
	ctx := context.Background()
	c := NewEdgeCapture(&fakeInner{}, &fakeCapturer{}, nil)
	if _, err := c.StartRecording(ctx, notoapi.StartRecordingOpts{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartRecording(ctx, notoapi.StartRecordingOpts{}); err == nil {
		t.Error("second StartRecording should be rejected")
	}
}

func TestEdgeCaptureStopWithoutStart(t *testing.T) {
	if _, err := NewEdgeCapture(&fakeInner{}, &fakeCapturer{}, nil).StopRecording(context.Background(), notoapi.StopRecordingOpts{}); err == nil {
		t.Error("StopRecording with no active recording should error")
	}
}

func TestEdgeCapturePauseResumeMarkerAndClose(t *testing.T) {
	ctx := context.Background()
	cap := &fakeCapturer{stopPath: "/tmp/x.m4a"}
	c := NewEdgeCapture(&fakeInner{}, cap, nil)
	if _, err := c.StartRecording(ctx, notoapi.StartRecordingOpts{Title: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := c.PauseRecording(ctx); err != nil || !cap.paused {
		t.Errorf("pause not delegated: err=%v paused=%v", err, cap.paused)
	}
	if err := c.ResumeRecording(ctx); err != nil || !cap.resumed {
		t.Errorf("resume not delegated: err=%v resumed=%v", err, cap.resumed)
	}
	if err := c.AddMarker(ctx, "decision"); err != nil {
		t.Errorf("AddMarker: %v", err)
	}
	if st, _ := c.GetRecording(ctx); len(st.Markers) != 1 || st.Markers[0].Label != "decision" {
		t.Errorf("marker not recorded: %+v", st.Markers)
	}
	if err := c.Close(); err != nil || !cap.closed {
		t.Errorf("Close should close the capturer: err=%v closed=%v", err, cap.closed)
	}
}

func TestEdgeCaptureStreamMetersStopsWhenInactive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cap := &fakeCapturer{stopPath: "/tmp/x.m4a"}
	c := NewEdgeCapture(&fakeInner{}, cap, nil)
	if _, err := c.StartRecording(ctx, notoapi.StartRecordingOpts{}); err != nil {
		t.Fatal(err)
	}
	ch, err := c.StreamMeters(ctx)
	if err != nil {
		t.Fatalf("StreamMeters: %v", err)
	}
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("meter stream closed before any event")
		}
		if ev.MicDB != -20 {
			t.Errorf("meter event mic db = %d, want -20", ev.MicDB)
		}
	case <-time.After(time.Second):
		t.Fatal("no meter event within 1s")
	}
	// Stopping ends the meter loop; the channel should close.
	if _, err := c.StopRecording(ctx, notoapi.StopRecordingOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // closed as expected
			}
		case <-deadline:
			t.Fatal("meter stream did not close after stop")
		}
	}
}

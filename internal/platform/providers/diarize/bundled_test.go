package diarize

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// fakeSTT is a minimal STTProvider returning a canned transcript (or error).
type fakeSTT struct {
	id  string
	tr  *artifacts.Transcript
	err error

	gotOpts stt.TranscribeOptions
}

func (f *fakeSTT) ProviderID() string               { return f.id }
func (f *fakeSTT) FeatureMap() stt.ProviderFeatures { return stt.ProviderFeatures{ProviderID: f.id} }
func (f *fakeSTT) Transcribe(_ context.Context, _ []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	f.gotOpts = opts
	return f.tr, f.err
}

func TestSegmentsToTurns(t *testing.T) {
	segs := []artifacts.Segment{
		{SpeakerID: "B", StartSeconds: 6, EndSeconds: 10},
		{SpeakerID: "A", StartSeconds: 0, EndSeconds: 3}, // out of order
		{SpeakerID: "A", StartSeconds: 3, EndSeconds: 5}, // touches prev A → merge
		{SpeakerID: "", StartSeconds: 5, EndSeconds: 6},  // no speaker → drop
		{SpeakerID: "C", StartSeconds: 9, EndSeconds: 9}, // zero length → drop
	}
	got := SegmentsToTurns(segs)
	want := []Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "B", StartSeconds: 6, EndSeconds: 10},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SegmentsToTurns = %+v, want %+v", got, want)
	}
}

func TestSegmentsToTurnsNoMergeAcrossSpeaker(t *testing.T) {
	// A then B then A back-to-back must stay three turns (no cross-speaker merge).
	segs := []artifacts.Segment{
		{SpeakerID: "A", StartSeconds: 0, EndSeconds: 2},
		{SpeakerID: "B", StartSeconds: 2, EndSeconds: 4},
		{SpeakerID: "A", StartSeconds: 4, EndSeconds: 6},
	}
	got := SegmentsToTurns(segs)
	if len(got) != 3 {
		t.Fatalf("got %d turns, want 3: %+v", len(got), got)
	}
}

func TestSegmentsToTurnsEmpty(t *testing.T) {
	if got := SegmentsToTurns(nil); got != nil {
		t.Errorf("SegmentsToTurns(nil) = %+v, want nil", got)
	}
}

func TestBundledDiarizer(t *testing.T) {
	f := &fakeSTT{
		id: "assemblyai",
		tr: &artifacts.Transcript{
			Segments: []artifacts.Segment{
				{SpeakerID: "A", StartSeconds: 0, EndSeconds: 5},
				{SpeakerID: "B", StartSeconds: 5, EndSeconds: 9},
			},
		},
	}
	d := NewBundledDiarizer(f)

	if d.ProviderID() != "assemblyai+bundled-diar" {
		t.Errorf("ProviderID = %q", d.ProviderID())
	}

	turns, err := d.Diarize(context.Background(), []byte("audio"), DiarizeOptions{NumSpeakers: 2, MeetingID: "m1"})
	if err != nil {
		t.Fatalf("Diarize: %v", err)
	}
	want := []Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "B", StartSeconds: 5, EndSeconds: 9},
	}
	if !reflect.DeepEqual(turns, want) {
		t.Errorf("turns = %+v, want %+v", turns, want)
	}
	// options must be forwarded to the wrapped STT call
	if f.gotOpts.NumSpeakers != 2 || f.gotOpts.MeetingID != "m1" {
		t.Errorf("forwarded opts = %+v", f.gotOpts)
	}
}

func TestBundledDiarizerPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	d := NewBundledDiarizer(&fakeSTT{id: "x", err: sentinel})
	if _, err := d.Diarize(context.Background(), nil, DiarizeOptions{}); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

// Compile-time assurance the adapter satisfies the seam.
var _ Diarizer = (*BundledDiarizer)(nil)

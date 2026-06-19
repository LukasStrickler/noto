package diarize

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// fakeSegEngine is an in-process SegmentEngine — the proof that any runtime plugs
// in behind LocalDiarizer.
type fakeSegEngine struct {
	name  string
	turns []EngineTurn
	err   error
}

func (f fakeSegEngine) Name() string { return f.name }
func (f fakeSegEngine) Segment(context.Context, []byte, DiarizeOptions) ([]EngineTurn, error) {
	return f.turns, f.err
}

type fakeBatchSegEngine struct {
	fakeSegEngine
	calls int
}

func (f *fakeBatchSegEngine) SegmentBatch(_ context.Context, audios [][]byte, _ []DiarizeOptions) ([][]EngineTurn, error) {
	f.calls++
	out := make([][]EngineTurn, len(audios))
	for i := range audios {
		out[i] = []EngineTurn{{Speaker: string(audios[i]), EndSeconds: float64(i + 1)}}
	}
	return out, nil
}

func TestLocalDiarizerNormalizes(t *testing.T) {
	eng := fakeSegEngine{name: "oracle", turns: []EngineTurn{
		{Speaker: "B", StartSeconds: 6, EndSeconds: 10},
		{Speaker: "A", StartSeconds: 0, EndSeconds: 3}, // out of order
		{Speaker: "A", StartSeconds: 3, EndSeconds: 5}, // touches → merge
		{Speaker: "", StartSeconds: 5, EndSeconds: 6},  // drop (no speaker)
	}}
	d := NewLocalDiarizer(eng)
	if d.ProviderID() != "local-diar:oracle" {
		t.Errorf("ProviderID = %q", d.ProviderID())
	}
	turns, err := d.Diarize(context.Background(), []byte("audio"), DiarizeOptions{})
	if err != nil {
		t.Fatalf("Diarize: %v", err)
	}
	want := []Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "B", StartSeconds: 6, EndSeconds: 10},
	}
	if !reflect.DeepEqual(turns, want) {
		t.Errorf("turns = %+v, want %+v", turns, want)
	}
}

func TestLocalDiarizerPropagatesEngineError(t *testing.T) {
	sentinel := errors.New("segment failed")
	d := NewLocalDiarizer(fakeSegEngine{name: "x", err: sentinel})
	if _, err := d.Diarize(context.Background(), nil, DiarizeOptions{}); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

func TestLocalDiarizerDiarizeBatch(t *testing.T) {
	audios := [][]byte{[]byte("A"), []byte("B")}
	opts := []DiarizeOptions{{MeetingID: "m0"}, {MeetingID: "m1"}}

	be := &fakeBatchSegEngine{fakeSegEngine: fakeSegEngine{name: "batch"}}
	d := NewLocalDiarizer(be)
	if !d.BatchCapable() {
		t.Fatal("BatchCapable = false for a BatchSegmentEngine")
	}
	turns, err := d.DiarizeBatch(context.Background(), audios, opts)
	if err != nil {
		t.Fatalf("DiarizeBatch: %v", err)
	}
	if be.calls != 1 {
		t.Errorf("engine batch calls = %d, want 1", be.calls)
	}
	if len(turns) != 2 || turns[0][0].Speaker != "A" || turns[1][0].Speaker != "B" {
		t.Fatalf("batch results misordered: %+v", turns)
	}

	lp := NewLocalDiarizer(fakeSegEngine{name: "plain", turns: []EngineTurn{{Speaker: "X", EndSeconds: 1}}})
	if lp.BatchCapable() {
		t.Error("BatchCapable = true for a plain engine")
	}
	turns2, err := lp.DiarizeBatch(context.Background(), audios, opts)
	if err != nil {
		t.Fatalf("fallback DiarizeBatch: %v", err)
	}
	if len(turns2) != 2 || turns2[1][0].Speaker != "X" {
		t.Fatalf("fallback results wrong: %+v", turns2)
	}

	if _, err := lp.DiarizeBatch(context.Background(), audios, opts[:1]); err == nil {
		t.Error("expected error for mismatched opts length")
	}
}

func TestSegmentEngineRegistry(t *testing.T) {
	RegisterSegmentEngine("unit-fake", func(cfg EngineConfig) (SegmentEngine, error) {
		return fakeSegEngine{name: "unit-fake", turns: []EngineTurn{{Speaker: "A", EndSeconds: 1}}}, nil
	})
	d, err := NewLocalDiarizerByName("unit-fake", EngineConfig{ModelDir: "/models"})
	if err != nil {
		t.Fatalf("NewLocalDiarizerByName: %v", err)
	}
	if d.ProviderID() != "local-diar:unit-fake" {
		t.Errorf("ProviderID = %q", d.ProviderID())
	}
	if _, err := NewLocalDiarizerByName("nope", EngineConfig{}); err == nil {
		t.Error("expected error for unknown engine")
	}
}

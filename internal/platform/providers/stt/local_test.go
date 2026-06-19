package stt

import (
	"context"
	"errors"
	"testing"
)

// fakeEngine is an in-process STTEngine — the proof that any runtime plugs in
// behind LocalSTT.
type fakeEngine struct {
	name  string
	words []EngineWord
	err   error
}

func (f fakeEngine) Name() string { return f.name }
func (f fakeEngine) Recognize(context.Context, []byte, TranscribeOptions) ([]EngineWord, error) {
	return f.words, f.err
}

func TestLocalSTTTranscribe(t *testing.T) {
	eng := fakeEngine{name: "oracle", words: []EngineWord{
		{Text: "hello", StartSeconds: 0.0, EndSeconds: 0.4, Confidence: 0.9},
		{Text: "there", StartSeconds: 0.4, EndSeconds: 0.8},
		// 1.5s gap (> defaultSegmentGap) → new segment
		{Text: "world", StartSeconds: 2.3, EndSeconds: 2.7},
	}}
	p := NewLocalSTT(eng)

	if p.ProviderID() != "local-stt:oracle" {
		t.Errorf("ProviderID = %q", p.ProviderID())
	}
	fm := p.FeatureMap()
	if !fm.IsLocal || !fm.Has(FeatureWordTimestamps) || fm.Has(FeatureSpeakerDiarize) {
		t.Errorf("FeatureMap = %+v (want local, word ts, no diarize)", fm)
	}

	tr, err := p.Transcribe(context.Background(), []byte("audio"), TranscribeOptions{MeetingID: "m1", Language: "en"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if len(tr.Words) != 3 {
		t.Fatalf("words = %d, want 3", len(tr.Words))
	}
	if len(tr.Segments) != 2 {
		t.Fatalf("segments = %d, want 2 (split on the 1.5s pause)", len(tr.Segments))
	}
	if tr.Segments[0].Text != "hello there" || tr.Segments[1].Text != "world" {
		t.Errorf("segment texts = %q / %q", tr.Segments[0].Text, tr.Segments[1].Text)
	}
	if tr.DurationSeconds != 2.7 {
		t.Errorf("duration = %v, want 2.7", tr.DurationSeconds)
	}
	if tr.MeetingID != "m1" || tr.Language != "en" {
		t.Errorf("lineage not threaded: %+v", tr.Provider)
	}
	// word→segment linkage
	if tr.Words[0].SegmentID != "s0" || tr.Words[2].SegmentID != "s1" {
		t.Errorf("word segment ids = %q / %q", tr.Words[0].SegmentID, tr.Words[2].SegmentID)
	}
	if tr.Words[0].Confidence == nil || *tr.Words[0].Confidence != 0.9 {
		t.Errorf("confidence not carried: %v", tr.Words[0].Confidence)
	}
	if tr.Words[1].Confidence != nil {
		t.Errorf("zero confidence should be nil, got %v", *tr.Words[1].Confidence)
	}
}

func TestLocalSTTPropagatesEngineError(t *testing.T) {
	sentinel := errors.New("decode failed")
	p := NewLocalSTT(fakeEngine{name: "x", err: sentinel})
	if _, err := p.Transcribe(context.Background(), nil, TranscribeOptions{}); !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
}

// fakeBatchEngine is a fakeEngine that also satisfies BatchSTTEngine, returning
// one canned word per audio so the per-audio mapping is checkable.
type fakeBatchEngine struct {
	fakeEngine
	calls int
}

func (f *fakeBatchEngine) RecognizeBatch(_ context.Context, audios [][]byte, _ TranscribeOptions) ([][]EngineWord, error) {
	f.calls++
	out := make([][]EngineWord, len(audios))
	for i := range audios {
		out[i] = []EngineWord{{Text: string(audios[i]), EndSeconds: 1}}
	}
	return out, nil
}

func TestLocalSTTTranscribeBatch(t *testing.T) {
	audios := [][]byte{[]byte("a"), []byte("b")}
	opts := []TranscribeOptions{{MeetingID: "m0"}, {MeetingID: "m1"}}

	// Batch-capable engine: one engine call, results mapped per audio.
	be := &fakeBatchEngine{fakeEngine: fakeEngine{name: "batch"}}
	p := NewLocalSTT(be)
	if !p.BatchCapable() {
		t.Fatal("BatchCapable = false for a BatchSTTEngine")
	}
	trs, err := p.TranscribeBatch(context.Background(), audios, opts)
	if err != nil {
		t.Fatalf("TranscribeBatch: %v", err)
	}
	if be.calls != 1 {
		t.Errorf("engine batch calls = %d, want 1", be.calls)
	}
	if len(trs) != 2 || trs[0].Words[0].Text != "a" || trs[1].Words[0].Text != "b" {
		t.Fatalf("batch results misordered: %+v", trs)
	}
	if trs[0].MeetingID != "m0" || trs[1].MeetingID != "m1" {
		t.Errorf("per-audio opts not applied: %q / %q", trs[0].MeetingID, trs[1].MeetingID)
	}

	// Plain engine: falls back to looping Recognize, same shape.
	lp := NewLocalSTT(fakeEngine{name: "plain", words: []EngineWord{{Text: "w", EndSeconds: 1}}})
	if lp.BatchCapable() {
		t.Error("BatchCapable = true for a plain engine")
	}
	trs2, err := lp.TranscribeBatch(context.Background(), audios, opts)
	if err != nil {
		t.Fatalf("fallback TranscribeBatch: %v", err)
	}
	if len(trs2) != 2 || trs2[1].Words[0].Text != "w" {
		t.Fatalf("fallback results wrong: %+v", trs2)
	}

	// Guard: opts must be parallel to audios.
	if _, err := lp.TranscribeBatch(context.Background(), audios, opts[:1]); err == nil {
		t.Error("expected error for mismatched opts length")
	}
}

func TestSTTEngineRegistry(t *testing.T) {
	RegisterSTTEngine("unit-fake", func(cfg EngineConfig) (STTEngine, error) {
		return fakeEngine{name: "unit-fake", words: []EngineWord{{Text: "ok", EndSeconds: 1}}}, nil
	})
	p, err := NewLocalSTTByName("unit-fake", EngineConfig{ModelDir: "/models"})
	if err != nil {
		t.Fatalf("NewLocalSTTByName: %v", err)
	}
	if p.ProviderID() != "local-stt:unit-fake" {
		t.Errorf("ProviderID = %q", p.ProviderID())
	}
	if _, err := NewLocalSTTByName("nope", EngineConfig{}); err == nil {
		t.Error("expected error for unknown engine")
	}
}

package stt

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

// LocalSTT is a runtime-agnostic local speech-to-text provider. It satisfies the
// STTProvider seam the pipeline already speaks, and delegates the actual
// recognition to a pluggable STTEngine — so the *runtime* (sherpa-onnx,
// onnxruntime_go, a subprocess, or a ground-truth oracle in tests) is a swappable
// detail, never baked into the provider or the benchmarks.
//
// LocalSTT owns the noto-shaped assembly: it turns the engine's flat word stream
// into an artifacts.Transcript (words with timestamps, grouped into coarse
// segments on pauses), and reports IsLocal + word-timestamp capability. It does
// NOT diarize — speaker labels come from a separate Diarizer and are joined by the
// merge step.
type LocalSTT struct {
	engine STTEngine
	// SegmentGapSeconds starts a new segment when the silence between words
	// exceeds it. Zero uses defaultSegmentGap.
	SegmentGapSeconds float64
}

// EngineWord is one recognized token as a runtime emits it — text plus timing,
// no speaker (local STT does not diarize).
type EngineWord struct {
	Text         string
	StartSeconds float64
	EndSeconds   float64
	Confidence   float64
}

// STTEngine is the runtime-agnostic recognition backend behind LocalSTT. An
// implementation receives the same audio bytes a caller would hand any
// STTProvider and returns recognized words. Implementations decode/feature-extract
// however their runtime requires; the oracle engine ignores the audio and replays
// a reference.
type STTEngine interface {
	// Name identifies the runtime (e.g. "sherpa-parakeet", "onnx-ctc", "oracle").
	Name() string
	// Recognize returns time-ordered recognized words for the audio.
	Recognize(ctx context.Context, audio []byte, opts TranscribeOptions) ([]EngineWord, error)
}

// BatchSTTEngine is an OPTIONAL STTEngine extension: recognize several
// independent audios in one warm pass, paying the runtime's fixed cost (model
// load, CUDA-context init) once for the whole batch instead of once per audio.
// Callers type-assert for it and fall back to per-audio Recognize — see
// LocalSTT.TranscribeBatch. The benchmark uses it to amortize the GPU across a
// whole corpus; a persistent inference server would satisfy it naturally.
type BatchSTTEngine interface {
	STTEngine
	// RecognizeBatch returns one word stream per input audio, in input order.
	RecognizeBatch(ctx context.Context, audios [][]byte, opts TranscribeOptions) ([][]EngineWord, error)
}

const defaultSegmentGap = 0.6

// NewLocalSTT wraps a recognition engine as an STTProvider.
func NewLocalSTT(engine STTEngine) *LocalSTT { return &LocalSTT{engine: engine} }

// ProviderID namespaces the engine under the local provider.
func (l *LocalSTT) ProviderID() string { return "local-stt:" + l.engine.Name() }

// FeatureMap advertises a local, word-timestamping recognizer with no diarization.
func (l *LocalSTT) FeatureMap() ProviderFeatures {
	return ProviderFeatures{
		ProviderID: l.ProviderID(),
		Features:   []Feature{FeatureTranscribe, FeatureWordTimestamps},
		SpeedTier:  "fast",
		IsLocal:    true,
	}
}

// Transcribe runs the engine and assembles an artifacts.Transcript: word-level
// output plus coarse pause-split segments (no speaker — that is the diarizer's
// and merge step's job).
func (l *LocalSTT) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	words, err := l.engine.Recognize(ctx, audio, opts)
	if err != nil {
		return nil, err
	}
	return l.assemble(words, opts), nil
}

// BatchCapable reports whether the underlying engine recognizes many audios in
// one warm pass (TranscribeBatch is always available; this says whether it
// actually amortizes the runtime's fixed cost or just loops).
func (l *LocalSTT) BatchCapable() bool {
	_, ok := l.engine.(BatchSTTEngine)
	return ok
}

// TranscribeBatch transcribes several independent audios, one transcript per
// audio (opts must be parallel to audios). A BatchCapable engine handles the
// whole batch in one warm pass; any other engine is looped — same results,
// no amortization.
func (l *LocalSTT) TranscribeBatch(ctx context.Context, audios [][]byte, opts []TranscribeOptions) ([]*artifacts.Transcript, error) {
	if len(opts) != len(audios) {
		return nil, fmt.Errorf("stt: TranscribeBatch needs one TranscribeOptions per audio (%d audios, %d opts)", len(audios), len(opts))
	}
	out := make([]*artifacts.Transcript, len(audios))
	be, ok := l.engine.(BatchSTTEngine)
	if !ok {
		for i, audio := range audios {
			tr, err := l.Transcribe(ctx, audio, opts[i])
			if err != nil {
				return nil, err
			}
			out[i] = tr
		}
		return out, nil
	}
	// Engine-level batching: per-audio options beyond identity/language cannot
	// vary within one warm pass, so the first audio's options steer the engine.
	wordLists, err := be.RecognizeBatch(ctx, audios, firstOpts(opts))
	if err != nil {
		return nil, err
	}
	if len(wordLists) != len(audios) {
		return nil, fmt.Errorf("stt: engine %s returned %d results for %d audios", l.engine.Name(), len(wordLists), len(audios))
	}
	for i, words := range wordLists {
		out[i] = l.assemble(words, opts[i])
	}
	return out, nil
}

func firstOpts(opts []TranscribeOptions) TranscribeOptions {
	if len(opts) > 0 {
		return opts[0]
	}
	return TranscribeOptions{}
}

// assemble turns the engine's flat word stream into the noto-shaped transcript:
// time-sorted words grouped into coarse pause-split segments.
func (l *LocalSTT) assemble(words []EngineWord, opts TranscribeOptions) *artifacts.Transcript {
	sort.SliceStable(words, func(i, j int) bool { return words[i].StartSeconds < words[j].StartSeconds })

	tr := &artifacts.Transcript{
		MeetingID: opts.MeetingID,
		Language:  opts.Language,
		Provider:  artifacts.TranscriptProvider{ID: l.ProviderID()},
	}
	gap := l.SegmentGapSeconds
	if gap <= 0 {
		gap = defaultSegmentGap
	}

	var (
		segIdx   = -1
		prevEnd  float64
		duration float64
	)
	for i, w := range words {
		wid := fmt.Sprintf("w%d", i)
		newSeg := segIdx < 0 || w.StartSeconds-prevEnd > gap
		if newSeg {
			segIdx++
			tr.Segments = append(tr.Segments, artifacts.Segment{
				ID:           fmt.Sprintf("s%d", segIdx),
				StartSeconds: w.StartSeconds,
				EndSeconds:   w.EndSeconds,
			})
		}
		seg := &tr.Segments[segIdx]
		if seg.Text == "" {
			seg.Text = w.Text
		} else {
			seg.Text += " " + w.Text
		}
		seg.EndSeconds = w.EndSeconds
		seg.WordIDs = append(seg.WordIDs, wid)

		tr.Words = append(tr.Words, artifacts.Word{
			ID:           wid,
			SegmentID:    seg.ID,
			StartSeconds: w.StartSeconds,
			EndSeconds:   w.EndSeconds,
			Text:         w.Text,
			Confidence:   confPtr(w.Confidence),
		})
		prevEnd = w.EndSeconds
		if w.EndSeconds > duration {
			duration = w.EndSeconds
		}
	}
	tr.DurationSeconds = duration
	return tr
}

func confPtr(c float64) *float64 {
	if c == 0 {
		return nil
	}
	return &c
}

// --- engine registry: config-driven runtime selection -----------------------

// EngineConfig is the runtime-agnostic configuration handed to an engine factory.
// Concrete engines read what they need (ModelDir, SampleRate, Options).
type EngineConfig struct {
	ModelDir   string
	SampleRate int
	Options    map[string]string
}

// STTEngineFactory builds an STTEngine from config.
type STTEngineFactory func(cfg EngineConfig) (STTEngine, error)

var (
	sttEngineMu sync.RWMutex
	sttEngines  = map[string]STTEngineFactory{}
)

// RegisterSTTEngine registers a runtime by name. Real runtimes register from
// build-tagged files so the default build pulls in no native dependency; tests
// and the benchmark oracle register in-process.
func RegisterSTTEngine(name string, f STTEngineFactory) {
	sttEngineMu.Lock()
	defer sttEngineMu.Unlock()
	sttEngines[name] = f
}

// NewLocalSTTByName constructs a LocalSTT around the named, registered engine —
// the production entry point for "pick the local runtime from config".
func NewLocalSTTByName(name string, cfg EngineConfig) (*LocalSTT, error) {
	sttEngineMu.RLock()
	f, ok := sttEngines[name]
	sttEngineMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("stt: no engine registered as %q (have %v)", name, registeredSTTEngines())
	}
	engine, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("stt: build engine %q: %w", name, err)
	}
	return NewLocalSTT(engine), nil
}

func registeredSTTEngines() []string {
	sttEngineMu.RLock()
	defer sttEngineMu.RUnlock()
	names := make([]string, 0, len(sttEngines))
	for n := range sttEngines {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var _ STTProvider = (*LocalSTT)(nil)

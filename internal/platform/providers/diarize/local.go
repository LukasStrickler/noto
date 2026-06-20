package diarize

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// LocalDiarizer is a runtime-agnostic local diarizer. It satisfies the Diarizer
// seam and delegates segmentation to a pluggable SegmentEngine, so the *runtime*
// (a pyannote-style sherpa-onnx segmenter, an onnxruntime_go segmentation+
// clustering pipeline, a subprocess, or a ground-truth oracle in tests) is a
// swappable detail — mirroring LocalSTT on the STT side.
//
// LocalDiarizer owns the noto-shaped normalization: it sorts the engine's turns
// and merges touching same-speaker turns into clean turns (the same projection
// the bundled diarizer applies to segments).
type LocalDiarizer struct {
	engine SegmentEngine
}

// EngineTurn is one speaker turn as a runtime emits it.
type EngineTurn struct {
	Speaker      string
	StartSeconds float64
	EndSeconds   float64
}

// SegmentEngine is the runtime-agnostic segmentation backend behind
// LocalDiarizer. It receives the same audio bytes any Diarizer would and returns
// speaker turns; implementations decode/segment however their runtime requires.
type SegmentEngine interface {
	// Name identifies the runtime (e.g. "sherpa-pyannote", "onnx-segclust", "oracle").
	Name() string
	// Segment returns speaker turns for the audio (need not be sorted/merged).
	Segment(ctx context.Context, audio []byte, opts DiarizeOptions) ([]EngineTurn, error)
}

// BatchSegmentEngine is an optional SegmentEngine extension: segment several
// independent audios in one warm pass, returning one turn list per input audio.
type BatchSegmentEngine interface {
	SegmentEngine
	SegmentBatch(ctx context.Context, audios [][]byte, opts []DiarizeOptions) ([][]EngineTurn, error)
}

// NewLocalDiarizer wraps a segmentation engine as a Diarizer.
func NewLocalDiarizer(engine SegmentEngine) *LocalDiarizer { return &LocalDiarizer{engine: engine} }

// ProviderID namespaces the engine under the local provider.
func (l *LocalDiarizer) ProviderID() string { return "local-diar:" + l.engine.Name() }

// Diarize runs the engine and normalizes its turns (sort + merge-adjacent),
// reusing the same projection as the bundled diarizer.
func (l *LocalDiarizer) Diarize(ctx context.Context, audio []byte, opts DiarizeOptions) ([]Turn, error) {
	raw, err := l.engine.Segment(ctx, audio, opts)
	if err != nil {
		return nil, err
	}
	return normalizeTurns(raw), nil
}

// BatchCapable reports whether the underlying engine can segment several
// meetings in one warm pass.
func (l *LocalDiarizer) BatchCapable() bool {
	_, ok := l.engine.(BatchSegmentEngine)
	return ok
}

// DiarizeBatch diarizes several independent audios. A BatchCapable engine
// handles the whole set in one warm pass; plain engines fall back to looping.
func (l *LocalDiarizer) DiarizeBatch(ctx context.Context, audios [][]byte, opts []DiarizeOptions) ([][]Turn, error) {
	if len(opts) != len(audios) {
		return nil, fmt.Errorf("diarize: DiarizeBatch needs one DiarizeOptions per audio (%d audios, %d opts)", len(audios), len(opts))
	}
	out := make([][]Turn, len(audios))
	be, ok := l.engine.(BatchSegmentEngine)
	if !ok {
		for i, audio := range audios {
			turns, err := l.Diarize(ctx, audio, opts[i])
			if err != nil {
				return nil, err
			}
			out[i] = turns
		}
		return out, nil
	}
	rawLists, err := be.SegmentBatch(ctx, audios, opts)
	if err != nil {
		return nil, err
	}
	if len(rawLists) != len(audios) {
		return nil, fmt.Errorf("diarize: engine %s returned %d results for %d audios", l.engine.Name(), len(rawLists), len(audios))
	}
	for i, raw := range rawLists {
		out[i] = normalizeTurns(raw)
	}
	return out, nil
}

func normalizeTurns(raw []EngineTurn) []Turn {
	turns := make([]Turn, 0, len(raw))
	for _, t := range raw {
		if t.Speaker == "" || t.EndSeconds <= t.StartSeconds {
			continue
		}
		turns = append(turns, Turn(t))
	}
	sort.SliceStable(turns, func(i, j int) bool {
		if turns[i].StartSeconds != turns[j].StartSeconds {
			return turns[i].StartSeconds < turns[j].StartSeconds
		}
		return turns[i].EndSeconds < turns[j].EndSeconds
	})
	return mergeAdjacent(turns)
}

// --- engine registry: config-driven runtime selection -----------------------

// EngineConfig is the runtime-agnostic configuration handed to an engine factory.
type EngineConfig struct {
	ModelDir   string
	SampleRate int
	Options    map[string]string
}

// SegmentEngineFactory builds a SegmentEngine from config.
type SegmentEngineFactory func(cfg EngineConfig) (SegmentEngine, error)

var (
	segEngineMu sync.RWMutex
	segEngines  = map[string]SegmentEngineFactory{}
)

// RegisterSegmentEngine registers a diarization runtime by name. Real runtimes
// register from build-tagged files; tests and the oracle register in-process.
func RegisterSegmentEngine(name string, f SegmentEngineFactory) {
	segEngineMu.Lock()
	defer segEngineMu.Unlock()
	segEngines[name] = f
}

// NewLocalDiarizerByName constructs a LocalDiarizer around the named, registered
// engine — the production entry point for "pick the local runtime from config".
func NewLocalDiarizerByName(name string, cfg EngineConfig) (*LocalDiarizer, error) {
	segEngineMu.RLock()
	f, ok := segEngines[name]
	segEngineMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("diarize: no engine registered as %q (have %v)", name, registeredSegmentEngines())
	}
	engine, err := f(cfg)
	if err != nil {
		return nil, fmt.Errorf("diarize: build engine %q: %w", name, err)
	}
	return NewLocalDiarizer(engine), nil
}

func registeredSegmentEngines() []string {
	segEngineMu.RLock()
	defer segEngineMu.RUnlock()
	names := make([]string, 0, len(segEngines))
	for n := range segEngines {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var _ Diarizer = (*LocalDiarizer)(nil)

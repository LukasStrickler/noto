// Package parakeet is noto's local speech-to-text provider, backed by
// Parakeet-TDT-0.6b-v3 running through the sherpa-onnx transducer runtime
// (CPU/CUDA on Linux, CoreML on Mac later). It implements stt.STTProvider, so
// it drops into the registry + router exactly where AssemblyAI sits today — but
// it runs on-device, emits native word timestamps, and does NOT diarize
// (diarization is a separate seam; see internal/platform/providers/diar).
//
// The decode loop (encoder→decoder→joint→greedy TDT) lives behind the `engine`
// seam: the default build ships a stub that reports the engine is not compiled
// in, so the binary builds and runs everywhere without the sherpa cgo
// dependency. Building with `-tags sherpa` (and the sherpa-onnx shared lib +
// model installed via `noto models download parakeet-tdt-0.6b-v3`) swaps in the
// real engine. This keeps the local-first pivot's default build green while the
// runtime integration lands.
package parakeet

import (
	"context"
	"fmt"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/platform/models"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

// ProviderID is the registry/routing id for the local STT provider.
const ProviderID = "parakeet-local"

// ModelID is the manifest id of the weights this provider needs.
const ModelID = "parakeet-tdt-0.6b-v3"

// engine is the swappable decode backend. nil means "not built into this
// binary" (the default, no-cgo build); the sherpa build provides a real one.
type engine interface {
	transcribe(ctx context.Context, pcm []float32, sampleRate int, opts stt.TranscribeOptions) (*artifacts.Transcript, error)
	Close() error
}

// Parakeet is the local STT provider. It owns (lazily) one decode engine and is
// safe for sequential use; callers serialize Transcribe (the service model pool
// does this).
type Parakeet struct {
	mgr  *models.Manager
	plan compute.Plan
	eng  engine
}

// New builds the provider over a model manager and the resolved accelerator
// plan. The engine is constructed via newEngine, which is a no-op stub unless
// the binary was built with the sherpa runtime.
func New(mgr *models.Manager, plan compute.Plan) *Parakeet {
	return &Parakeet{mgr: mgr, plan: plan, eng: newEngine(mgr, plan)}
}

func (p *Parakeet) ProviderID() string { return ProviderID }

func (p *Parakeet) FeatureMap() stt.ProviderFeatures {
	return stt.ProviderFeatures{
		ProviderID: ProviderID,
		// Native word + segment timestamps from the TDT architecture; runs on
		// both channels of a dual-channel capture. Deliberately NOT
		// FeatureSpeakerDiarize — diarization is the diar.Diarizer seam now.
		Features:  []stt.Feature{stt.FeatureTranscribe, stt.FeatureWordTimestamps, stt.FeatureMultiChannel},
		SpeedTier: "fast",
		IsLocal:   true,
	}
}

// Transcribe runs local STT. It fails with an actionable error when the model
// isn't installed or the engine isn't compiled in, so the pipeline surfaces a
// clear "run noto models download" note rather than a silent wrong transcript.
func (p *Parakeet) Transcribe(ctx context.Context, audio []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	if p.mgr != nil && !p.mgr.Available(ModelID, p.plan.Backend, p.plan.Tier) {
		return nil, fmt.Errorf("parakeet-local: model %q not installed (run: noto models download %s)", ModelID, ModelID)
	}
	if p.eng == nil {
		return nil, fmt.Errorf("parakeet-local: sherpa-onnx engine not built into this binary (rebuild with -tags sherpa)")
	}
	pcm, sampleRate, err := decodePCM(p.mgr, audio)
	if err != nil {
		return nil, fmt.Errorf("parakeet-local: decode audio: %w", err)
	}
	return p.eng.transcribe(ctx, pcm, sampleRate, opts)
}

// Close releases the engine (implements the service model pool's loadedModel).
func (p *Parakeet) Close() error {
	if p.eng != nil {
		return p.eng.Close()
	}
	return nil
}

// Ensure fetches the model if missing — the lazy "fetch on first transcribe"
// hook the pipeline can call.
func (p *Parakeet) Ensure(ctx context.Context, log models.Logf) error {
	if p.mgr == nil {
		return fmt.Errorf("parakeet-local: no model manager")
	}
	return p.mgr.Ensure(ctx, ModelID, p.plan.Backend, p.plan.Tier, log)
}

// compile-time check.
var _ stt.STTProvider = (*Parakeet)(nil)

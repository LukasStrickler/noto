package stt

import (
	"context"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

type Feature string

const (
	FeatureTranscribe          Feature = "transcribe"
	FeatureWordTimestamps      Feature = "word_timestamps"
	FeatureSpeakerDiarize      Feature = "speaker_diarize"
	FeatureContextBiasing      Feature = "context_biasing"
	FeatureMultiChannel        Feature = "multi_channel"
	FeatureOverlapDetection    Feature = "overlap_detection"
	FeatureAudioClassification Feature = "audio_classification"
)

type ProviderFeatures struct {
	ProviderID string
	Features   []Feature
	SpeedTier  string
	IsLocal    bool
}

func (pf ProviderFeatures) Has(f Feature) bool {
	for _, feat := range pf.Features {
		if feat == f {
			return true
		}
	}
	return false
}

// TranscribeOptions contains options for transcription.
type TranscribeOptions struct {
	// Language is the BCP-47 language code (e.g., "en", "de-DE").
	// Empty string triggers auto-detection.
	Language string
	// ContextBias allows providing domain-specific terms to improve accuracy.
	ContextBias []string
	// NumSpeakers hints the expected number of speakers for diarization.
	NumSpeakers int
	// Model is the transcription model to use (provider-specific).
	// AssemblyAI currently chooses its production speech model in the adapter.
	Model string
	// MeetingID is the noto meeting ID for artifact lineage.
	MeetingID string
	// SpeakerEmbeddings maps provider speaker labels (e.g., "A", "B") to
	// voice embedding vectors for cross-meeting speaker matching.
	// May be nil or empty if embeddings are not available; matching
	// will be skipped in that case without failing the job.
	SpeakerEmbeddings map[string][]float64
}

type STTProvider interface {
	ProviderID() string
	FeatureMap() ProviderFeatures
	Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error)
}

var RequiredFeatures = []Feature{
	FeatureTranscribe,
}

var SpeedTierOrder = []string{"fast", "medium", "accurate"}

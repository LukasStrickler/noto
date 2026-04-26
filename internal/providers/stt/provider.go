package stt

import (
	"context"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

// TranscribeOptions contains options for transcription.
type TranscribeOptions struct {
	// Language is the BCP-47 language code (e.g., "en", "de-DE").
	// Empty string triggers auto-detection.
	Language string
	// ContextBias allows providing domain-specific terms to improve accuracy.
	ContextBias []string
	// NumSpeakers hints the expected number of speakers for diarization.
	NumSpeakers int
	// MeetingID is the noto meeting ID for artifact lineage.
	MeetingID string
}

// STTProvider is the interface for speech-to-text providers.
// Implementations must be safe for concurrent use.
type STTProvider interface {
	// ProviderID returns the provider's canonical ID (e.g., "assemblyai", "whisper").
	ProviderID() string
	// Transcribe converts audio data to a normalized transcript artifact.
	// The audio []byte should be the raw audio file content.
	Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error)
}
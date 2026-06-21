// Package diarize is the standalone speaker-diarization seam: "who spoke when",
// independent of what they said.
//
// Diarization used to be bundled inside the STT provider (an inline-diarizing
// STT returns speaker-labelled segments). The local-first pipeline splits the two — a local
// STT decoder emits words with no speaker labels, and a separate local diarizer
// produces the turns — so the merge step can attribute words to speakers. This
// package defines the seam both sides plug into, mirroring stt.STTProvider's
// shape.
//
// BundledDiarizer adapts any inline-diarizing STTProvider to this seam, so the
// diarize benchmark can score the current (bundled) diarization as a reference
// column before a local diarizer exists.
package diarize

import "context"

// Turn is a single-speaker stretch of speech — the diarizer's output unit, the
// same shape an RTTM line carries. The JSON tags are the remote-diarize wire
// shape (see RemoteDiarizer / the /v1/compute/diarize handler).
type Turn struct {
	Speaker      string  `json:"speaker"`
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
}

// TurnsResponse is the /v1/compute/diarize response body: the speaker turns the
// remote diarizer produced. Defined here so the client and server share one
// shape.
type TurnsResponse struct {
	Turns []Turn `json:"turns"`
}

// DiarizeOptions carries the few hints a diarizer can use. It mirrors the
// relevant subset of stt.TranscribeOptions.
type DiarizeOptions struct {
	// NumSpeakers hints the expected number of speakers (0 = unknown/auto).
	NumSpeakers int
	// MeetingID is the noto meeting ID for artifact lineage.
	MeetingID string
}

// Diarizer turns raw audio into speaker turns. Implementations must not assume a
// particular sample rate beyond what they document; callers pass the same audio
// bytes they would hand an STTProvider.
type Diarizer interface {
	// ProviderID identifies the diarizer (e.g. "pyannote-local",
	// "bundled-diar").
	ProviderID() string
	// Diarize returns time-sorted speaker turns for the audio.
	Diarize(ctx context.Context, audio []byte, opts DiarizeOptions) ([]Turn, error)
}

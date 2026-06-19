package service

import (
	"context"
	"strings"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// This file implements both halves of the compute transport (item 2):
//
//   - the CLIENT half — resolveSTTAdapter / resolveDiarizer pick a remote
//     provider when the active config offloads that capability, else fall back
//     to the in-process one. The pipeline calls these, so offloading STT to a
//     GPU box while diarization stays local is pure configuration.
//
//   - the SERVER half — ComputeTranscribe / ComputeDiarize run the capability
//     in-process and are exposed at /v1/compute/*. They DELIBERATELY use the
//     local adapters (newSTTAdapter / activeDiarizer), never the resolve*
//     helpers, so a compute backend can't accidentally re-delegate to itself.

// resolveSTTAdapter returns the STT provider for the next transcribe: a remote
// one when config offloads speech, otherwise the in-process adapter.
func (s *Service) resolveSTTAdapter(ctx context.Context, providerID string) (stt.STTProvider, error) {
	cfg := s.currentCfg()
	if ep, remote := cfg.Compute.Resolve(cfg.Compute.Speech); remote {
		return stt.NewRemoteSTT(ep.URL, s.resolveComputeToken(ctx, ep.TokenRef)), nil
	}
	return s.newSTTAdapter(providerID)
}

// resolveDiarizer returns the diarizer for the next transcribe: a remote one
// when config offloads diarization, otherwise the in-process diarizer (which may
// be nil — diarization stays optional).
func (s *Service) resolveDiarizer(ctx context.Context) diarize.Diarizer {
	cfg := s.currentCfg()
	if ep, remote := cfg.Compute.Resolve(cfg.Compute.Diarize); remote {
		return diarize.NewRemoteDiarizer(ep.URL, s.resolveComputeToken(ctx, ep.TokenRef))
	}
	return s.activeDiarizer()
}

// resolveComputeToken loads a compute endpoint's bearer token from the secrets
// store (the same Keychain/credentials.json path provider and backend tokens
// use). Returns "" when no ref is configured or the secret is missing.
func (s *Service) resolveComputeToken(ctx context.Context, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || s.secrets == nil {
		return ""
	}
	tok, _ := s.secrets.Get(ctx, ref)
	return tok
}

// ComputeTranscribe runs in-process transcription on uploaded audio — the server
// half of the remote STT transport. It always uses the local adapter so a
// compute backend never re-delegates. Returns the raw provider transcript; the
// calling pipeline (on the orchestrating node) owns normalization.
func (s *Service) ComputeTranscribe(ctx context.Context, audio []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	if len(audio) == 0 {
		return nil, notoapi.NewError(notoapi.CodeInvalidRequest, "no audio in compute request", nil)
	}
	provider := strings.TrimSpace(s.currentCfg().Routing.SpeechProvider)
	adapter, err := s.newSTTAdapter(provider)
	if err != nil {
		return nil, err
	}
	return adapter.Transcribe(ctx, audio, opts)
}

// ComputeDiarize runs in-process diarization on uploaded audio — the server half
// of the remote diarize transport. Returns unsupported_capability when no local
// diarizer is wired (rather than empty turns, so the caller can tell "no
// diarizer here" apart from "one speaker").
func (s *Service) ComputeDiarize(ctx context.Context, audio []byte, opts diarize.DiarizeOptions) ([]diarize.Turn, error) {
	if len(audio) == 0 {
		return nil, notoapi.NewError(notoapi.CodeInvalidRequest, "no audio in compute request", nil)
	}
	d := s.activeDiarizer()
	if d == nil {
		return nil, notoapi.NewError(notoapi.CodeUnsupportedCapability, "this compute backend has no diarizer", nil)
	}
	return d.Diarize(ctx, audio, opts)
}

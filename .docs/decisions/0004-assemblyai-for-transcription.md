# ADR 0004: AssemblyAI for transcription and diarization; no local STT

- **Status:** Accepted
- **Date:** 2026-05-20

## Context

noto needs accurate transcription with word timings and speaker diarization. Running STT locally
(whisper.cpp and friends) would mean shipping and maintaining model weights, paying a large CPU
or GPU cost per meeting, and still trailing hosted diarization quality. The product's value is in
what it does *with* a transcript — cited summaries, cross-meeting identity, search — not in being
a transcription engine.

## Decision

AssemblyAI is the production STT + diarization provider, behind the `stt.STTProvider` interface
(`internal/platform/providers/stt`). Raw provider output is immediately normalized to noto's
own `transcript.v1` artifact (`providers.NormalizeTranscript`) before anything downstream reads
it, so the provider is replaceable and its payload is debug data, not a contract. When no key is
configured, the pipeline synthesizes a deterministic placeholder transcript so the full flow
works in development and on Linux.

## Alternatives considered

- **Local/offline STT** (whisper.cpp, etc.) — rejected as a default: model management, weak
  diarization, heavy per-meeting compute. Deliberately out of scope; the `STTProvider` seam
  leaves the door open.
- **Provider-shopping abstraction** (many STT backends, switchable) — premature; one good
  provider plus a normalization seam is enough. The artifact format, not the provider, is the
  stable contract.

## Consequences

- High-quality transcripts and diarization with no local ML for STT.
- Everything downstream depends on the normalized `transcript.v1` shape, never on AssemblyAI's
  JSON — swapping providers is a normalizer change.
- The dry-run fallback keeps tests and Linux development key-free.
- Note: speaker *recognition* (cross-meeting identity) is **not** delegated to AssemblyAI — that
  runs locally on the voiceprint, see [ADR-0005](0005-in-process-ecapa-embedder.md). Audio is
  sent out only for transcription.

## Verification

`internal/platform/providers/stt` (AssemblyAI adapter), `internal/platform/providers/speech`
(normalization), `runTranscribe` in `internal/app/service/jobs_pipeline.go` (key check →
transcribe → normalize → validate, with synthetic fallback).

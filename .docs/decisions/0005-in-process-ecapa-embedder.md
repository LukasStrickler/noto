# ADR 0005: In-process, CPU-only ECAPA embedder for speaker recognition

- **Status:** Accepted
- **Date:** 2026-06-01

> Path note: the benchmarks referenced below (`bench/voiceprint/`, `bench/personas/`) now
> live under `benchmark/` — `benchmark/voiceprint/` and `benchmark/identity/` respectively.

## Context

AssemblyAI ([ADR-0004](0004-assemblyai-for-transcription.md)) diarizes *within* a meeting —
labels A/B/C — but those labels carry no relation across meetings. Linking "the same person" over
time is **speaker recognition**, a separate model. Two forces shaped the choice: it must run on
the **data server's CPU** (no GPU, cheap, co-located with the audio it reads), and voiceprints
are **biometric data** that should never leave the backend for a third party to compute.

A local benchmark (`bench/voiceprint/`) compared six embedding models on CPU for load time, RTF,
footprint, and EER under clean and deliberately hard audio (3 s turns + reverb + babble @ 8 dB).
Clean LibriSpeech saturates near ~2 % EER, so the **hard** column did the curation. ECAPA-TDNN-512
was the fastest (RTF 0.029) and lightest (24 MB), with the best robustness *per unit of compute*;
ResNet293 was only ~0.5 pp better at ~7× the cost; the rest were dominated or broken. See
[benchmarks.md §1](../benchmarks.md#1-embedding-model-selection--benchvoiceprint).

## Decision

noto ships a single embedding model, **ECAPA-TDNN-512** (WeSpeaker ONNX, 192-d), run **in
process** via `onnxruntime_go` with a pure-Go kaldi-fbank frontend and ffmpeg for decode
(`internal/platform/providers/speaker`). The model, the ONNX runtime, and a static ffmpeg are
installed into `<dataDir>/voiceprint` by `noto speaker-model download`; the embedder resolves
**lazily** (`activeSpeakerEmbedder`), so installing it into a running server takes effect on the
next transcribe with no restart. The 192-d output deliberately matches the stored profile-vector
dimension. A remote HTTP embedder is supported as an **override** (`NOTO_SPEAKER_EMBEDDING_URL`)
for split deployments, not the default.

## Alternatives considered

- **A separate voice microservice** (the original plan's "voice service owns everything") —
  rejected for the default: an in-process Go embedder keeps it a single backend binary. The HTTP
  override preserves the split-deployment option without shipping a second service.
- **ResNet293 / TitaNet (SOTA tier)** — marginal accuracy gain at large compute and footprint
  cost; TitaNet also drags in NeMo/torch. Rejected as default; ResNet293 remains a justifiable
  optional download if a large, noisy roster ever needs it.
- **Bundling weights in the binary** — rejected: keeps the binary lean and the model is data, not
  code; it downloads on first use into the data dir.

## Consequences

- Cross-meeting identity with **no GPU** and seconds of compute per meeting (only a few windows
  per speaker are embedded). Biometric vectors stay server-side; audio leaves only for STT.
- Recognition is **opt-in**: with no model installed and no remote endpoint, transcription still
  succeeds and speaker mappings stay `"unmatched"` — never silently wrong.
- One model means embeddings live in **one space**. Profiles are tagged with the model id
  (`"ecapa"`) so a future second model *can* be namespaced, but cross-model gallery isolation is
  not enforced today because only one model ships.
- The download is a plain fetch from upstream (HuggingFace / ONNX runtime releases); there is no
  checksum pinning yet — a hardening follow-up.

## Verification

`internal/platform/providers/speaker/{embedder,ecapa,fbank,media,install}.go`; lazy resolution in
`Service.activeSpeakerEmbedder` and the `NOTO_SPEAKER_EMBEDDING_URL` override in `service.go`;
`noto speaker-model status|download` in `internal/ui/cli/commands_system.go`. Accuracy is
reproduced by `bench/voiceprint/` and the AMI suite in `bench/personas/`.

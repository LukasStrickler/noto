# Speaker identity

How noto gives a recurring voice a stable, cross-meeting identity. This documents the system as
built; for the numbers that justify the choices below — and the corpora behind them — see
[benchmarks.md](benchmarks.md).

## Problem

AssemblyAI diarizes each meeting into local labels (A/B/C) — but "speaker A in Monday's standup"
and "speaker B in Friday's review" carry no relation to one another. noto adds the missing layer:
a local, CPU-only voice-recognition step that links the same person across meetings, assigns a
stable `profile_id`, and lets a name entered once resolve everywhere that person appears.

This is **speaker recognition** (cross-meeting identity), layered on top of AssemblyAI's
**diarization** (within-meeting separation). noto builds only the recognition half; it stays
free of ML in its core packages.

## Where it runs in the pipeline

```
ingest → transcribe → summarize (LLM) → index
                └─ identify: embed each diarized speaker + match against profiles
```

Conceptually, identify is the step that consumes the diarized turns plus the source audio and,
per meeting-speaker, produces `{profile_id, status, confidence}`. In the current code it runs
**inside the transcribe stage** (`EmbedSpeakers` → `matchSpeakers`), not as a separate job — the
pipeline is the four stages above. Extracting it into a standalone, re-runnable stage (so a
rename or merge can re-identify without re-transcribing) is planned, not done.

Identity is stored as IDs rather than baked into text, so a meeting speaker carries a stable
`profile_id` and renaming a profile updates the transcript, the speakers tab, and every summary
item that references it, with no rewrite of stored artifacts.

Identity is computed and persisted server-side (`internal/platform/providers/speaker` for the
embedder, `internal/core/speakers` for the pure decision logic, `internal/platform/speakerstore`
for persistence). The backend is the single source of truth; the TUI is a thin client over
`notoapi`. The decisions behind this design are recorded in
[ADR-0005](decisions/0005-in-process-ecapa-embedder.md) and
[ADR-0006](decisions/0006-precision-first-matching-policy.md).

## Embedding model

noto ships **ECAPA-TDNN-512** (WeSpeaker ONNX): 192-d embeddings, 24 MB, CPU-only, ~30× faster
than real time. It was chosen over five alternatives in a head-to-head study under clean and
deliberately degraded audio — it is the fastest and lightest candidate while holding the best
robustness per unit of compute, and its 192-d output already matches the stored profile-vector
dimension. The full comparison, including why heavier models were not worth defaulting to, is in
[benchmarks.md §1](benchmarks.md#1-embedding-model-selection--benchvoiceprint).

The embedder runs in-process: a pure-Go kaldi-fbank frontend feeds the ONNX model via
`onnxruntime_go`, with ffmpeg decoding any audio or video input. `noto speaker-model
download|status` installs the model, the ONNX runtime, and ffmpeg into the data directory (never
the repo); the embedder resolves lazily, so a model installed into a running server is used on
the next transcribe without a restart. `noto import-audio <file>` runs the whole pipeline on any
existing media file.

## Matching and decision bands

For each diarized speaker, noto gathers their clean speech windows, embeds them, and aggregates
to one comparable vector, then scores that vector by cosine similarity against existing profiles:

- **Robust centroid aggregation** (`speakers.RobustCentroid`, the default in
  `LocalEmbedder.EmbedSpeakers`): a medoid-anchored, bimodality-guarded mean that rejects outlier
  windows. On the AMI corpus this closes the genuine/impostor score overlap (verification EER
  0.09 % → 0.00 %) and lifts auto-recall 96 % → 99 %.
- **Decision bands** from a `MatchConfig` (`Auto=0.70`, `Pending=0.55`, `Margin=0.05`):
  - `≥ 0.70` → **auto** link (silent merge into the matched profile),
  - `0.55–0.70` → **pending** (surfaced for one-click human confirmation),
  - `< 0.55` → **new** profile.
- **Top-1-vs-top-2 margin gate** (`MatchConfident`): when the two best profiles are within
  `Margin`, the auto-link is downgraded to pending — a guard against same-gender collisions that
  costs zero correct auto-links on AMI.
- **Min-enrollment-speech gate** (`MinEnrollSpeech = 3 s`): holds auto-confirm when a speaker
  contributed too little voice to enroll reliably.

Thresholds are **precision-first by design**: 0.70 / 0.55 is tuned never to merge a stranger
silently, accepting a small human-review rate instead — the right trade for biometric identity. A
more permissive 0.58 / 0.50 raises recall but begins false-merging, so it is not adopted.
**AS-Norm** score normalization (`speakers.ScoreASNorm`) is implemented and unit-tested but
marginal on AMI, so it is not enabled in the live matcher.

## Storage

Profiles and their embeddings live in SQLite (`internal/platform/speakerstore`), with vectors
stored as float32 BLOBs. At noto's scale — hundreds to low-thousands of people — brute-force
cosine matching is microseconds per query, so no vector index is needed; a pure-C `sqlite-vec`
index is the planned escalation only past tens of thousands of vectors.

## Privacy

Speaker embeddings are **biometric data** and never leave the backend. Audio is sent to
AssemblyAI for *transcription* only, never for *identity*. noto supports per-person delete /
forget (dropping a profile's vectors and links) and can disable voice identity entirely.

## Known limits

The numbers above isolate the embed-and-match step using ground-truth diarization. In production
the dominant risk is **upstream diarization error** — mislabeled turns poison a profile's
centroid and inflate stranger false-accepts — followed by **channel mismatch** (e.g. an 8 kHz
phone leg), which leaves identification intact but weakens open-set rejection. Per-word
confidence weighting is the intended fix for the diarization case; the robust centroid only
halves the damage. See [benchmarks.md §3](benchmarks.md#3-limitations) for the full breakdown.

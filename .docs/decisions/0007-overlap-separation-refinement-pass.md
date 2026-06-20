# ADR 0007: Overlap speech separation as a deferred, idle-GPU refinement pass

- **Status:** Accepted (design; implementation gated on remote-compute wire, see Consequences)
- **Date:** 2026-06-20

## Context

When two people talk at once, the product must transcribe **both** individually, not a mixed
blur. Today it cannot: STT runs on the single mixed channel and emits one word-stream, and the
merge (`providers/merge`) pins each word to exactly one speaker (the turn with the most
time-overlap). So in overlap the second speaker is garbled or lost — measured ~60% WER in
overlap regions vs ~16% single-speaker.

A measured research arc (`scripts/modal_overlap_sep.py`, `benchmark/dataset/score_overlap_sep.py`,
on real AMI) established the shape of the fix and its limits:

- **Source separation recovers the 2nd speaker** the mix destroys: on substantive 2-speaker
  overlap regions, separating the audio → 2 streams → transcribing each cut overlap cpWER
  **72.3% → 57.1% (+15pt)**. The qualitative goal works.
- **A context-padded window** (±2s around the overlap) helps the separator (free, +1.6pt).
- **Selectors don't generalize:** neither no-reference signals (word-count, stream distinctness)
  nor STT confidence predict per-region win/loss; the only clean predictor (mix cpWER) needs the
  reference. So **always-separate** the substantive overlaps is the robust rule.
- **The separator is at its ungated ceiling (~57%):** SepFormer and Mossformer2 are both
  synthetic-mix-trained and mismatch AMI far-field; the AMI-trained separator
  (`pyannote/speech-separation-ami`) is gated.
- **Aggregate meeting impact is modest (~1.3pt cpWER):** substantive overlap is only ~9% of
  meeting words. The value is **qualitative** (both speakers at the moments they overlap), not a
  big aggregate-metric move.

## Decision

Implement overlap separation as a **deferred, idle-GPU refinement pass that produces a transcript
v2**, never as an always-on stage and never blocking the fast first transcript (v1).

1. **Fast path (unchanged):** `STT(mix) ∥ diar → merge → ECAPA → transcript.v1`. This is what the
   user sees first.
2. **Refinement pass (new, deferred):** detect substantive 2-speaker overlap regions (overlap
   detection + ≥2 speakers each with real content); for each, separate a context-padded window →
   transcribe each stream → merge **both** speakers' words into the transcript, attributed, as
   `transcript.v2` with provenance. Always-separate the qualifying regions; gate only *which*
   regions by cheap diarizer heuristics (overlap duration, ≥2 substantive speakers) — **not** STT
   confidence (proven uninformative on short overlap clips).

The pass is scheduled on **idle GPU** because its aggregate value is modest but its per-moment
value is high: spend only spare compute on it, on only the ~9% overlap audio.

## Alternatives considered

- **Always-on whole-meeting separation** — rejected: the ~1.3pt aggregate gain doesn't justify
  running a separator + second STT pass on every meeting's full audio.
- **Leave it mixed (status quo)** — rejected: it fails the explicit product goal ("transcribe
  both individually").
- **A stronger blind separator (Mossformer2, etc.)** — rejected as the *primary* lever: same
  synthetic→far-field domain mismatch as SepFormer, so it doesn't break the ceiling; not worth
  the integration cost for a modest aggregate payoff.
- **Two-channel capture** — *complementary and preferred for the common case*: capturing the
  local mic and remote/system audio on separate channels separates **you-vs-remote** overlap by
  construction, perfectly, with no model. This pass handles the residual **remote-vs-remote**
  overlap. Two-channel capture is macOS-gated (system-audio capture); see
  `cmd/capture/main.swift` (mic-only today).

## Consequences

- The qualitative goal is met at overlap moments without slowing the first transcript or paying
  separation cost on clean audio.
- **Implementation is gated on the remote-compute wire:** the separator must run where STT/diar
  run (the Modal/`computewire` compute path, Program B2). Until that lands, this is a validated
  design, not shipped code — deliberately not added as dormant pipeline surface.
- Reuses the existing meeting **version** scaffolding (`storage` `CreateVersion`/version
  manifests) for v1→v2 provenance.
- `transcript.v1` stays the contract for summarize/index; v2 is additive (both speakers in
  overlap), never a breaking shape change (see ADR-0001 artifact stability spirit).
- Bounded cost: separation touches only ~9% of audio and runs on idle GPU, so it does not move
  the hosted-batch $/audio-hour KPI materially.

## Verification

Research artifacts and numbers live in `GPU_REDESIGN_PROGRESS.md` ("Overlap separation"):
`modal_overlap_sep.py` (+`score_overlap_sep.py`) measured +15pt overlap-cpWER recovery,
`overlap_regions.py` derives the ground-truth regions, and the meeting-level ROI (~1.3pt
aggregate, 8.7% overlap word-share) is computed from the AMI reference timeline. Implementation
acceptance, when wired: v2 carries both speakers' attributed words in overlap regions; v1 latency
and the $/audio-hour KPI are unchanged.

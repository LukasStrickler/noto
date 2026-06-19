# ADR 0006: Robust aggregation and a precision-first matching policy

- **Status:** Accepted
- **Date:** 2026-06-02

> Path note: the AMI persona benchmark referenced below (`bench/personas/`) now lives
> under `benchmark/identity/`.

## Context

Given embeddings ([ADR-0005](0005-in-process-ecapa-embedder.md)), the question is *when to merge*
a meeting speaker into an existing profile. This is **open-set** recognition: most decisions are
about people already known, but a stranger must not be silently fused into someone else's
identity — a false merge is a biometric error that corrupts every meeting it touches and is hard
for a user to even notice. So the policy is tuned for **precision over recall**: prefer asking a
human over merging wrongly.

The AMI persona benchmark (`bench/personas/`, 52 speakers, ground-truth turns) drove the
specifics. Two failure modes dominated, and neither is the embedder: **upstream diarization
confusion** (a misassigned turn poisons a profile's mean vector) and **same-gender top-2
ambiguity** (two profiles nearly tie). See
[benchmarks.md §2](../benchmarks.md#2-cross-meeting-persona-accuracy--benchpersonas).

## Decision

Matching is cosine similarity against a profile's **single centroid**, with four guardrails:

1. **Decision bands** — `auto ≥ 0.70`, `pending ≥ 0.55`, else `new` (`speakers.AutoThreshold` /
   `PendingThreshold`). Precision-first: a LibriSpeech-calibrated 0.58/0.50 raised recall but
   started false-merging on AMI, so it was **not** adopted.
2. **Robust per-meeting aggregation** — a speaker's windows are combined with `RobustCentroid`: a
   medoid-anchored mean that drops outlier windows, with a *bimodality guard* that falls back to
   the plain mean when the dropped windows form a coherent rival cluster (a diarization error we
   can't safely take sides on). This closed the genuine/impostor overlap (EER 0.09 % → 0.00 %).
3. **Top-1-vs-top-2 margin gate** — `MatchConfident` downgrades an auto-match to pending when the
   best two profiles are within `0.05`, so an ambiguous tie goes to a human (0 % recall cost on
   AMI).
4. **Minimum enrollment speech** — an auto-confirm built from `< 3 s` of voice is downgraded to
   pending; short speakers score markedly lower and shouldn't merge silently.

## Alternatives considered

- **Higher-recall thresholds (0.58/0.50)** — rejected: starts false-merging strangers.
- **AS-Norm cohort score normalization** — *implemented and unit-tested* (`speakers.ScoreASNorm`)
  but **not enabled** on the live path: on AMI the scores already separate, so it added
  complexity without moving the decision. Kept in tree for the channel-mismatch case where it
  should help.
- **Multi-exemplar gallery + bipartite per-meeting uniqueness** (the original plan) — **not
  built**. Profiles are a single centroid; on an auto-match the profile is updated as a plain
  two-vector mean of (stored centroid, new meeting vector). Each meeting speaker is matched
  independently — there is no in-meeting uniqueness constraint yet. Recorded here so the gap is
  explicit; per-word confidence weighting is the more promising next lever for the diarization
  failure mode.

## Consequences

- **Zero false auto-merges** on the AMI gallery, with 100 % of true links at least surfaced for
  one-click confirmation — the right trade for biometric identity.
- The residual cost is a small **pending-review rate**, which channel mismatch (e.g. 8 kHz) can
  inflate; that's a human-in-the-loop cost, not a silent error.
- Honest limits: single centroid (no gallery), plain-mean profile updates, no uniqueness
  constraint, AS-Norm dormant. These bound how far the current numbers generalize and are the
  documented next steps.

## Verification

`internal/core/speakers/{matching,robust,types}.go` (bands, `MatchConfident`, `RobustCentroid`,
`ScoreASNorm`, constants); `Service.matchSpeakers` in
`internal/app/service/jobs_pipeline.go` wires the margin + min-enroll gates and the centroid
update. Benchmarked in `bench/personas/` (`Benchmark50`, `Persona_Conditions`, `Persona_SOTA`).

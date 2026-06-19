# Architecture Decision Records

Short, dated records of architectural decisions that are hard to reverse or that future
readers will wonder about — why a thing is the way it is, and what was rejected.

Write one when a choice (a) shapes the structure of the system, (b) trades off against an
obvious alternative someone will later ask about, or (c) would otherwise live only in a PR
thread. Don't write one for routine or easily-reversible changes.

## Format

Copy [TEMPLATE.md](./TEMPLATE.md) to `NNNN-short-title.md` (next number, kebab-case title).
Keep it short: Context → Decision → Consequences. Set Status to `Accepted` when merged;
mark it `Superseded by NNNN` rather than deleting it when a later ADR replaces it.

## Index

These are recorded retrospectively for the decisions noto is already built on; dates reflect when
each choice was made, not when it was written down. They are grouped from the architectural spine
outward to the flagship speaker-recognition work.

**Architecture**

- [0001 — Backend owns all state; the TUI is a thin client](0001-backend-is-source-of-truth.md) ·
  one source of truth for both humans and agents.
- [0002 — Three deployment modes behind one client](0002-three-deployment-modes-one-client.md) ·
  in-process / local daemon / remote, all behind `notoapi.Client`.
- [0003 — Storage behind the ArtifactRepository seam](0003-storage-behind-artifactrepository.md) ·
  SQLite + filesystem now, Postgres/S3 as one package later.

**Pipeline**

- [0004 — AssemblyAI for transcription & diarization](0004-assemblyai-for-transcription.md) ·
  no local STT; normalize to our own artifact.

**Speaker recognition**

- [0005 — In-process, CPU-only ECAPA embedder](0005-in-process-ecapa-embedder.md) ·
  the benchmarked model choice; biometrics stay server-side.
- [0006 — Robust aggregation & precision-first matching](0006-precision-first-matching-policy.md) ·
  robust centroid and the gates that prevent silent false merges.

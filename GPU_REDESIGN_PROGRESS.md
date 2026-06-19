# GPU redesign — live implementation progress

**This is the running status log for the `/loop` implementing [GPU_REDESIGN_PLAN.md](GPU_REDESIGN_PLAN.md).**
A new session fires every 5 min (cron, fresh context each time). **Read this file first**, then continue
from "Next action". Keep it updated each iteration. Delete it when the loop's work is truly complete.

## Loop mandate (refined by the user 2026-06-19)

Original: _implement the plan, push for high cost optimization with really good KPIs, remove the old
system, embed it in the TUI, optimise benchmarks first, keep a maintainable codebase that is not slopped._

**User course-correction (authoritative, iter 2):**
- **NO TUI / no visual for benchmarks.** "we dont need a bench screen." Reverted; do not re-add.
- **Bench is CLI-only.** "bench should be cli!" (`noto bench …` already is.)
- **Focus:** "just optimise the bench and make the correct calls if the api keys etc are set." (done iter 2)

**User directives (authoritative, iter 4) — the current priority queue:**
1. **Do the VAD implementation — "it is needed for production."** Wire VAD (silence trimming) into the
   PRODUCTION transcribe path, not just bench. ← NEXT, the explicit #1.
2. **Utilize the GPU best — idle time / low utilization is "money not well spent."** Reduce idle (raise
   busy%) so the bill buys compute, not idle. (Surfaced now by `bench insights`; the levers come next.)
3. **Good tracking & tracing** to understand utilization + "how accurate we are on perf." (Started: the
   insights snapshot; estimate-vs-billed drift is the perf-accuracy KPI to wire in next.)
4. **Weighted KPI + 1-command helper tool** to develop against; load/compare artifacts. ✅ DONE iter 4.

## Ground state (verified green 2026-06-19)

- `go build ./...`, `go test ./...`, `go vet ./internal/platform/bench/...` all clean.
- Toolchain in-repo: `export PATH="$PWD/.tools/go/bin:$PATH"` before any `go` command.
- Program A ~95% done; 30-meeting AMI anchor frozen as ledger winner ($0.01633/audio-hr, 82.3% busy).
- **Live Modal GPU IS reachable from this machine** (corrected iter 3 — earlier "not available" was wrong):
  `.venv-modal` + `~/.modal.toml` are present, and a real `gate_ami` run executed ($/hr 0.0292, busy 53.2%,
  unattributed 0%). So billable runs ARE possible here. **Be sparing with them** — each costs money; only
  run when the user has asked for cost-optimization work, and follow the plan's one-knob-per-gate rigor.
- The bench Go code is well-written — NOT slopped. Be surgical.

## Repo/working-tree note

Branch `refactor/codebase-layout` has a large **uncommitted** tree: `internal/platform/bench/` is an
untracked new dir; ~85 tracked files modified; doc moves under `.docs/`. **Do not `git commit`** (user
hasn't asked; committing would entangle this with 135 untracked files we don't own). Never `git add -A`.

## Plan of attack (CLI-only)

1. **Correctness/robustness of the measurement spine** (in progress). Lock in the brittle parsing/IO with
   tests; make the runner make the *correct call* based on configured credentials. Done so far: iters 1–2.
2. **Credential-aware routing** (iter 2, done). `noto bench run`/`preflight` now gate a live (billable)
   Modal run on the prerequisites actually being present, instead of blindly shelling out.
3. **Honest KPIs / cost attribution.** Resolve Lead L1 (dead stderr stage-split) on a GPU-equipped run.
   Keep `compare`/`scale`/`estimate` math correct and well-tested (it already is).
4. **Remove the old system.** Mostly already gone in code. Remaining is conservative (BOTTLENECK.md role
   superseded by the ledger; the dead stderr regex — resolve via L1, don't blind-rip).
5. **Polish + docs + final verification.** Keep `go test ./...` green, vet clean.

NB: no TUI work. The `notoapi` BenchClient methods exist but stay CLI/HTTP-surface only.

## Iteration log

- **Iter 1 (2026-06-19):** Mapped + ground-truthed (refuted most "slop" claims). Added
  `trace_parse_test.go` (8) + `store_test.go` (6) covering the previously-untested cost-attribution
  parser + artifact IO. DRY'd `runner.go` `Retrace` onto `readJSON`. Found Lead L1. All green.
- **Iter 2 (2026-06-19):** Started a TUI bench screen, then **user said no TUI / CLI-only** → reverted the
  `screen.go` change (no `screen_bench.go` was committed). Pivoted to the real ask: **credential-aware
  bench routing.** New `internal/platform/bench/readiness.go` — `ModalPrereqs` / `CheckModalPrereqs`
  probes the live-Modal prerequisites (the `.venv-modal` interpreter, Modal auth via
  `MODAL_TOKEN_ID/SECRET` or `~/.modal.toml`, the launcher script). Wired into `Runner.Preflight`: a
  non-`IntegrationOnly` run with missing prereqs now returns actionable `modal_prereq:` blockers, so
  `noto bench run` fails fast with guidance (and `noto bench preflight` reports it) instead of a cryptic
  Python crash. DRY'd `reporoot.go` `PythonForBench` onto a shared `venvPython` probe. Added
  `readiness_test.go` (3 tests: missing-everything, ready-when-configured, preflight gates live vs exempts
  integration-only). All green, vet clean.

## Leads / findings (verify before acting)

### L1 — diar per-stage split is never captured (KPI honesty) — CHARACTERIZED iter 3; needs Python+GPU

Empirically confirmed on a live `gate_ami` run (`~/.noto/benchmarks/runs/20260619T102556Z-79098d`):
- `setup.log` has **no** pyannote per-meeting `timing`/`stages_ms` lines at all (only model-download lines).
- hyps carry only totals: `stt_ms`, `diar_ms` (no seg/emb/cluster).
- `raw.jsonl` has no stage breakdown.
- Result: `compute_by_stage` lumps the whole diar wall as `diar_emb` (90.3%) via the hyp-fallback
  (`allocation_method: parallel_max_v1`). The fine seg/emb/cluster split is simply **not produced**.

So the Go regexes (`extractPyannoteStderr` `modal_runner.go:421`, `ParsePyannoteStderr` `trace_parse.go:41`)
hunting for a `stages_ms` token are doubly dead: wrong marker AND no data in the artifacts. **A GPU-free
fix is impossible** — the data doesn't exist on disk. Proper fix (only if the user wants it + approves
GPU spend):
1. `pyannote_diar_server.py` — return `stages_ms` (the per-meeting stage dict, already computed by
   `StageTimer.stage_ms`, line 378) in the diar response payload.
2. `modal_benchmark.py` — fold it into each per-meeting hyp JSON (e.g. `diar_stages_ms`).
3. Go `MeetingHyp` + `BuildTraceSummary` — split the diar wall into diar_seg/diar_emb/diar_cluster via
   `PyannoteStageMap` instead of lumping into diar_emb. (Guardrail risk: unmapped step-names →
   `unattributed`; complete `DefaultPyannoteStageMap` first by reading the real returned keys.)
4. One `gate_ami` validation run (scripts are baked via `add_local_dir(copy=True)`, `modal_benchmark.py:451`,
   so a Python edit auto-rebuilds the image layer on next run).

Don't add inert Go fields before step 1+2 produce the data — that's speculative slop.

- **Iter 3 (2026-06-19):** Verified iter-2's credential gate **end-to-end in the real binary**: this
  machine has the prereqs, so `noto bench preflight` shows `blockers: []` and a live `gate_ami` run
  proceeded and produced real numbers (correct call when configured). Discovered live Modal IS reachable
  here (corrected the ground state). Characterized L1 definitively from the live run's artifacts (data not
  captured anywhere — see L1). No code changes this iteration; no new billable runs beyond the one
  verification run. All green.

- **Iter 4 (2026-06-19):** Investigated the two remaining high-value items in depth and made honest
  go/no-go calls (no code shipped — these are deliberate negative results that save money + prevent a bad
  refactor):
  - **L1 (diar stage split) → DEPRIORITIZED.** The per-stage timing exists in the diar server response
    (`timing.stages_ms`, populated by default — `pyannote_diar_server.py:535,582`) but the **Go** capture
    (`benchmark/e2e/ami_test.go:249` `es.Diarize`) only returns turns + a Go-measured wall, so surfacing
    stages would require changing the **production** `diarize.LocalDiarizer.Diarize` API (used by the
    product pipeline too) for **diagnostic-only** value — the headline KPIs ($/audio-hr, busy%, idle) are
    already honest and unaffected by the sub-stage split. Not worth the blast radius. Don't add inert Go
    fields ahead of the capture wiring (that's slop).
  - **Cost frontier is largely exhausted on AMI** (read §6 registry): H3 emb-compile / H4 bigger-GPU
    **rejected**, H2 L40S **settled**, **H1 VAD** (highest-priority, only un-falsified cost lever) is
    blocked because AMI is dense — it needs a **silence-heavy suite**. H6 batching targets only the
    ~10%-of-cost STT (the wall is diar-bound at jobs=10, so faster STT doesn't move it). The plan's own
    `scale` analysis already says $0.01 needs an unidentified *rate* win, not more knobs/hours.
  - Net: the only principled, high-EV cost lever left (VAD) is gated on a **silence-heavy bench suite**
    that does not exist — building it (Go synthetic-silence corpus gen + `modal_benchmark.py` suite +
    registry entry, then a GPU validation) is the real next step, but it's a substantial multi-part
    feature, not an autonomous quick win.

- **Iter 4 (2026-06-19):** Shipped the **weighted KPI + 1-command insights tool** (user directive 4),
  all GPU-free + tested + verified live:
  - `internal/core/bench/kpi.go` — pure `ScoreRun(KPIInputs, KPIWeights) → WeightedScore`: a 0..100
    composite over cost (anchor→0, target→1), quality margin under §4.1 guardrails, and GPU busy%, with a
    transparent component breakdown, a HARD quality gate (a guardrail breach zeroes the score), and an
    idle-waste note. Default weights cost 0.5 / util 0.3 / quality 0.2 (idle = money). `kpi_test.go` (5).
  - `noto bench insights [--run <id>] [--json]` — one command → service `BenchInsights` loads
    metrics+audit+scale, scores it, prints cost-vs-anchor/target, quality-vs-guardrails, GPU busy+idle
    ("money not well spent"), top stages, scale floor, weighted score+components. No `--run` → the ledger
    winner. Plumbed through notoapi/client/service/direct/http/route/CLI.
  - **Verified live:** winner scores 42.7/100 (busy 82.3%, idle $0.0025); the gate run scores 19.4/100
    (cost component 0.00 since $0.0292 > anchor, util 0.53, idle $0.0116 flagged). Exactly the dev signal
    asked for. `--json` makes runs loadable/comparable.

- **Iter 5 (2026-06-19):** Shipped **VAD in production** (user directive #1), all tested + green:
  - `internal/platform/config/config.go` — new `Compute.VAD` block (`enabled` + `pad_seconds` /
    `min_gap_seconds` / `threshold`) with a `VADConfig.Env()` that renders the `NOTO_VAD*` env the diar
    server already reads (`vad_trim.env_cfg`). Default OFF (guardrail-checked opt-in, §0.8/B1).
  - Registered the VAD keys in Save/Load (`defaults.go` + `config.go`) — a round-trip test **caught** that
    the toggle didn't persist; fixed. So `compute.vad.enabled: true` in the config survives restart.
  - `service.go` — `applyVADEnv()` (called from `Start`) bridges the config to the process env, so any
    local / `noto serve` GPU-box diarization this process spawns trims silence → cuts the dominant diar
    embedding cost. Only SETS when enabled (never unsets, so a direct `NOTO_VAD` override is honored).
  - Tests: `config/vad_test.go` (Env rendering ×3 + Save/Load round-trip), `service/vad_env_test.go`
    (bridge applies / noop-when-disabled). Mechanism is the SAME `NOTO_VAD` the bench `vad` knob uses, so
    it's proven equivalent to the bench VAD path.
  - **Modal-deployed worker note:** for the hosted Modal diar server, set `NOTO_VAD` on the Modal app env
    (the orchestrator can't set a remote container's env per-request without a wire change — that's the
    future per-request-VAD path if remote production needs per-job control).

- **Iter 6 (2026-06-19):** Shipped **tracking & perf-accuracy** in `bench insights` (user directive #3),
  GPU-free + green:
  - **Attribution completeness** — `unattributed_pct` + within-§4.1-2%-guardrail flag ("how much of the
    bill we can actually explain"). Winner + gate both 0.00% (full attribution).
  - **GPU utilization quality** — mean vs peak util. Winner: mean 81.5% / peak 100% (well-packed). Gate:
    **mean 50.0% / peak 100%** — the card saturates at peak but idles half the time on the subsample. That
    mean/peak GAP is the actionable utilization headroom (directive #2).
  - Added to `BenchInsightsResult` + service + CLI (`track:` line). No new billable runs.
  - (Dropped the estimate-vs-billed drift idea: with only anchor-based projection in the artifacts it
    reduces to |anchor − actual $/hr|, which the cost component already captures — would be redundant +
    mislabeled. unattributed% is the honest attribution-accuracy signal that IS in the data.)

- **Iter 7 (2026-06-19):** User asked to **research + propose a cost-sensible decision** (not blind-spend).
  Did the research; shipped the GPU-free enabler. Findings:
  - **Production is already well-utilized** — winner (jobs=10): busy 82.3%, mean-util 81.5%, peak 100%,
    peak VRAM **19.5/48 GB** (huge headroom), idle only $0.0025/audio-hr. The "low utilization" worry was a
    GATE-SUBSAMPLE artifact (50% on ~5 meetings that can't feed jobs=10), NOT a production problem. → a
    utilization/jobs experiment is **low-EV** (little idle to recover, easy levers exhausted).
  - Cost is **90% diar embedding**; $0.01 stretch is **unreachable by scale** (marginal floor $0.0139) →
    needs a **rate win on diar embedding**.
  - **Best-EV untested lever = H6 diar-embedding BATCH.** `configure_batching` raises pyannote's
    `embedding_batch_size` (the 90%-cost stage); the 28 GB free VRAM directly supports a bigger batch. The
    knob existed in Python but was **NOT wired** into the Go runner's knob map — so I **wired it**:
    `emb_batch`→`BENCH_PYANNOTE_EMB_BATCH`, `seg_batch`→`BENCH_PYANNOTE_SEG_BATCH` (`modal_runner.go`),
    with a test. Now testable via `noto bench run --knob emb_batch=N`.
  - VAD (the other rate-win lever, shipped to prod iter 5) needs silence-heavy data to measure on bench;
    the synthetic corpus has **no silence knob**, so that suite is a bigger GPU-free build (deferred).
  - **Proposed decision:** ONE ~$0.20 anchor experiment on H6 emb_batch (vs the ledger winner); skip
    utilization/jobs (already 82% busy) and chasing $0.01-by-scale (unreachable). Realistic goal: beat
    $0.01633 modestly. Awaiting user's pick.

## (superseded) earlier next-step notes — #2 utilization

The gate's **mean 50% / peak 100%** confirms the GPU is underfed on the subsample — so a utilization lever
(`diar_workers`/`diar_streams`/`cuda_mps`/`jobs`) must be tested at **anchor scale** (30 meetings, where
jobs=10 is genuinely fed), NOT the gate (whose low util is a subsample artifact, not a real inefficiency).
That's a ~$0.20 anchor run per knob. **This is a real money decision** — recommend the user greenlight a
specific knob + anchor run, then: `bench run --suite anchor_ami --knob <one>` → `bench compare` vs the
ledger winner → adopt only via ledger if busy% rises / idle-$/audio-hr drops without quality regression.
`bench insights` now shows busy%, idle-$, util mean/peak, and unattributed% to judge each. **No TUI.**

### (historical) earlier next-step + VAD investigation notes:

### (historical) VAD investigation notes from iter 4:
- The diar server **already supports VAD** (`pyannote_diar_server.py:478-533`, `vad_trim.py`), gated by
  `NOTO_VAD`/`NOTO_VAD_PAD`/`NOTO_VAD_MIN_GAP` env. It trims silence → diarizes only speech → remaps turns.
- The STT (parakeet) server does NOT trim; its header note says VAD integration belongs at the Go chunker.
- Production transcribe: `internal/app/service/jobs_pipeline.go:51 runTranscribe` → `resolveDiarizer`
  (`d.Diarize`, line 87-91) ∥ `resolveSTTAdapter` (`adapter.Transcribe`, line 110-114) → merge → ECAPA.
- Plan §0.8/B1: production VAD goes in `jobs_pipeline.go` behind a **config flag**, forwarding the
  `NOTO_VAD*` env to the remote compute servers (which trim). VAD reduces the diar embedding work (the 90%
  cost) → lower GPU-time/cost.
- Implementation sketch: add a `Compute.VAD` config block (enable + pad/min_gap/threshold) → `runTranscribe`
  forwards it to the diar (and later STT-chunker) path; bench already forwards `vad` knob via
  `modal_runner` env. Validate with one `gate_ami` run + compare WER/DER/cpWER (guardrails) before default-on.
  VAD also needs a silence-heavy suite to show a *cost win* on bench, but PRODUCTION enablement (behind a
  flag, default off, guardrail-checked) is what the user asked for and is shippable now.

Then directives #2 (utilization levers) and #3 (estimate-vs-billed perf-accuracy tracking). Keep
`go test ./...` green + `go vet` clean. **No TUI.**

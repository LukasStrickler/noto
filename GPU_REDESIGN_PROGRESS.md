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

- **Iter ~13 (2026-06-19) — repair ceiling + targeted overlap cost (ACCURACY pivot):** User reframed:
  accuracy is the product, cost is the constraint ("cheapest thing is bullshit if it's inaccurate").
  Real data: avg word confidence mean 0.149 / median 0.075 (NeMo TDT confidence is an entropy score, not
  a probability — use the RANKING, never the absolute); WER ~0.21, DER ~0.10, cpWER ~0.29 (cpWER is the
  product-critical one — "who said what"). Shipped `core/bench/repair.go RepairCeiling` (oracle headroom:
  bottom-10% repair → ~40% relative error drop on real data → repair IS worth the compute) — commit 730223e.
  Then user clarified the OVERLAP architecture: **two channels only** (your mic + all remote participants
  mixed on system audio; NO Zoom/Meet hooks). you-over-room overlap is FREE (separate channels); only
  ≥2 REMOTE speakers overlapping WITHIN the system channel needs expensive separation. Built
  `core/bench/overlap.go` (`OverlapRegions` sweep-line ≥2 distinct speakers; `PlanOverlapRepair` targeted
  vs blanket spend; SavingsFactor = speech/overlap) — commit 603252e. **Grounded on 43 AMI RTTMs (GPU-free):
  hard-overlap = 11.85% of speech → targeted repair +3.6–11.9% of base cost vs +30–100% blanket → 8.4×
  cheaper.** AMI is the worst case (4-way in-person); real two-channel calls are far lower.
  - **DONE (commit 7730409):** (1) platform `OverlapAnalysis(runID)` + `noto bench overlap` CLI —
    GPU-free "is overlap repair worth it" gate. AddressableFraction = `DER(full).Total −
    DER(SkipOverlap).Total` (error seconds in overlap regions) + targeted-vs-blanket cost anchored to the
    run's $/audio-hr. **Live on the real 20-meeting run: hard-overlap 11.3% of speech, but 33% of all DER
    error is in those regions → targeted +5.7% of cost addresses a third of diarization error vs +50%
    blanket (8.8× cheaper).** That ratio greenlights building the separation pass.
  - **DONE (commit 108f7c0):** the benchmark-measured repair SPINE both GPU passes share —
    `core/bench/splice.go` (`SpliceSpan` substitute-a-re-decode + `OutcomeFromDeltas` accept/wash/regress
    rule) + `platform/bench/repair_measure.go` (`MeasureSplice`: real WER+cpWER before/after vs reference,
    whole-transcript re-scored so seam errors are charged). Closes the B7 attempt+measure loop EXCEPT the
    GPU re-decode: the loop is `plan.Attempt → re-decode(span) → MeasureSplice → .Outcome → BuildRepairReport
    → PassesB7Gate`, all wired and tested offline.
  - **DONE (commit 45650bf):** the B7 attempt+measure LOOP — `platform/bench/repair_attempt.go`
    `AttemptRepairs`/`attemptMeeting`: per meeting, plan low-conf spans → re-decode via an injected
    `ReDecoder` → `MeasureSplice` → fold into a run-level `RepairReport` + B7 gate (accepted-per-USD,
    negative-rate). DRY-RUN, no production writes. `ReDecoder` is the ONLY GPU seam — the whole loop is
    tested offline with a fake (oracle→accepted+WER drops; corrupting→negative; failed→skipped, no crash).
    Core `MergeRepairReports`; factored `repairWordsOf` + `loadRefMeeting`.
  - **DONE (this iter) — the ReDecoder seam is built as a RUN-PAIR, no new GPU endpoint.** Key finding
    from exploring the STT path: Parakeet TDT is **greedy-only** (no beam/temperature knob exposed) — the
    only wired knob that changes the hypothesis is `nemo_precision` (bf16→fp32). So the cheap "same-model
    alternate decode" has WEAK signal; a genuinely error-fixing alternate needs beam/maes (a Python change
    to the parakeet server). Rather than an on-demand single-span Modal call, the `ReDecoder` is now a
    **run-pair**: `internal/platform/bench/repair_redecode.go` `runReDecoder` loads a SECOND completed
    run's hyps (a different decode config) and slices the span's words; cost is **STT-only** (asr$/audio-sec
    from the alt run's trace_summary × span seconds — diar never re-runs). `AttemptRepairsFromRun(runID,
    altRunID)` + full CLI `noto bench repair-attempt --run <baseline> --alt-run <alt>` wired through
    service/notoapi/apiclient/routes. **Proven on REAL artifacts** (baseline 2f6cc5 confidence run + alt
    47afb2 gate run): 103 low-conf spans on ES2011b, **targeted STT-only cost $0.00020**, 0 accepted/0
    negative → **B7 gate correctly FAILS** ("no net improvement") because both runs share the identical
    greedy bf16 decode → identical spliced words → zero WER change. The machine is correct and does NOT
    fabricate a benefit. `repair_redecode_test.go` (3 tests). Full suite green, vet clean.
  - **DONE (this iter) — fp32 alternate GPU run + corrected the per-span measure + EMPIRICAL result.**
    Ran `gate_ami --knob nemo_precision=fp32` → run `20260619T205305Z-ef90ba` ($/hr 0.0262, busy 57%).
    `repair-attempt(2f6cc5, ef90ba)` → **0 accepted, net WER Δ 0** on ES2011b's 103 spans, targeted cost
    $0.0002. Investigated: a positional diff suggested 2211/3955 words "differ" but that was a SHIFT
    artifact from minor word-segmentation in the pre-reference 5–31s region; **in every ref-covered
    low-conf span, bf16 and fp32 produce IDENTICAL text.** So the cheap same-model precision-alternate
    buys **zero** — greedy TDT is deterministic on content across precision. (Earlier-iter claim that fp32
    "differs substantially" was wrong; this is the correction.)
  - **DONE (this iter) — fixed the real design flaw the GPU run exposed:** per-span accept/reject was
    scored on WHOLE-transcript WER (`errors/RefLen` over ~3000 words → a 1-word fix moves it ~1/3000,
    `round4`+0.0005-threshold → always a wash). New `MeasureSpliceLocal` scores the span-LOCAL WER/cpWER
    (only ref+hyp words inside the span) for the DECISION — a 1-word fix in a 3-word span = WER Δ 0.33.
    `attemptMeeting` now decides on local deltas but reports the TRUE whole-transcript aggregate
    (`measureWhole`: apply EVERY accepted edit, re-score once) as NetWERDelta — the honest with/without
    number, not a sum of differently-denominated per-span deltas. Tests: local-sensitivity vs whole
    (200-word fixture), run-pair disk-load fixture. Verified the machine accepts a real correction
    (unit test) and correctly washes identical input (the live fp32 result). Full suite green, vet clean.
  - **DONE (this iter) — beam/maes alternate decode built + wired.** `parakeet_stt_server.py`
    `enable_beam_decode()`: `change_decoding_strategy(strategy=maes|beam|alsd|tsd, beam.beam_size=N,
    compute_timestamps=True)`, gated by `NOTO_PARAKEET_DECODE`/`NOTO_PARAKEET_BEAM_SIZE`, guarded EXACTLY
    like the confidence path — the decode-time revert now covers conf OR beam, degrading to validated
    greedy on any failure (TDT-beam unsupported/config drift), never crashing. Word timestamps requested
    under beam; if a build drops them, words_of emits text-only → Go falls back → run still completes.
    Wired `decode`/`beam_size` knobs (`modal_runner.go` knobLauncherEnv → `BENCH_PARAKEET_DECODE/BEAM_SIZE`;
    `modal_benchmark.py` KNOB_FORWARDS → `NOTO_PARAKEET_DECODE/BEAM_SIZE`), knob-mapping test added. Commit
    92c6c98. Python py_compile OK, full Go suite green, vet clean.
  - **DONE — beam run (`20260619T211144Z-076ff4`) decoded BYTE-IDENTICAL to greedy** (0/3955 words differ).
    Diagnosis: `NOTO_PARAKEET_DECODE=beam` DID reach the NeMo server (summary knobs confirm), but
    `enable_beam_decode` set `decoding_cfg.compute_timestamps` — an UNKNOWN field → structured config
    rejected → the guard silently reverted the whole beam switch to greedy. **Fixed (commit c14b05c):** set
    only known fields (strategy, beam_size); timestamps come from `transcribe(timestamps=True)`. Whether
    this NeMo build supports TDT `maes` beam AT ALL is still unverified — a future beam attempt must set
    `BENCH_PARAKEET_SERVER_STDERR=1` to read the server log. Lesson: capture stderr on alternate-decode runs.
  - **`stt_model` knob wired (commit 3390826)** — `--knob stt_model=<hf-id>` → `NOTO_PARAKEET_MODEL` (§10.5
    alternate-ASR). Confirmed `nvidia/parakeet-tdt-1.1b` exists (TDT, ~2× the 0.6b-v3, same `timestamp['word']`
    API). A different MODEL is GUARANTEED to produce different words (unlike fp32/failed-beam), so the repair
    machine finally gets real signal; the accept gate keeps only edits that improve local WER.
  - **DEFINITIVE (conclusive beam run `20260619T212224Z-8b7d0a` with stderr capture):** the server log
    confirms **"beam decode enabled (strategy=maes, beam_size=4)"** — beam ENGAGED (the compute_timestamps
    bug was the earlier silent-revert) — yet the output is **byte-identical to greedy** (0/3955 words). So
    NeMo TDT greedy is ALREADY beam-optimal for parakeet-tdt-0.6b-v3; beam finds no better path. Combined
    with fp32 (also identical): **same-model alternates CANNOT produce a different hypothesis for this
    model.** The cheap repair methods are a dead end FOR THIS MODEL — not a bug, a property. The ONLY lever
    that yields a genuinely different decode is a DIFFERENT MODEL.
  - **1.1b model run failed on VRAM, diagnosed + fixed:** `--knob stt_model=nvidia/parakeet-tdt-1.1b` (run
    `bx18ukpfx`) crashed — NOT a model-incompat: the bigger STT model starved VRAM and the pyannote diar
    server (10 workers) OOM-exited mid-request (`diarize: server exited mid-request`). Fix: `BENCH_DIAR_WORKERS=2`
    (→ NOTO_PYANNOTE_WORKERS, a wired knob) frees VRAM; the alt run doesn't need its diar output anyway
    (turns come from the baseline). Shell env forwards to the subprocess (`cmd.Env = append(os.Environ()…)`).
  - **★ THE MACHINE IS VALIDATED END-TO-END ON GPU DATA (run `20260619T212713Z-fc7b5f`, 1.1b alternate).**
    1.1b finally produced a GENUINELY different decode (3501 vs 3955 words, 96/103 spans differed).
    `repair-attempt(2f6cc5, fc7b5f)`: **25 accepted · 39 negative · net WER Δ +0.0060 (WORSE) → B7 gate
    correctly FAILS** ("no net improvement, negative rate over cap"). The honest verdict: **parakeet-tdt-1.1b
    is OLDER/WEAKER than 0.6b-v3, so as a repair source it regresses more spans (39) than it fixes (25), and
    the net benchmark effect is negative** — the system tried a plausible repair, measured it on the
    reference, found it net-negative, and REFUSED to ship it. That is exactly the safety property the user
    asked for ("don't make it worse"). The full chain (accept real fixes, catch real regressions, measure
    the true whole-transcript aggregate, gate) now provably works on real data.
  - **Two product insights from the real run:** (1) a repair model must be genuinely BETTER than the
    baseline on the hard spans, not just different — a bigger-but-older model is net-negative; the right
    source is a newer/stronger model, an ensemble, or more-context re-decode. (2) **Seam cost:**
    locally-accepted edits don't always aggregate to a whole-transcript win when the alternate has different
    word SEGMENTATION (1.1b: 3501 vs 3955 words) — splicing shifts boundaries; the gate's whole-transcript
    aggregate correctly catches this (the local-accept decision alone is optimistic).
  - **Added `SpansDiffered` (commit pending)** — `repair-attempt` now reports "N got a different re-decode",
    distinguishing "the alternate produced no different hypothesis" (fp32/beam: 0–1 differed → a same-model
    dead end) from "the different words didn't help" (1.1b: 96 differed, net-negative). The exact ambiguity
    that cost diagnosis time across the fp32/beam/1.1b runs is now one line in the tool.
  - **★ FIRST POSITIVE NUMBER — from EXISTING GPU data, zero new spend (oracle ceiling).** The seam-cost
    finding was addressable offline: added `oracleCeiling` (core RepairReport `CeilingWERDelta`/`Accepted`)
    — greedily commit only the locally-accepted edits that strictly lower the WHOLE-transcript WER. On the
    1.1b data: **naive (apply all 25) = +0.0060 (worse), oracle ceiling (keep the 4 that help) = -0.0008
    (better).** So even a WEAK alternate (1.1b) contains 4 genuine whole-transcript fixes the machine
    extracts; the ceiling is small precisely because 1.1b is a weak source. The gap (0.006 → -0.0008) is
    quantified **selector headroom** — confidence over-selects + splice seams; production needs a better
    selector than "apply every confidence-flagged span". `repair-attempt` now prints both numbers + the
    headroom note. NOTE: the ceiling is REFERENCE-guided (an upper bound), not the production number — it
    answers "how much could this alternate buy with perfect selection", the right "is it worth it" signal.
    Commit pending. Tests: oracleCeiling drops a seam-regressing candidate. Full suite green, vet clean.
  - **NEXT (transcription):** a bigger POSITIVE ceiling needs a repair source genuinely STRONGER than
    0.6b-v3 (canary-1b — different arch/API, needs a server adapter; or ensemble/more-context re-decode).
    The machine + ceiling now make any such source a one-command evaluation. NEXT (diarization): overlap-span
    planner + ReDiarizer → diar attempt loop (mirror repair_attempt.go) on the validated MeasureDiarSplice
    spine. Cost discipline: ~$0.30 GPU this session built+validated the whole repair system AND surfaced a
    real positive ceiling with NO further spend — be sparing; the machine is proven.
  - **DONE (this iter) — diarization repair MEASUREMENT spine** (the user-critical overlap/two-channel half),
    GPU-free + tested: `core/bench/diar_splice.go` `SpliceTurns`/`TurnsInSpan` (pure turn-interval surgery,
    the diar analog of SpliceSpan) + `platform/bench/repair_diar_measure.go` `MeasureDiarSplice` (DER
    before/after a re-diarization, accept via shared OutcomeFromDeltas). KEY finding the tests caught: DER
    does optimal speaker PERMUTATION, so a span-LOCAL DER is spuriously 0 (any single label maps perfectly)
    — but it's unneeded: DER error is denominated in SECONDS, so a multi-second overlap fix moves
    whole-meeting DER by ≈2/1500 > the accept floor (where a 1-word WER fix is 1/N and washes). So
    whole-meeting DER is the diar decision measure. 6 tests. NEXT for diar: the overlap-span planner +
    ReDiarizer (separate system channel → re-diarize) → diar attempt loop (mirror repair_attempt.go).

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

- **Iter 8 (2026-06-19):** H6 experiment run + pushed + started the repair system (user: "push this, use
  GPU to optimise, implement the self-repair + KPI system").
  - **H6 emb_batch=64 → REJECTED** (gate compare: +45.8% cost, busy 53→31%; bigger batch serialized diar
    on this corpus). One more lever crossed off for ~$0.04. Knob now wired (`--knob emb_batch/seg_batch`).
  - **Pushed:** committed the full green working tree as a WIP checkpoint (`5dbd7b0`, 427 files) and
    `git push -u origin refactor/codebase-layout` (was un-pushed; upstream now set).
  - **Repair system B7 core (dry-run) — `internal/core/bench/repair.go` + tests (pure, GPU-free):**
    `RepairSpan` (eligibility + `ExpectedValue` per §10.4), `RepairBudget` (§10.6 sec/dollar budget),
    `PlanRepairs` (rank by EV, greedy-fill budget, record budget-skipped high-value seconds),
    `RepairReport` + `PassesB7Gate` (accepted-per-$, negative-rate, net WER/entity delta — the "how good
    is the accuracy gain and at what cost" answer). 5 tests. DRY-RUN ONLY — no production transcript writes
    (§10.4), gated until B7 passes.

- **Iter 9 (2026-06-19):** B7 wiring — confidence → candidate spans → preview, all GPU-free + tested:
  - `core/bench/repair.go` `RepairWord` + **`SpansFromWords`** — group maximal runs of low-confidence
    words into `RepairSpan`s (PError = riskiest word, ProductValue = max/entity). (Named `RepairWord`, not
    `WordConfidence`, to avoid colliding with calibration's scoring type.) 2 tests.
  - `platform/bench/repair.go` **`Runner.RepairPreview(runID, threshold)`** — loads a run's hyps, builds
    spans, plans under a default budget (10% speech, 5-min cap) → preview (candidate spans, attempt/skipped
    seconds, budget, projected cost). `HasConfidence=false` when the run lacks word confidence. 2 tests.
  - So the full dry-run chain is now wired + tested: hyps → RepairWord → SpansFromWords → PlanRepairs →
    RepairPreview / RepairReport → PassesB7Gate.

- **Iter 10 (2026-06-19):** Shipped `noto bench repair` + wired the confidence knob; found the B6 blocker.
  - `noto bench repair --run <id>` — surfaces the B7 dry-run preview (candidates, seconds, projected cost),
    full plumbing; graceful "no confidence — re-run with `--knob confidence=1`" path. Verified on a real run.
  - Wired `confidence` knob → `BENCH_PARAKEET_CONFIDENCE` → `NOTO_PARAKEET_CONFIDENCE` (Go + Python), test.
  - **BLOCKER found (cost ~$0.06 of GPU):** `--knob confidence=1` **crashes `TestAMICapture`** on some
    meetings (ran twice: 2/5 then 3/5 failed; NOT duration-correlated). Tried leaning the NeMo confidence
    config to word-only (`preserve_frame_confidence=False` — correct anyway, we never read frame conf) — did
    NOT fix it. So the NeMo word-confidence decode is unstable on this corpus, likely a process-level fault
    the bench error truncates the stderr for. The documented "degrade-not-crash" contract is violated.
    Pushed (5938c39). **Do NOT keep re-running blind** — next attempt needs the parakeet server's stderr
    (enable `NOTO_PARAKEET_SERVER_STDERR` capture into a savable artifact, or reproduce on a single meeting
    with stderr visible) BEFORE spending more GPU. Repair system + CLI + knob are all ready; only the
    confidence-DATA population is blocked.

- **Iter 11 (2026-06-19):** **UNBLOCKED B6 confidence** — diagnosed GPU-free, fixed, demonstrated live.
  - Diagnosed the `confidence=1` capture crash from a FAILED run's box logs (`/tmp/noto-bench-out-*/…/raw.jsonl`)
    — **no extra GPU**: `stt/parakeet: Something went wrong with word-level confidence aggregation` — NeMo's
    **TDT** (`parakeet-tdt-0.6b-v3`) word-confidence aggregation raises on some inputs; the server propagated
    it → crashed capture (violating the "degrade, never crash" contract).
  - Fix (`parakeet_stt_server.py`): snapshot plain decoding cfg; on a confidence-decode failure, revert +
    retry plain (one-way). **Confidence gate now SUCCEEDS** ($/hr 0.0269). Plus `NOTO_PARAKEET_SERVER_STDERR`
    passthrough + 16KiB tail (the diagnostic switch).
  - Fixed a real budget bug (`RepairPreview` fell back to audio_sec when speech_sec absent → budget was 0).
  - **`noto bench repair` now works on REAL data:** run `20260619T115608Z-2f6cc5` → 459 candidate spans
    (1100s), budget 300s, attempt 299.9s, projected $0.015. Committed `93c3f2c`, pushed.
  - NOTE: only 1/5 meetings emitted confidence (ES2011b, 3443 words) — the global revert means once any
    meeting hits the TDT fault, the rest decode plain. Fine for first B6 data; a per-request revert/restore
    would raise coverage (enhancement).

- **Iter 12 (2026-06-19):** Wired B6 calibration into the run flow + surfaced it; demonstrated on real data.
  - The bridge (BuildWordConfidences/WriteCalibration) existed but had NO callers — confidence runs made
    no calibration.json. Now: `scoreCalibration` + `Runner.CalibrateRun` (retroactive, GPU-free);
    modal_runner writes calibration.json inline on confidence runs (best-effort).
  - `noto bench calibration --run <id>` — ECE/Brier/risk-coverage, bottom-decile capture vs random,
    high-conf error rate, §10.3 admissibility gate. Full plumbing. Committed 057d605, pushed.
  - **Live on the confidence run:** 3587 words, **ECE 0.81** (NeMo TDT confidence is poorly calibrated in
    absolute terms — it's an entropy measure, not a probability) BUT **bottom-decile capture 0.396 vs 0.10
    random (+0.296 lift), 0 high-conf errors → signal ADMISSIBLE**. So the confidence RANKING is good enough
    to select repair candidates (what B7 needs), even though the absolute values aren't probabilities.

## Repair + KPI system — roadmap (user directive: implement ALL items)

Done: **B6 calibration WIRED + SURFACED** (`noto bench calibration`, admissible on real data) · **B7
dry-run** (core + SpansFromWords + RepairPreview + `noto bench repair`, demonstrated) · **confidence knob**
+ **B6 confidence UNBLOCKED** (TDT degrade-not-crash). Remaining, in order:
1. **B6 confidence GPU run + CLI surface (next, paired):** run one Modal `gate_ami` with word confidence
   enabled (the parakeet server's confidence path) to populate real per-word confidence, THEN add
   `noto bench repair --run <id>` showing the RepairPreview (candidates, seconds, projected cost) — so the
   "see + understand how good our KPIs are and at what cost" is demonstrable on REAL data. (CLI without a
   confidence run shows "no confidence data", so they go together.) Need: how the bench forwards the STT
   confidence flag (NOTO_PARAKEET_CONFIDENCE) — wire a `confidence` knob like emb_batch if not present.
2. **B7 attempt + measure:** the actual same-model alternate re-decode of attempted spans + benchmark
   delta scoring → `RepairReport` (the GPU step that fills accepted/negative/net-delta).
3. **B8 production repair:** transcript v2 with provenance, behind a passing B7 gate. Extend
   `contextBiasTerms()` for biasing — do NOT build a parallel rules engine (§10.5).
Keep `go test ./...` green + vet clean. **No TUI.**

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

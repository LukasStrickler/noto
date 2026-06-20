# GPU redesign — progress & current state

**Running status for the `/loop` implementing [GPU_REDESIGN_PLAN.md](GPU_REDESIGN_PLAN.md).**
A fresh-context session fires periodically. **Read this file first**, then continue from "Next /
blocked". Keep it updated; the per-iteration blow-by-blow lives in `git log` (detailed commit
messages), so this file stays a tight CURRENT-STATE doc, not an append-only log.

Toolchain is in-repo: `export PATH="$PWD/.tools/go/bin:$PATH"` before any `go` command (or `make`).
Commit + push as you go on branch `refactor/codebase-layout` (this is the active working branch).

## Authoritative user directives (most recent wins)

- **GPU repair ONLY — no OpenRouter/LLM repair** (2026-06-20). The LLM context-correction repair path
  was built then REMOVED at user request; do not reintroduce. Test: if a repair calls an external
  LLM/OpenRouter, don't. (The deterministic CPU glossary entity-repair is NOT that, and was kept.)
- **Real transcription + diarization optimization via GPU optimization + repair techniques** — not TUI,
  not test scaffolding. Apply optimizations; don't just build measurement tools.
- **NO bench TUI. Bench is CLI-only** (`noto bench …`). Reverted an early bench screen; do not re-add.
- **Accuracy is the product, cost is the constraint** — "the cheapest thing is bullshit if it's
  inaccurate." cpWER ("who said what") is the product-critical metric.
- **Two-channel capture** (your mic + all remote participants mixed on system audio; NO Zoom/Meet hooks).
  you-over-room overlap is free; only ≥2 REMOTE speakers overlapping within the system channel needs the
  expensive separate-and-re-diarize. **BUT capture is currently MIC-ONLY** — see blockers.

## Ground state (verified green)

- `go build ./...`, `go test ./...`, `go vet ./...` all clean.
- **CI-health baseline (2026-06-20):** `golangci-lint run ./...` is **0 issues** (was 18; fixed in
  commit 89774f7); no flaky tests (the speaker cross-meeting test's map-iteration flake fixed in
  3d51cf8); `go test -race ./...` is **clean across every package** — no data races. Re-running the
  full race suite is ~200s (benchmark/identity alone is ~135s); don't redo it without a concurrency
  change to justify it.
- **Cost north star MET:** 30-meeting AMI anchor is the ledger winner at **$0.01633/audio-hr** (busy
  82.3%), beating the $0.0194 target. Cost is ~90% diarization embedding.
- Live Modal GPU IS reachable here (`.venv-modal` + `~/.modal.toml`); real gate runs (~$0.03) execute.
  Be sparing — only run bounded, high-EV, one-knob gate experiments (the plan's workflow).

## Done (autonomous, GPU-free where possible)

- **Program A measurement spine:** `noto bench run/preflight/compare/estimate/scale/audit/retrace/
  insights/ledger`, credential-aware routing, weighted KPI (`ScoreRun`), GPU util + idle-cost + cost
  attribution, scale-readiness gate. All tested.
- **VAD shipped to PRODUCTION** (`Compute.VAD` config → `applyVADEnv` → diar server `NOTO_VAD*`), default
  off, guardrail-checked.
- **B6 calibration** wired + surfaced (`noto bench calibration`): TDT confidence is an entropy RANKING,
  not a probability (ECE 0.81) — but admissible (bottom-decile capture 0.396 vs 0.10 random, 0 high-conf
  errors), good enough to select repair candidates.
- **GPU repair machine (B7 dry-run, reference-scored, gated):** `noto bench repair-attempt --run --alt-run`
  (run-pair re-decode) + `noto bench diar-repair-attempt --run --alt-run` (run-pair re-diarize). Shared
  core (`OutcomeFromDeltas`, oracle ceiling, `PassesB7Gate`). Validated end-to-end on real data: accepts
  real fixes, rejects regressions, measures the true whole-transcript aggregate, gate is honest.
- **Production entity-repair (deterministic, GPU-free, KEPT):** `core/entityrepair` +
  `providers.RepairTranscriptEntities`, wired into `jobs_pipeline` after normalization. Snaps
  low-confidence words to the meeting glossary/participant names — misspelled + split-compound
  ("data dog"→"Datadog") + standalone name parts. Conservative (exact/confident words untouched,
  segment-safe), glossary-gated (`contextBiasTerms` now also emits individual name parts).
- **OpenRouter/LLM repair path REMOVED** (per directive); summary path untouched.
- **KPI/cost-math correctness audit (9 bugs fixed, +11 regression tests).** Adversarial audit of the
  measurement spine's math (the "really good kpis" foundation) found and fixed 9 verified
  wrong-result bugs: (trace_summary) degraded telemetry billed every meeting the full container cost
  → `ComputeUSD = N×total`; per-stage `round4` drift pushed `ComputeUSD` past `TotalUSD` and the clamp
  hid it as 0% unattributed; run-wide `diarSub`/`diar_emb_ms` reused per-meeting → skewed asr/diar split
  and an N× `diar_emb_ms` column. (metrics) an empty-reference meeting injected hyp insertions into the
  aggregate WER numerator with a zero denominator; `MetricsFromSummaryKPIs` dropped a *measured* 0.0 via
  `value!=0` (a perfect synthetic run read as "no data" and flipped scale/ceiling gates pass→fail).
  (kpi) absent quality AND absent cost were scored as worst-possible (0) with their weight kept in the
  denominator → smoke runs dragged ~10pts, unmeasured-cost runs 2× misranked; now renormalized out.
  (repair) `RepairCeiling` truncated the bottom-decile count to 0 for <10-word slices → false flat
  ceiling; now ceils + floors-at-1 like `BottomFractionCapture`. All conserve invariants now hold.
- **Bench math audit part 2 (3 more bugs fixed, +5 tests).** Swept the remaining math modules
  (scale/spend/compare/overlap); **spend + overlap came back CLEAN**, scale + compare had 3 verified
  bugs: (scale) `FitScaleModel` clamped a negative slope to marginal-0 while keeping a `fixed` from the
  discarded slope → false **$0 floor** + false "reachable by scale" on noisy 1-run data; now REJECTS a
  non-positive slope so the caller falls back to the idle-based floor. (compare) `gpuCompare` computed an
  idle-cost delta against a phantom 0 when only one run sampled the GPU → fabricated "regression / busy
  0%→N%" insight; now nil unless both sides sampled. (compare) `WaterfallDeltas` omitted 7 of 11 additive
  `CostWaterfall` buckets, so a cost move in any of them made the residual gate spuriously `retry` a real
  win; now tracks all buckets. **Bench KPI/cost/scale/compare/metrics math is now audited end-to-end
  (12 bugs fixed across 2 passes); spend + overlap verified clean.**
- **Bench INGEST/parse audit (5 distinct bugs fixed, +7 tests).** Swept the data-IN layer that feeds
  the (now-correct) math. ★ Biggest: the **pyannote stderr regex never matched the live server** —
  it required a literal `stages_ms` token, but the server emits the stages BRACKETED
  (`[segmentation=800 embeddings=4800 clustering=200]`); only the hand-authored fixture used the
  fictional `stages_ms` form, so tests passed while every real run lost its per-stage diar timing.
  Parser now accepts both forms (bracket requires an `=` inside, so the `[pyannote-server]` prefix is
  skipped); fixture corrected to the real format. Also: (metrics) the real producer omits `speech_sec`
  → `addDuration` now falls back to audio seconds (the other 2 consumers already did) so `speech_hours`
  isn't silently 0; (modal_runner) a single truncated hyp file aborted the whole run → now skipped like
  the Python sibling; (CLI) `bench preflight --budget-usd` defaulted to 1.5, under-capping non-gate
  tiers vs `bench run` → now 0 (= tier default); (registry) an unknown suite ran `--quick` but projected
  the full 11.44h corpus → now projects the ~2h subsample. **The bench spine is audited end-to-end
  (math + ingest): 17 correctness bugs fixed across 3 passes.**
- **Bench CLI-rendering audit (3 display fixes, +2 tests).** Last-mile pass over how the (correct)
  numbers are PRESENTED. All 3 were "misleading" (right number, deceptive label): scale-readiness
  printed a $/audio-hr RATE as "10h cost $X" (read as a 10× understated total → now `$/audio-hr@10h`);
  `bench compare` on incomparable runs printed `delta_pct: 0.0%` (a Go zero-value, not a measured
  no-change) while hiding the real `incomparable_reason` → now prints the reason and only shows
  delta_pct when comparable; ledger-winners + run-summary labeled the headline per-audio-hour cost as
  `$/hr` (reads as wall-clock) → now `/audio-hr`, matching every sibling renderer. **The bench spine is
  now audited for correctness AND presentation (20 issues across 4 passes); audit COMPLETE — see
  memory [[bench-kpi-math-audit]].**
- **Optimizations embedded in the TUI ("embed it nicely in the tui").** New **Accuracy**
  config section (`internal/ui/tui/screen_config.go`, `secAccuracy`) surfaces the production
  opts that were previously config-file-only: **VAD silence-trim** is a live toggle (Enter /
  click), **entity-repair** shows as always-on status. Toggle rides the existing `PatchConfig`
  vertical: added `ConfigVAD` to the `notoapi.ConfigCompute` DTO (pointer = patch-presence sets
  the whole posture, the Privacy idiom; never clobbers sibling Compute fields), `publicComputeConfig`
  exposes it, `PatchConfig` applies it + `reapplyVADEnv` (symmetric set/unset via single-source
  `config.VADEnvKeys()`) so a toggle re-syncs the env for the NEXT diar-server incarnation (a
  not-yet-started server picks it up on first start; an already-warm local pyannote singleton keeps
  its posture until it restarts — we don't kill a server mid-diarization). NOT a bench screen (the forbidden thing) — it's the
  production cost lever (VAD trims the dominant diar-embedding cost) made visible + usable. Tested:
  service patch (env both directions + sibling-preserve), TUI render-reflects-state + Enter-toggles
  (read-modify-write carries tuning floats).

## Dead-code sweep — classified, don't re-investigate (2026-06-20)

Ran `golang.org/x/tools/cmd/deadcode` over production reachability, cross-checked vs tests +
interface satisfaction. Most hits are FALSE POSITIVES: `benchmark/*` is test-harness code (not in the
`noto` binary), and the `audio.go`/`*.ProviderID` style hits are interface-satisfying methods. The
genuinely-dead, zero-intent symbols were REMOVED (commits `1ab7b99`, `e07975d`): `storage.MeetingStore`
+ wrapper methods, `DirectoryLayout.RelativeToRecordings`, `storage.ParseVersionID`,
`providers.SortedCapabilities`, and the canonical-JSON checksum trio
(`ComputeJSONChecksum`/`CanonicalJSON`/`sortJSON`, superseded by raw-byte `ComputeChecksum`).

**KEEP — these are FORWARD INFRA, not old system (do NOT remove on a future sweep):**
- **Meeting-versioning** (`CreateVersion` + `WriteVersionManifest`/`ReadVersionManifest`/
  `CopyAudioToVersion`/`ComputeFileChecksum`): `CreateVersion` is tested-only, but `VersionDir`/
  `VersionsDir` are wired into the live `DirectoryLayout` and `EnsureDirs` creates `versions/` —
  reserved space for a future writer (transcript-v2/repair per plan B8).
- **`AudioMetadata` persistence** (`WriteAudioMetadata`/`ReadAudioMetadata`): pairs with the real
  `artifacts.AudioMetadata` artifact type (has `Kind`/`Version`/`Validate`).
- **`modelPool.acquire` + byte-budget eviction**: documented forward infra ("ECAPA today;
  Parakeet/diarizer next"). Caveat: `NOTO_MODEL_MAX_RESIDENT_MB` is parsed + passed to `newModelPool`
  but INERT until `acquire` is wired (only `pin` runs today, which ignores the budget) — a known gap,
  not a bug; honest once heavy models get pooled.

## Key validated findings (hard-won; don't re-derive)

- **Transcription repair sources are weak on AMI (<0.5% WER).** fp32 vs bf16 = byte-identical (greedy TDT
  deterministic on content); beam/maes = byte-identical (greedy already beam-optimal for
  parakeet-tdt-0.6b-v3); different model parakeet-tdt-1.1b = genuinely different but WEAKER/older
  (net-negative, oracle ceiling −0.0008); audio perturbation speed:0.9 = weak (ceiling −0.0004). The
  bottom-decile errors are genuinely-hard AUDIO that same-family decodes fail on too. The two numbers:
  naive (apply all accepted) vs oracle ceiling (keep only whole-transcript improvers) — the gap is
  selector headroom; production needs a better selector than "apply every flagged span".
- **Diar repair, first real KPI (VAD alt, no new spend):** `diar-repair-attempt(no-vad × vad)` →
  **net DER Δ −0.0034 @ $0.0056** (101/621 overlap regions differed; 4 accepted/3 negative; accepted-per-$
  714). Gate FAILS on negative-rate: the accept decision uses the REFERENCE, so −0.0034 is a
  reference-guided CEILING; VAD trims silence, it doesn't SEPARATE overlap → weak overlap source. (Cost
  needed `noto bench retrace` on the VAD run first — its trace had the L1 diar-attribution gap.)
- **~33% of AMI DER lives in overlap regions** (`noto bench overlap`) — the high-headroom half, addressable
  only by real separate-and-re-diarize.
- **The 20.7% headline is a Mix-Headset (overlapping-mix) artifact; the PRODUCT-relevant number is much
  better.** `benchmark/dataset/single_speaker_wer.py` (offline, zero-GPU — reuses an existing run's hyps)
  partitions the anchor transcript by where the reference has exactly 1 speaker active vs ≥2: over 20
  meetings, **single-speaker WER 15.8%** (58.7k ref words) vs **overlap WER 60.1%** (14.6k) → overall 24.7%.
  Since the product captures per-channel (your mic alone; system-audio digitally, no acoustic bleed), its
  real-world case is the single-speaker lane (~16%, near model ceiling), NOT the mixed 20.7%. AMI
  Mix-Headset is therefore a *pessimistic* proxy; all the WER headroom is concentrated in overlap, which is
  the capture-gated separate-and-re-diarize lever — confirming overlap (not STT, not the merge) is the
  accuracy frontier.
- **★ The oracle-count KPI concern is MEASURED and REFUTED on AMI — the KPIs are honest on speaker count
  (don't re-run).** The AMI bench tells the diarizer the true speaker count (`benchmark/e2e/ami_test.go`:
  `numSpk := distinctSpeakers(m.Turns)`), while PRODUCTION (`jobs_pipeline.go:94`) passes `NumSpeakers: 0`
  (auto-detect; the count is unknown at meeting time, so auto is the correct product default). I built a
  `diar_speakers=auto` knob (commit 8077ab0) and ran the gate product-realistically (run
  `20260620T141513Z-ddd52f`, ~$0.05): **DER 0.0838 vs oracle 0.0837 (IDENTICAL), cpWER attribution error
  (cpWER−WER) 0.063 vs 0.062 (unchanged).** Per-meeting, pyannote auto-detect nailed the count on 4/5
  meetings (4 spk) and was off-by-one on one (5 vs 4). So **auto ≈ oracle on AMI** — pyannote's auto speaker
  counting is essentially as good as being told the count; the headline WER 20.7 / DER 8.4 / cpWER 27 do
  NOT materially overstate product accuracy from the oracle advantage. Hypothesis (oracle inflates cpWER)
  was WRONG on AMI. *Secondary:* auto-count needs more GPU memory headroom at high diar concurrency — it
  OOM-crashed the pyannote server at the gate's 10 workers where oracle fit; re-ran clean at
  `BENCH_DIAR_WORKERS=1`. Production diarizes at low concurrency, so this is a bench-saturation artifact,
  not a product issue (but a real note for diar batch sizing under auto-count).
  *Cost side (closed by reasoning, no extra GPU):* the headline $/audio-hr is ~90% diar EMBEDDING, which
  runs per audio-segment REGARDLESS of speaker count; auto vs oracle differ only in the CLUSTERING step
  (cheap compute — the OOM was its *memory* at 10× concurrency, not compute). So auto-count cost ≈ oracle
  cost; the $0.0163 cost KPI holds for production's auto-count config too. The clean per-throughput
  confirmation (auto at jobs=10 with `NOTO_PYANNOTE_WORKERS` tuned to fit VRAM) is the only thing not yet
  *measured* — low-surprise, so not run autonomously. **Net: BOTH the quality and cost KPIs are honest for
  production's auto-count config — the oracle-vs-auto investigation is complete.**
- **Net bench-vs-product honesty (now measured, not assumed):** the one REMAINING optimism/pessimism gap is
  Mix-Headset overlap, which makes WER *pessimistic* — the product captures per-channel, so its real
  single-speaker WER is 15.8%, not the mixed 20.7%. The oracle-count gap I worried about turned out
  negligible (above). So the honest read is the KPIs are if anything *conservative* on WER for the
  per-channel product, and representative on diarization.
- **Offline word→speaker attribution (the merge) is already optimal** —
  `benchmark/dataset/attribution_experiment.py` (zero-GPU, `SLICE_SEC`-windowed for ~10s cheap iteration)
  re-scores cpWER under midpoint / span / span+gap / +smooth re-attribution of each hyp word from the diar
  turns. Baseline 0.2226; every variant is worse-or-noise (midpoint +4.25 pts, span +1.72, span+gap −0.14,
  +smooth −0.09). So the cpWER-vs-WER gap is **not** a fixable merge bug — it's intrinsic diarization/overlap
  error. Rules out an easy offline cpWER win; the only attribution lever left is better diar turns (overlap).
- **DER measurement:** whole-meeting DER is the per-span decision (DER's speaker-permutation makes a
  single-speaker-window local DER spuriously 0). Diar accept-rule has no seam cost (naive == ceiling).
- **Cost frontier largely exhausted on AMI:** H3 emb-compile / H4 bigger-GPU / H6 emb_batch all REJECTED,
  H2 L40S settled. $0.01 is unreachable by scale (marginal floor > $0.01) — needs a *rate* win on diar
  embedding. VAD (the one un-falsified rate lever) needs a silence-heavy suite to show a cost win; AMI is
  dense. Production is already well-packed (busy 82.3%, peak VRAM 19.5/48 GB — the "low util" worry was a
  gate-subsample artifact).
- **L1 — diar per-stage split is not captured in artifacts** (server doesn't emit `stages_ms`; the Go
  parser is dormant-but-purposeful with a graceful hyp-based fallback — do NOT rip it). `noto bench
  retrace` re-derives diar cost from the stored hyp diar-wall (the working path; allocation-aware).

## Blocked / Next (each needs a resource I can't supply autonomously)

- **Stronger transcription repair (IN PROGRESS — user greenlit 2026-06-20).** Built the **canary multitask
  ASR adapter** (commit 5b55b46): server loads any NeMo model via generic `ASRModel.restore_from` + the
  `stt_model` knob; canary needed its `source_lang`/`target_lang`/`pnc` prompts, added via the signature-
  filter so the parakeet TDT path is byte-identical.
  - **Run 1 (`20260620T081051Z-e2ba6f`) — KEY FINDING:** canary-1b-flash is a **40 s-context model**
    (`max_duration=40`, confirmed in the run's raw.jsonl). Fed a whole 30-min meeting via naive transcribe()
    it TRUNCATES — only **1178 of 4531 ref words (0.26×), WER 0.83** vs parakeet's 0.21 (DER identical 0.0837,
    confirming only STT changed). BUT on the part it transcribed canary was CORRECT where parakeet was WRONG
    (ref "okay so we'll try to zip through this since we're short on time" → canary nails it, parakeet
    hallucinates "I was recording but it was very understandable"). So canary IS accurate; the blocker is
    long-form handling + its punctuation (the " . " tokens inflated WER and would pollute a splice).
  - **Fix (commit 2ec706c):** decode canary in fixed ≤30 s windows + stitch with per-window timestamp offsets
    (`words_of` gained `time_offset`); default `pnc='no'` so output is plain lowercase like parakeet/the ref.
  - **Run 2 (chunked, `20260620T082658Z-b05ed8`) — DEFINITIVE NEGATIVE.** Chunking fixed the truncation
    (3052 words, WER 0.83→0.37) but canary-1b-flash is genuinely **WORSE than parakeet on AMI**: WER 0.374 vs
    0.207, cpWER 0.545 vs 0.270 (FAIR comparison — `hypWordTokens` applies `metrics.Normalize`, so canary's
    punctuation/caps are stripped; the residual `pnc='no'`-ignored output is cosmetic). As a repair source:
    `repair-attempt(2f6cc5, canary2)` → net WER Δ +0.0037, **oracle ceiling 0 edits** — zero whole-transcript
    value. (DER identical 0.0837 throughout — only STT changed.)
  - **★ CONCLUSION — transcription-repair sources are EXHAUSTED on AMI (5 validated):** fp32 = identical,
    beam/maes = identical, parakeet-1.1b = weaker, perturbation = weak (−0.0004), canary-1b-flash = WORSE
    (0.37 vs 0.21). On AMI far-field overlapping meeting audio, **parakeet-tdt-0.6b-v3 is already the best
    available model** — no alternative beats it, so model-based transcription repair has NO headroom here. The
    repair MACHINE is fully validated (it correctly rejected every weak source). **Strategic:** transcription
    repair can only show value on a benchmark where parakeet is WEAK (accented / domain-jargon / code-switch /
    noisy non-meeting audio) — i.e. the plan's A8b slice suites, which need datasets. The canary long-form
    adapter (commits 5b55b46, 2ec706c) is kept — it WORKS and would help wherever canary is the stronger
    model; AMI just isn't that place. ~$0.10 GPU spent; conclusion is definitive — do not re-run canary on AMI.
- **Diarization overlap (the 33% headroom)** → separate-and-re-diarize, GATED on real **system-audio
  capture**: `cmd/capture/main.swift` taps the mic only; system audio is an acknowledged stub
  ("requires AudioHardware APIs"). macOS/ScreenCaptureKit work; can't build/validate from this Linux box.
- **Cost** → frontier exhausted on AMI; a real rate-win on diar embedding (cheaper embedding model, or the
  two-channel split that makes the mic channel single-speaker) is the only lever, both non-trivial.

The autonomous GPU-free + zero-spend work is essentially complete and hardened. Further substantive wins
need a greenlit bounded GPU experiment, a stronger-ASR server adapter, or the macOS capture work.

## Plan-verified gate status (2026-06-20 — checked against GPU_REDESIGN_PLAN.md; don't re-derive)

The remaining phases are **gated by the PLAN'S OWN sequencing**, not just by judgment — so the loop is
correctly at a gate, and building the gated phases now would VIOLATE the plan. For future iterations:

- **Program A (measurement spine): COMPLETE** — §0.7 marks all exit criteria `[x]` (met 2026-06-19); the
  spine is additionally audited end-to-end (20 correctness/display fixes) and verified clean.
- **B1 VAD: probed, does NOT adopt on AMI** (dense audio; VAD trims silence and AMI has little). The plan
  (§0.8) makes **B2–B5 production-wire/chunk/latency/throughput GATED until a B1 adopt path wins** — so
  they must NOT be built until VAD adopts, which needs a **silence-heavy suite** (a dataset).
- **B6 calibration: math done + confidence pipeline wired & tested** (§14/B6). The one residual step is a
  Modal run with `NOTO_PARAKEET_CONFIDENCE=1` to populate live confidence — but B7 already validated 5
  repair sources as weak on AMI, so that run would only re-confirm a known dead-end → NOT the "high-EV
  one-knob" the plan requires for spending GPU. Don't run it speculatively.
- **B7 repair: machinery built + validated; NO ROI on AMI** (parakeet is the best model there). **B8
  production repair is gated behind B7 ROI** → not built (would be unused infra; the user's "apply
  optimizations, don't just build measurement tools" applies).
- **TUI: keep the contract STABLE** (plan line 20: "GPU work is an internal implementation swap, NOT a new
  user workflow"). The VAD/entity-repair Accuracy config surface is the right "embed"; adding bench/cost
  screens would violate the contract (and the "NO bench TUI" directive).

**Net: the three real unblocks are all resource decisions (the user's call), not code I can write:**
(1) a **silence-heavy suite** → B1 VAD adopt → unblocks B2–B5; (2) a **parakeet-weak dataset** → B7 repair
ROI → unblocks B8 (the canary/perturbation adapters are built and waiting); (3) **macOS system-audio
capture** → overlap separate-and-re-diarize → the 33% DER headroom.

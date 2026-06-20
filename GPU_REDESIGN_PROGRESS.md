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
  - **Run 2 (chunked, in flight):** validating. NEXT when it lands: standalone WER (expect ≪0.83 — is canary
    actually stronger than parakeet 0.21?), then `repair-attempt(2f6cc5, canary2)` for the repair KPI. If
    canary's standalone WER beats parakeet, the bigger result may be "canary is a better PRIMARY model", not
    just a repair source.
- **Diarization overlap (the 33% headroom)** → separate-and-re-diarize, GATED on real **system-audio
  capture**: `cmd/capture/main.swift` taps the mic only; system audio is an acknowledged stub
  ("requires AudioHardware APIs"). macOS/ScreenCaptureKit work; can't build/validate from this Linux box.
- **Cost** → frontier exhausted on AMI; a real rate-win on diar embedding (cheaper embedding model, or the
  two-channel split that makes the mic channel single-speaker) is the only lever, both non-trivial.

The autonomous GPU-free + zero-spend work is essentially complete and hardened. Further substantive wins
need a greenlit bounded GPU experiment, a stronger-ASR server adapter, or the macOS capture work.

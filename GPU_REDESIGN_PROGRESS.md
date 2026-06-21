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
- **TUI is a CONSUMER frontend (like a web app); don't expose management/config there** (2026-06-20). VAD,
  GPU opts, deployment knobs, etc. belong to whoever DEPLOYS the product (config file / admin API), not the
  end user who "just connects." Reverted the TUI "Accuracy" VAD-toggle section (467e581) for this reason.
  Keep the TUI clean and consumer-focused; new ops/optimization knobs go to deployment config, not a screen.
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
- **VAD/optimization is a DEPLOYMENT config, NOT a consumer-TUI toggle (corrected 2026-06-20).** An
  earlier pass (467e581) added an "Accuracy" config section to the TUI (VAD toggle + entity-repair status)
  — REVERTED, because the TUI is a CONSUMER frontend (like a web app: people just connect and use it), and
  management/optimization knobs (VAD, GPU opts) belong to whoever DEPLOYS the product, not the end user.
  The backend stays: `Compute.VAD` config (file) → `applyVADEnv` at startup is the deployment path, and the
  `ConfigVAD` DTO + `PatchConfig` + `reapplyVADEnv` remain reachable via the ADMIN HTTP route
  (`routes_admin`) for operator/deployment config — so VAD is still set the right way (deploy-time / admin),
  just not exposed in the consumer TUI. Kept the non-TUI improvements from that commit (contextBiasTerms
  computed once per job, `min` builtin, comment fixes). The "embed it nicely in the tui" ask is satisfied by
  keeping the TUI a clean consumer surface — NOT by surfacing ops knobs in it.
- **Production "who said what" correctness review (2026-06-20) — ACTIVE PATH VERIFIED CLEAN.** Swept the
  production transcript-attribution code (the cpWER-critical core), distinct from the heavily-audited bench
  MEASUREMENT spine. Findings: (a) `merge.Attribute` (word→speaker) is correct — midpoint-containment →
  max-overlap → nearest-gap fallback, always returns a speaker when turns exist (no phantom-`""`-speaker
  bug), `resegment` sentinel + gap logic sound. (b) The DEFAULT normalizer chain
  (`NewTranscriptNormalizers` = `DiarizationNormalizer` + `SpeakerLabelNormalizer`) is correct and
  deliberately MINIMAL: DiarizationNormalizer merges adjacent same-speaker turns with duration-weighted
  confidence; SpeakerLabelNormalizer rewrites only the human-readable `Label`, never the `ID`/segment refs
  (so `ValidateTranscript` can't reject). The text-rewriting normalizers (Timestamp/Format/Punctuation/
  Confidence) are deliberately EXCLUDED from the default path (they corrupt provider text — e.g. Format's
  `\b\w+-\s*` mangles "well-known"→"known"; this is KNOWN and handled by exclusion, NOT a bug to "fix").
  (c) FIXED a real bug in `TimestampNormalizer`: the >=30s-gap branch appended the segment, then fell through
  to the unconditional append → DUPLICATED every segment preceding a long gap (commit 9548455, +strengthened
  test). **Honest caveat:** TimestampNormalizer is an exported, tested component but is **not wired into any
  production or bench path** (grep-confirmed) — so the fix hardens shipped library code, but its real-world
  impact is ~nil today (the commit message overstated it as user-facing). Net: the *active* "who said what"
  path is verified correct; one latent bug in an unused exported normalizer fixed.
- **★ Cross-meeting identity: true running-mean voiceprints (2026-06-20) — REAL accuracy fix in the ACTIVE
  path.** Continuing the production review into the speaker-identity code (`matchSpeakers`, the "who is this
  person across meetings" feature): the auto-match centroid update was `Centroid([stored, new])` — a 50/50
  average that DISCARDS how many enrollments the profile already represents, so a single noisy embedding (bad
  audio / overlap contamination) swung an established voiceprint HALFWAY, degrading future matches. The
  variable is even named "centroid" — the intent was a true mean, but with only [stored, new] it degenerated
  to an EMA(α=0.5). Fix: track an enrollment `EmbeddingCount` on the profile and fold each new observation as
  a TRUE running mean (`speakers.RunningMean` weights the existing centroid by its count, the new sample by 1,
  so an n=10 profile moves only ~1/11 per update — robust to one bad sample, while still adapting). Wired:
  new column `embedding_count` (CREATE TABLE default 1 + idempotent `migrateAddColumns` ALTER for existing DBs,
  legacy rows → 1; the PRAGMA-guarded pattern already in speakerstore); New profiles enroll at count=1; Auto
  matches `RunningMean` + increment. Tested: `RunningMean` de-weighting + clamp + dim-validation, the
  `embedding_count` round-trip + upgrade migration, and the end-to-end `matchSpeakers` auto-match (count 4→5,
  status auto). build/vet/lint(0)/race clean. This is a genuine, in-the-hot-path accuracy improvement (more
  robust voiceprints → better cross-meeting "same person" matching → better People-screen suggestions),
  serving the loop's "highly accurate diarization" — unlike the prior normalizer fix, this code IS in the
  default production path.
- **★ Close the human-feedback learning loop + count-weighted merge (2026-06-20).** Follow-on to the
  running-mean fix: the AUTO-match path folded a speaker's voiceprint into the profile, but the two MANUAL
  paths did NOT — `assignMeetingSpeaker` and `PatchMeetingSpeakerMappings` set the profile + "manual" status
  yet never updated the centroid. So the STRONGEST identity signal (a human explicitly confirming "this
  speaker is Alice") taught the model NOTHING, while a lower-confidence auto-match did — backwards. Fix: a
  shared `foldEmbeddingIntoProfile` helper (count-weighted running mean, best-effort) now teaches the profile
  on a genuine manual (re)assignment too — gated on an ACTUAL profile change so a confirm-in-place doesn't
  double-count an embedding the auto path already folded. The auto path was refactored onto the same helper
  (one definition of "learn from an enrollment"). Also fixed `MergeSpeakerProfiles`, which combined two
  profiles' voiceprints 50/50 (`Centroid([t,s])`) ignoring their enrollment counts — merging a 1-enrollment
  profile into a 20-enrollment one swung the survivor halfway; now `speakers.WeightedMean` weights each side
  by its count (and sums them). Added `speakers.WeightedMean` (general two-way weighted mean; `RunningMean`
  now delegates to it). Tested: `WeightedMean` heavier-side/symmetry/clamp/dim-error, `foldEmbeddingIntoProfile`
  count-weighting + no-op safety + first-enrollment, and the auto path's count increment (unchanged).
  build/vet/lint(0)/race clean. Net: EVERY voiceprint update — auto, manual, merge — is now count-weighted,
  and human corrections finally improve cross-meeting identity.
- **★ Close the loop for CONFIRMATIONS, not just reassignments (2026-06-21).** The count-weighted manual fold
  above only fired when a patch CHANGED the profile (`*ProfileID != oldProfile`) — so a REASSIGN taught the
  model, but **confirming a pending suggestion in place** (the single most common identity action: accepting
  the review-queue's "is this Alice?" with the SAME profile) was treated as a no-op and folded NOTHING. The
  guard's premise ("same profile ⇒ already folded by the auto path") is false for a PENDING match — only
  `StatusAuto` folds; pending/new never did. So the human-feedback loop was still open for the very case it
  most needed to close. Fix: extracted the decision into a pure, exhaustively-tested predicate
  `shouldFoldOnPatch(oldProfile, newProfile, oldStatus, newStatus)` — fold iff the profile CHANGED (reassign;
  new profile never saw the voiceprint) OR a PENDING mapping became confirmed in place (genuine new learning).
  Still skips the already-counted cases (auto folded by the auto path, "new" seeded at creation, "manual"
  folded once) so no double-count. 9-case table test pins every transition; build/vet/lint(0)/test clean.

- **★ Upgrade-safety: a stale-dimension profile no longer poisons ALL speaker matching (2026-06-21).** Reviewing
  the identity ACCURACY path (`matchSpeakers` → `MatchConfident`), found `matchSpeakers` built its candidate list
  from EVERY profile regardless of embedding dimension, while `MatchWithConfig` (the live matcher) errors on the
  FIRST candidate whose centroid dim ≠ the query's (a documented strict contract — `TestMatchDimensionMismatch`
  pins it, and `Match`'s siblings `MatchCandidates`/`RankCandidates` SKIP instead, so the matcher and its
  callers already disagree on policy). Net effect: the moment the embedder model ever changes (or any mixed-dim
  profile exists), a single stale-dim profile makes `MatchConfident` return `DimError` → `matchSpeakers` returns
  it → the whole embed step (and identity for EVERY meeting) fails. Fix lives at the CALLER, preserving the
  matcher's strict precondition: `matchSpeakers` now derives the query dimension from the meeting's embeddings
  (all share one, same embedder) and includes only profiles at that SAME dimension — a different-model profile is
  skipped, not fed to the matcher. Same-dim (incl. legacy ECAPA-192) profiles still match exactly as before; a
  future embedder swap degrades gracefully (old profiles ignored until re-enrolled) instead of hard-failing.
  Tested: a 256-dim stale profile alongside a 192-dim match → no error, auto-matches the 192-dim one (the test
  errors without the fix, since List returns the stale profile first). build/vet/lint(0)/race clean.

- **★ Delete a meeting → clean up its speaker mappings (2026-06-21).** `DeleteMeeting` (and the low-level
  `RepoDeleteMeeting`) removed the meeting artifacts + the search-index entry but NOT its
  `meeting_speaker_mappings` rows — which live in a SEPARATE store the artifact repo can't reach. So every
  deleted meeting ORPHANED its mappings: a deleted meeting with unresolved speakers kept inflating the "to
  identify" nav badge forever (phantom work the user can never clear), and the orphans skewed the cross-meeting
  identity priors (`coAttendance` / `profileMeetingCount` / `meetingsForProfiles`) and made `profileMeetings`
  list a meeting that no longer exists. The seed/reset path ALREADY deletes both (so cleanup is the established
  intent); the user-facing delete just never matched it. Fix: both delete paths now `DeleteByMeeting` on the
  mapping store (nil-guarded like the `search` cleanup), and `DeleteMeeting` emits a status-bar refresh so the
  badge drops immediately (consistent with `DeleteSpeakerProfile`). Tested: deleting a meeting removes its
  mappings and decrements `CountUnresolved`, while a mapping in another meeting survives. Also made the in-memory
  mapping fake's `DeleteByMeeting` actually delete (was a no-op, which would have hidden this). build/vet/lint(0)/race clean.

- **★ Re-transcribe no longer destroys manual speaker assignments (2026-06-21).** `matchSpeakers` consulted
  existing PROFILES but never existing MAPPINGS — it blindly `Upsert`ed a fresh auto-match per speaker. So
  re-running transcribe/pipeline on an already-identified meeting (reachable via the generic `CreateJob` API)
  OVERWROTE a user's MANUAL assignment (the strongest identity signal, the whole point of the human-feedback
  loop) with a lower-confidence machine guess — silent data loss on the same (meeting, speaker) key. Fix:
  matchSpeakers now loads the meeting's existing mappings first and SKIPS any speaker whose current mapping is
  `manual`, preserving it as-is; auto/pending/new are still recomputed (safe — they're machine-derived). Tested:
  a meeting with a manual spk_0→Alice survives a re-match whose embedding would otherwise auto-pick a different
  profile. build/vet/lint(0)/race clean.

- **★ Don't lose an in-flight recording on shutdown (2026-06-21).** A recording active when the service shut
  down was LOST: `svc.Close` (and the host's Close) only stopped the meter loop and called `ipc.Close`, never
  `StopRecording` — so the capture was never finalized (no `ipc.Stop` to save the audio, no pipeline enqueued),
  and the user's in-progress "singular meeting note" vanished. Fix composes with the durable job queue +
  crash-resume: `Close` now finalizes an active recording (saves audio + enqueues its pipeline) AFTER the worker
  pool drains (so the fresh job just persists rather than being claimed-then-canceled) but BEFORE the event
  hub / ipc / jobsDB it needs are torn down. The queued pipeline job survives in the jobs DB and the next
  startup processes it → the recording becomes a meeting instead of being dropped. Tested: a dry-run recording
  active at Close leaves a QUEUED pipeline job for its meeting. build/vet/lint(0)/race(service) clean.

- **★★ MULTI-WORD SEARCH WAS COMPLETELY BROKEN — every 2+ word query errored (2026-06-21).** The single
  biggest UX bug found this loop. `sanitizeFTS5Query` emits each word as a `("term" OR term*)` group (exact OR
  prefix) and JOINED them with a SPACE. FTS5 accepts implicit-AND between bare terms (`a b`) but REJECTS it
  between two parenthesized groups — `(a) (b)` is a hard `fts5: syntax error near "("`. So ANY multi-word search
  (`"ship plan"`, `"two words here"`) failed outright; only single words worked. Latent because every existing
  search test used a single-token query. Fix: join groups with an explicit `" AND "` (proven valid by probe;
  keeps all-words-must-match semantics). ALSO fixed an adjacent bug in the same function — a user typing an
  UPPERCASE FTS5 keyword (`AND`/`OR`/`NOT`/`NEAR`) built `("AND" OR AND*)` whose bare `AND*` collided with the
  operator keyword → syntax error; tokens are now lowercased (the unicode61 index folds case, so no matches
  lost). Also removed a redundant `-`/`'`/`_` branch whose comment claimed the opposite of what the code did.
  Tests: a multi-word query returns only docs containing ALL words (AND semantics), and uppercase-keyword
  queries don't error. Full suite + vet + lint(0) clean.

- **★ SSE reconnect leaked a connection on every natural stream-end (2026-06-21).** The remote-mode event
  stream (`apiclient.streamEventsWithReconnect`) closed `resp.Body` ONLY on the `>=400` and heartbeat-timeout
  paths. When the SERVER ended the stream (clean EOF) while the ctx was still alive — the common case — the
  client returned/reconnected WITHOUT closing the body, and the `sseParser` only READS the body (never closes
  it). The transport can't auto-clean a request whose ctx is still live, so each natural stream-end leaked an
  HTTP connection (it never returns to the pool). Over a long remote TUI session with periodic stream-ends this
  accumulates. Fix: close `resp.Body` at the top of the `!ok` (stream-ended) branch and on the ctx-cancel path.
  8 existing reconnect tests verified BEHAVIOR (events, backoff) but never resource cleanup — added a
  close-counting transport test proving the body is released on a server-driven stream-end (fails pre-fix:
  opened=1 closed=0). Subtlety learned + recorded: a ctx-cancel test can't catch this because the transport
  closes the body on ctx cancellation — only a SERVER-driven end exposes the leak.
- **★ IPC read deadline: 30s was dead code; a >100ms helper response failed the call (2026-06-21).** The capture
  IPC client (`appsocket.call`, macOS Swift helper over a unix socket) set a 30s read deadline, then OVERWROTE
  it with a fresh 100ms deadline on EVERY read-loop iteration — so the 30s was dead, and any helper response
  that didn't arrive within 100ms timed out, failed the call, and dropped the connection (partial bytes
  discarded). Start/device-setup latency >100ms would intermittently break recording on macOS. Extracted the
  read loop into `readFramedResponse(ctx, conn, fallback)` — sets ONE deadline up front (the earlier of the 30s
  fallback and the caller's ctx deadline), accumulates until the `\n` terminator / EOF / deadline, reports ctx
  cancellation. macOS-gated so untestable end-to-end here, but the framing is platform-independent and now
  net.Pipe-tested on Linux: a 250ms response succeeds (would fail under the old 100ms), split writes reassemble,
  and a never-responding helper times out at the ctx deadline. The IPC layer had ZERO tests before this.

## ★ GPU-feeding DILUTION — the real hosted-util lever (2026-06-21, found; fix NOT yet built — needs greenlight)

Sharper answer to "maximize GPU utilization on hosted" than the keep-warm/idle-backfill story below. **Production
runs the FULL pipeline in ONE worker** (`JobPipeline` = ingest→transcribe→summarize→index; enqueued by
`recording.go:161` + `import.go:95`). But two of those stages DON'T touch the GPU: `summarize` is an external
LLM call (`OpenRouterAdapter`, seconds–tens-of-seconds on a long transcript) and `index` is local CPU. So a
worker holding a pipeline slot spends a big chunk of its life NOT sending GPU requests.

The offloaded pool is sized to **10 (the measured GPU batch optimum)** — but that optimum was measured on the
GPU stages alone (the accuracy bench runs transcribe+diar, never summarize). With the full pipeline, at any
instant some of the 10 workers are in summarize/index → **effective concurrent GPU requests < 10 → the GPU is
fed BELOW its own optimum**. The dilution scales with the summarize fraction of pipeline wall-time (LLM on a
long transcript is non-trivial). This is invisible to the bench (no LLM) and not captured by the posture pool.

**Fix — CORRECTED 2026-06-21 (the first cut was incomplete).** Splitting the pipeline into a follow-on
summarize+index job is necessary for the UX win but does NOT by itself fix the dilution: a follow-on job run in
the SAME worker pool still occupies a worker slot doing non-GPU work, so the same number of workers are tied up
off-GPU and effective GPU concurrency is unchanged. The dilution's true root cause is **one undifferentiated
pool running both GPU and non-GPU work**, so to feed the GPU at N you'd need >N workers — but a flat oversize
risks N+ concurrent GPU requests (past the contention point) whenever many workers ARE in the GPU stage. The
correct fix DECOUPLES GPU concurrency from worker count: either (a) a **GPU semaphore** of size N that workers
hold only across the GPU call (pool sized larger, GPU requests capped at N exactly), or (b) **separate pools** —
a GPU pool (transcribe, sized to N) + a non-GPU pool (summarize/index). Both keep N concurrent GPU requests
regardless of how many meetings are in the LLM/index tail.
**Big caveat — impact depends on the GPU deployment:** with a RESERVED/fixed GPU (`min_containers≥1`, one box)
the dilution wastes capacity you've paid for → real throughput loss, worth fixing. With Modal AUTOSCALING /
pay-per-request, a worker in summarize just means fewer concurrent requests = lower instantaneous throughput but
NO wasted spend (you don't pay for GPU during summarize) — so the fix mostly buys peak throughput, not cost.
Which regime the product targets decides whether this is worth the infra. **Needs a measured hosted run
(summarize fraction of pipeline wall-time × deployment regime) before building — flagged, not assumed.** The UX
half (transcript-before-summary via a follow-on job) is independently worthwhile and lower-risk.

## GPU utilization — analysis for the user's "avg util / sustained, not just peak" question (2026-06-21)

Grounded in 72 real `.modal-results` runs + the sampler (`scripts/modal_benchmark.py:utilization_stats`,
busy=samples≥50%, 0.5s cadence) and the Go KPI (`trace_summary.gpuUtilization`). Findings, so a future
iteration doesn't re-derive:
- **We are NOT just peak-utilizing.** `mean_utilization_pct ≈ busy_pct` in every run (best batch 93.7 vs 94.1,
  peak 100) — when the card is busy it's genuinely SATURATED (the diar-embedding ResNet pins it), not spiking.
- **The ~18% gap is fixed STARTUP (model load), not steady-state scheduler gaps.** busy% rises with run length:
  `<60s 49% → 60–180s 47%(med)/87%(max) → 180–600s 87% → >600s 94%`. The scale model already decomposes this
  (fixed $0.0189 amortizes to the $0.0139 marginal floor). No avoidable steady-state idle the scheduler leaves.
- **Scheduler is near-optimal for FEEDING** (offloaded pool=10 @ batch optimum + chain-wake → 94% busy at
  scale). The remaining "util all the time" levers are NOT scheduler code: (a) keep-warm — Modal
  `scaledown_window=300s` default keeps warm across <5min gaps, `min_containers=0` scales truly-idle to zero;
  set `min_containers≥1` for zero cold-starts at the cost of paid idle GPU; (b) idle-BACKFILL — the
  `JobPriorityIdle` tier is built to run deferred refinement (overlap separation/repair) on spare GPU, gated on
  the separation GPU endpoint, NOT the scheduler; (c) singular meetings can't be kept busy (no work) — the right
  metric there is latency + idle-cost, not util. The bench CLI already surfaces mean/busy/peak separately.

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
- **`merge.MergeOverlapStreams` + `OverlapStream`/`OverlapRegion`** (`providers/merge/overlap.go`): the
  ADR-0007 v2 merge that splices separated per-speaker overlap streams back into the transcript (both
  speakers transcribed individually). Tested now (`overlap_test.go`); wired when the deferred
  separation/refinement pass lands (gated on remote-compute, B2). Do NOT remove as "dead" — it's the
  documented [[ADR 0007]] consumer.
- **`speakers.AssignStreams` + `SpeakerCandidate`** (`core/speakers/overlap_assign.go`): the ADR-0007
  stream→speaker step — assigns each separated overlap stream to a meeting speaker by ECAPA cosine
  (one-to-one for the equal-count case so the two streams get DISTINCT speakers). Tested now
  (`overlap_assign_test.go`). Together with `MergeOverlapStreams` this completes the Linux-doable refinement
  logic; the gated wire is just: separate (GPU) → ECAPA-embed streams (exists) → `AssignStreams` →
  `MergeOverlapStreams`. Do NOT remove as "dead".

## ★ Overlap separation — the per-speaker-transcription direction (2026-06-20 loop: "2 people talk → transcribe both individually")

**The headline product accuracy lever.** Grounded gap: the merge (`providers/merge/merge.go`) assigns each
word to EXACTLY ONE speaker (the turn with the most time-overlap). So where 2 people talk at once, the
single-stream STT emits one mixed/garbled transcript and the merge pins it all to ONE speaker — **the
pipeline has NO mechanism to transcribe both overlapping speakers separately.** That is the 60% overlap WER.

**Headroom (from `single_speaker_wer.py`, 20 mtg):** overlap is 20% of words at 60% WER vs 15.8%
single-speaker. Perfect overlap separation → overall WER **24.7% → 15.8% (−8.8 pts, 36% relative)**; even
halving overlap WER → 18.7% (−6 pts). cpWER ("who said what") gains MORE — the 2nd speaker's words get a
correct owner instead of being lost/misattributed. This is the single biggest accuracy prize in the system.

**Approach = the plan's H10 (source separation / GSS), tested on AMI** — a separation stage BEFORE STT:
overlap region → 2-speaker separation model (e.g. SpeechBrain SepFormer-WHAMR16k, trained on noisy/reverb
mixes that match AMI far-field) → transcribe each stream → assign to the 2 diarized speakers. (The product's
own answer is two-channel capture — you-vs-remote separates by channel — but that's macOS-gated; remote-vs-
remote overlap still needs this, and AMI is the proving ground I have.)

**Plan: (1) extract overlap regions + per-speaker ref words [DONE — `overlap_regions.py`]; (2) Modal
separation+transcribe experiment [DONE — `modal_overlap_sep.py` + `score_overlap_sep.py`, run ap-nmGhDy0];
(3) score recovery vs single-stream baseline. H10 is "high-cost / prove-first" — proving on small samples.**

**Step-2 first result (7 regions, ES2002a — INFRA WORKS, signal inconclusive but PROOF-OF-CONCEPT seen).**
Pipeline end-to-end: slice overlap region from Mix-Headset → SepFormer-WHAMR16k → 2 streams → parakeet each
→ region cpWER vs the single mixed transcript. On 7 regions: baseline 62.5% == separated 62.5% (null
average), BUT **region 4 is a clear win**: REF A "allergic to animal" (simultaneous with B "ah") — the MIXED
transcript garbled it to "Magic animals", the SEPARATED stream recovered "Allergic to animals". So
separation CAN recover a speaker the mix destroys. The null average is a SAMPLE artifact: most AMI 2-speaker
overlaps are one dominant speaker + a 1-word BACKCHANNEL ("first"/"uh"/"ah") — low separation value, and
SepFormer (balanced-mix-trained) emits a short hallucination ("Thanks"/"No") for the faint 2nd talker.
**Next: filter to SUBSTANTIVE overlaps + a bigger sample.** Cost so far ~$0.05.

**★★ Step-2 DECISIVE RESULT (82 substantive 2-spk overlap regions, ES2002a/b + ES2005a; `min_max_words=3`).
SEPARATION WORKS — overlap cpWER 72.3% → 58.7%, recovery +13.6 pts (19% relative), 36/82 regions improved.**
On the real prize (two people saying substantive things at once, not dominant+backchannel), running
SepFormer-WHAMR16k → 2 streams → parakeet each recovers the 2nd speaker the single mixed transcript loses.
This is the FIRST validated accuracy lever beyond the parakeet/merge ceiling, and it directly answers the
user's "transcribe both individually" goal. Honest caveats: (a) 58.7% residual — SepFormer artifacts +
AMI far-field/balanced-mix mismatch leave headroom (a meeting-matched separator or GSS could push further);
(b) uses REFERENCE overlap regions (oracle overlap detection) — production needs pyannote's overlap
detector in front; (c) SepFormer is 2-speaker, so 3-4-way AMI overlaps (the worst) are out of scope; (d)
overlap is ~20% of words, so the meeting-level cpWER gain is smaller (~2-3 pts) but real, and it's exactly
the "who said what" the product cares about. Total overlap-sep GPU ~$0.10.
**★★★ Step-3 — SELECTOR finding = the production architecture (82-region analysis, no extra GPU).**
Separation is HIGH-VARIANCE: helped 36 / hurt 13 / neutral 33; when it wins it's +33pts, when it loses
−33pts. Root cause (grounded): **separation helps when the MIX is failing, hurts when the mix already got
the dominant speaker** — wins have avg baseline cpWER 0.85, losses 0.50. So gate it:
- always-mix (today) 72.3% → always-separate 58.7% → **confidence-gated selector 56.2%** (separate only
  when mix cpWER ≥ ~0.5) → oracle-selector ceiling 54.4%.
The gate is a DOUBLE win: more accuracy (56.2 vs 58.7) AND compute efficiency — separation runs ONLY on the
overlaps the mix can't handle (a fraction of the ~20% overlap), so the added GPU is tiny and deferrable to
idle time. **Production selector signal = the MIXED transcript's STT word confidence** (low conf ≈ high mix
cpWER ≈ separate) — which the B6 confidence pipeline (`NOTO_PARAKEET_CONFIDENCE=1`) already emits. This
fuses the loop's asks: smart preprocessing (separate) + targeted compute on idle GPU (gated, only-when-
needed) + the 2-speaker "transcribe both individually" recovery.

**Stream→speaker ASSIGNMENT is a real, non-trivial step (confirmed 2026-06-20).** Attempted a no-GPU
end-to-end meeting-level measurement (splice ES2005a's separated streams into its stored base transcript,
score full cpWER) — it produced a bogus −13pt because the separated streams were assigned via the REF
speaker labels while the base transcript uses pyannote's OWN labels: mixing two label spaces breaks cpWER's
permutation. The lesson: separated streams must be assigned to the BASE TRANSCRIPT's speaker labels, which
can't be done from text — it needs ACOUSTIC (ECAPA) assignment (embed each stream, match to the meeting's
speaker centroids). So the ADR-0007 `stream→speaker assign` step is a genuine architecture piece, not a
detail, and the clean meeting-level number needs it (lives in the gated orchestrator). The ground-truth
estimate **~1.3pt aggregate** stands as the best meeting-level figure until then.

**Production architecture (grounded, ready to design):** STT(mix) ∥ diar+overlap-detection → for overlap
regions where mix confidence is LOW: separate → re-transcribe each stream → merge BOTH as attributed
speakers; else keep the mix. The separation pass is a deferred/idle-GPU secondary stage, not a whole-meeting
cost. **Step-3c — no-reference selectors DON'T work; always-separate is robust (free analysis).** Tested whether
a runtime-computable signal (separated/mixed word-count ratio, inter-stream distinctness) predicts the
win/loss so we could gate WITHOUT the ref. They barely discriminate (wins sepw/mixw 1.86 vs losses 1.51;
distinct 0.95 vs 0.88) and gating on them is WORSE than always-separate (61-63% vs 57.1%). So: (a)
ALWAYS-SEPARATE the substantive overlap regions is the robust simple choice (+15pt, 57.1%); (b) the ONLY
clean win/loss predictor is mix QUALITY (baseline cpWER 0.85 wins vs 0.50 losses) → production proxy = mix
STT CONFIDENCE (the server emits `confidences[]` = normalized Tsallis entropy under `NOTO_PARAKEET_CONFIDENCE=1`,
higher = more confident; low conf ≈ mix failing ≈ separate). The selector's role is thus mainly COMPUTE
EFFICIENCY (which overlaps to bother separating) + trimming the −33pt losses, not the headline accuracy.
**UPDATE — confidence proxy ALSO fails (validated, run `.modal-overlap-sep-conf`):** ran with
`NOTO_PARAKEET_CONFIDENCE=1` and captured the mix's per-word confidence; on these short overlap clips the
values compress to ~0 (wins 0.000 vs losses 0.002 — right direction but far too weak) and gating on them is
worse than always-separate (60-62% vs 54.6%). parakeet's entropy-confidence is uninformative on short
overlapping audio. **So BOTH candidate selector signals (no-ref + STT confidence) fail → the production
design is ALWAYS-SEPARATE the substantive overlap regions (robust +12-15pt); the compute-efficiency gate
(which regions to separate) uses cheap DIARIZER heuristics — overlap duration + ≥2 substantive speakers from
pyannote's overlap detection — NOT STT confidence. The per-region keep-mix-vs-sep refinement to trim the
−33pt losses is unsolved and deferred (not blocking the headline win).**

**★★★ Step-4 — MEETING-LEVEL ROI = modest aggregate, big qualitative → the architecture must be CHEAP +
DEFERRED (no GPU, grounded from refs).** The +15pt is on overlap REGIONS, but substantive 2-speaker overlap
is only **8.7% of meeting words** (recoverable 2nd-speaker words 2.9%), so the AGGREGATE meeting-level cpWER
gain is **~1.3 pts realistic (27% → 25.7%), ~2.9 upper bound.** So the honest product picture:
- The QUALITATIVE goal — "when 2 people talk, transcribe BOTH individually" — IS achieved (both speakers
  recovered at overlap moments, the visible UX win the user asked for).
- The AGGREGATE metric barely moves (~1.3pt) because overlap is a small share of words.
- THEREFORE the overlap-separation must be a **cheap, DEFERRED, idle-GPU refinement pass** on only the ~9%
  substantive-overlap audio — NOT a costly always-on stage. The user's "compute optimisation on idle times /
  good scheduler" is exactly the right frame: low-aggregate-value + high-qualitative-value ⇒ spend only idle
  compute on it. This validates the deferred-pass architecture (overlap-detect → separate substantive 2-spk
  regions → re-transcribe → merge both attributed → transcript v2) and tells us NOT to make it block the
  fast first transcript (v1). **Decision: proceed to wire it as the cheap deferred pass; do NOT over-invest
  in separator quality (modest aggregate payoff). The bigger product win for overlap is two-channel capture
  (you-vs-remote, macOS-gated), with this as the remote-vs-remote refinement.**

**Step-3d — stronger BLIND separator (Mossformer2) doesn't help; separator at ungated ceiling.** Tried
Mossformer2 (clearvoice) as the SOTA alt backend — clearvoice INSTALLED + ran but its output format didn't
match (separated streams came back empty), so the run scored == baseline (integration failure, not a real
Mossformer2 result). Reverted the clearvoice path (kept the validated SepFormer script + the clean
`separate()` abstraction). **Strategic conclusion: a stronger BLIND separator wouldn't fundamentally help
anyway** — SepFormer AND Mossformer2 are both trained on SYNTHETIC balanced 2-speaker mixes (WSJ0/WHAM), so
both have the same domain mismatch with AMI far-field PARTIAL overlap. The fix is an **AMI-TRAINED** separator
(`pyannote/speech-separation-ami-1.0`, joint diar+sep, PixIT) — but pyannote separation models are GATED
(need a HF token + accepted terms; the project deliberately uses the ungated community-1 diar mirror). So
**padded SepFormer (~57% overlap cpWER, +15pt) is the best VALIDATED ungated config, and the separator is at
its practical ungated ceiling.** Further separator quality needs either a gated AMI-trained model OR — the
better PRODUCT answer — **two-channel capture** (you-vs-remote separates by CHANNEL, no model, perfect; only
remote-vs-remote needs the model), which is macOS-gated. **Net: overlap-separation RESEARCH is at a strong,
proven conclusion (+15pt lever, always-separate + diarizer-gate architecture, padded, ungated ceiling ~57%).
The next move is PRODUCT: wire the proven lever, not chase marginal ungated separators.** Total overlap-sep
GPU ~$0.40.

**Step-3b — CONTEXT WINDOW improves separation (free, same model).** Separating the TIGHT overlap span
starved SepFormer of context (it's trained on fully-overlapped clips). Fix: `pad_sec` separates a WIDER
window (±2s of surrounding single-speaker audio) then transcribes only the overlap PORTION of each stream.
Result on the same 82 regions: separated cpWER **58.7% → 57.1%** (recovery +13.6 → +15.2 pts, 36 → 38
regions improved). Nearly free (SepFormer is tiny; STT still only sees the overlap portion). So the stack so
far: always-mix 72.3% → tight-separate 58.7% → padded-separate 57.1% → +selector (~−2.5pt) on top → ~no-pad
oracle 54.4%. **Next: (1) STRONGER separator (Mossformer2 / a meeting-matched model) — the 54% ceiling is
SepFormer's synthetic-mix limit and the real product-quality lever; (2) validate the confidence PROXY for
the selector (`NOTO_PARAKEET_CONFIDENCE=1` on the mix); (3) the production wire (overlap-detect → low-conf
gate → padded-separate → re-transcribe → merge both attributed speakers).**

## GPU-efficiency / throughput frontier — grounded diagnosis (2026-06-20, user: "push throughput + strong accuracy")

From the anchor winner's real trace (`20260619T070223Z-7af33b`) + `noto bench scale`, not speculation:
- **diar EMBEDDING = 95.2% of compute** ($0.1198/$0.1258); ASR is 4.8%. Throughput is gated entirely by
  the embedding ResNet's GPU compute.
- **GPU 82.3% busy, 100% peak util while running, peak VRAM 19.5/48 GB (40%).** The embedding is
  **COMPUTE-bound, not VRAM-bound** — so the 40% VRAM headroom is NOT usable throughput headroom, and
  bigger embedding batches (`emb_batch`) are a no-win (same FLOPs) — which matches its prior rejection.
- **Scale model: fixed $0.0189 + marginal FLOOR $0.0139/audio-hr.** The ~18% idle is mostly fixed overhead
  that AMORTIZES with batch size: $/audio-hr → $0.0141 at 100h. So the quality-NEUTRAL throughput lever
  (scale / fill idle) is already near-optimal and bottoms out at ≈ the floor.
- **The floor ($0.0139) is the embedding compute rate.** Below it needs a RATE win: (a) fewer audio-seconds
  embedded = VAD work-reduction — quality-PRESERVING but rejected on AMI (dense: Silero overhead > silence
  trimmed, +25%); REAL remote meetings have turn-taking silence AMI lacks, so this is a BENCH gap, not a
  dead lever; or (b) a cheaper embedding model = quality TRADEOFF, which conflicts with "strong accuracy."
- **Net: the AMI batch is at its quality-PRESERVING throughput frontier.** Quality-neutral packing is done;
  the one quality-preserving rate-win (VAD) can't be shown on pathologically-dense AMI. **"Optimise
  benchmarks first": a realistic meeting suite WITH natural silence is the unlock for the VAD throughput
  lever** — the bench (dense AMI) under-represents the product's real audio. Accuracy frontier stays overlap
  (capture-gated). Both axes' remaining gains are bench/data-gated, not algorithm-gated.

## Done — throughput

- **Posture-aware job worker pool (commit 3736d0c).** Production ran a hardcoded 4-worker pool; each worker
  BLOCKS on its compute call, so it drove ≤4 concurrent meetings → ~4 concurrent GPU requests, capping
  hosted-batch throughput at ~40% of the GPU's proven optimum (jobs=10). `ComputeConfig.JobWorkers()` now
  resolves posture-aware: LOCAL→4 (heavy in-process models, unchanged default), OFFLOADED stt+diar→10 (feed
  the remote GPU at its batch optimum), explicit `Compute.JobConcurrency` overrides. Only the offload
  opt-in changes; local users untouched. End-to-end hosted measurement is B2-gated, but the worker ceiling
  that would bottleneck it is gone.
- **Chain-wake: full-pool ramp on a burst (2026-06-21, modest).** The enqueue-side `notifyWorkers` is a
  buffer-1 COALESCING signal, so a tight burst of enqueues (bulk import/reindex) woke only ~1–2 workers and the
  rest trickled in on the 500ms poll ticker — a slow ramp to the GPU-batch parallelism the posture pool sizes
  for. A worker now chain-wakes a sibling on every SUCCESSFUL claim, so a burst propagates across the whole pool
  near-instantly; when the queue drains the woken sibling finds nothing and parks (one harmless extra claim, and
  the ticker remains the worst-case backstop). No flaky timing test added (a ramp-SPEED change is only
  observable by timing, which the doc's "no flaky tests" invariant forbids; the ticker bounds the worst case so
  the change is safe). build/vet/lint(0)/race(service) clean.
- **GPU-dispatch path reviewed clean (2026-06-21) — don't re-audit.** Swept the actual compute-dispatch code
  for this loop: `runTranscribe`'s STT∥diar concurrency is correct (buffered channel doubles as the join, always
  drained so the diar goroutine can't leak, ctx propagates to both stages, merge is the first point needing
  both); `diarize.RemoteDiarizer` is correct (shares `http.DefaultTransport` so connections pool across the
  hosted batch, body decoded+closed, ctx-scoped request, 30-min ceiling); `ComputeConfig.JobWorkers()` posture
  logic is right (big pool requires BOTH stt+diar remote — if either heavy model runs in-process, workers do
  local compute and must not oversubscribe). The settled dispatch path is solid; the loop's recent wins were all
  in the FRESHEST commits (identity, resume, cancel), confirming the bug surface is new code, not old.
- **★ Priority job scheduler — the "use the GPU on what matters first" knob (2026-06-20).** The worker pool
  CLAIMED jobs strict-FIFO (`ORDER BY created_at`), so a user waiting on their just-recorded meeting sat
  behind any already-queued bulk reindex or multi-GB model download — and the planned deferred idle-GPU
  passes (ADR 0007 overlap separation, GPU repair) would have competed FIFO-equally with live transcription,
  defeating "spend only idle compute on it." Added a `priority` column (single source of truth
  `JobKind.Priority()` in `notoapi`): **Interactive(10)** = the meeting-processing path a user waits on
  (ingest/transcribe/summarize/pipeline/index), **Background(80)** = bulk/housekeeping with no one waiting
  (reindex/verify/download_model), **Idle(100)** = deferred secondary compute (set explicitly via
  `CreateJobOpts.Priority`). `claimNextJob` now `ORDER BY priority, created_at` — highest-priority first, FIFO
  within a tier. This (a) improves TODAY's mixed queue (fresh transcription preempts a queued reindex/model
  download) and (b) is the **scheduling lane the ADR-0007 deferred overlap-refinement pass needs** — when B2
  wires it, it enqueues at `JobPriorityIdle` and runs only when nothing interactive/background is queued,
  realising the loop's "compute optimisation on idle times." Schema is upgrade-safe: fresh DBs get the column
  from `CREATE TABLE`; existing DBs via idempotent `db.AddColumnIfMissing` (PRAGMA-guarded, since SQLite has
  no `ADD COLUMN IF NOT EXISTS`). `Job.Priority` is `json:"-"` (internal scheduler detail, kept off the
  consumer-facing job view per the "clean consumer surface" directive). Tested: kind→priority mapping,
  cross-priority claim order (idle-enqueued-first still claimed LAST), FIFO-within-priority, and the
  old-DB→new-column upgrade path. Build/vet/lint(0)/race all clean.
- **★ Crash-resume: in-flight jobs survive a restart (2026-06-20).** The whole point of a SQLite-persisted
  job queue is durability across restarts, but startup recovery only marked running jobs `interrupted` (a
  TERMINAL state) — so a server restart mid-pipeline **stranded the user's meeting permanently** (it never
  transcribed/summarized unless manually re-triggered). `recoverInterruptedJobs` now RE-QUEUES an in-flight
  job to finish: every pipeline stage overwrites its artifact (transcribe/summarize/index are idempotent —
  verified by reading the stage code), so re-running from the start is safe, and the row keeps its `priority`
  so it re-enters the queue at its **original tier** (composes with the scheduler above — a resumed
  interactive job is re-prioritized correctly). Poison-pill-guarded by the existing `attempt` column
  (incremented on every claim): past `maxJobAttempts=3` a reliably-crashing job is parked as `interrupted`
  rather than crash-looping the server on each startup. Tested: resume-under-cap (→queued, priority preserved,
  progress reset), park-at-cap (→interrupted), and resumed-then-claimable (the resumed job is immediately
  claimable and its attempt increments on re-claim). Build/vet/lint(0)/race clean.
- **★ Graceful-restart resume: don't strand in-flight meetings on a planned deploy (2026-06-21).** Crash-resume
  (above) only covered HARD crashes — jobs left `running` because the process died. A GRACEFUL shutdown was
  different: `Close` cancels the worker pool's context, so the in-flight job's compute returns a ctx error and
  `runJob` recorded it as `JobCanceled` — a TERMINAL state `recoverInterruptedJobs` never resumes. So every
  planned restart/deploy silently stranded up to N in-flight meetings (N = worker count) as "canceled",
  indistinguishable from a user pressing cancel. Fix keys on the TWO cancellation scopes: shutdown cancels the
  worker pool's PARENT ctx, a user `CancelJob` trips only the per-job CHILD ctx (and also sets
  `cancel_requested`). `runJob` now finalizes via a pure `finalizeJobStatus(err, shutdownCanceled, jobCanceled)`
  — shutdown wins and RE-QUEUES (status→queued, priority+attempt preserved, like the single-job
  `requeueInterruptedJob`), so a restart finishes the meeting; a genuine user cancel without shutdown still
  finalizes `canceled`. The re-queue write happens within the worker goroutine `Close` already waits for
  (bgWG), so it lands before the DB closes — no new shutdown race (service-pkg `-race` clean). Tested: pure
  table for all 5 (err × scope) combos + single-job requeue→claimable with priority preserved.
- **★ Wire the WRITE-ONLY `cancel_requested` column → user cancels are now durable (2026-06-21).** Follow-on
  audit of the resume work found `cancel_requested` was SET by `CancelJob` but **read NOWHERE** (grep-confirmed:
  schema + one write, zero reads) — so cancellation lived only in the in-memory per-job ctx and was LOST across a
  restart: a job the user explicitly canceled, if the process died (or shut down) mid-cancel, was RESUMED by
  `recoverInterruptedJobs`. The prior graceful-resume commit also made the shutdown-race variant worse (it
  re-queued a user-canceled job). Root cause: the per-job ctx is canceled in BOTH a user cancel AND a
  shutdown (child of the pool ctx), so `ctx.Err()` can't tell them apart. Fix: `runJob` now uses
  `context.WithCancelCause`; `CancelJob` cancels with the `errJobCanceledByUser` sentinel, so
  `context.Cause` distinguishes a genuine user cancel from a shutdown-propagated one. `finalizeJobStatus` is
  now `(err, shutdownCanceled, userCanceled)` with **user-cancel AUTHORITATIVE** — it wins even if a shutdown
  races it, so a canceled job is never resurrected; a pure shutdown still re-queues. And
  `recoverInterruptedJobs` gained a first step: any `running` row with `cancel_requested=1` is finalized
  `canceled` (not resumed), covering a crash between the `CancelJob` write and the worker finalizing. Tested:
  expanded `finalizeJobStatus` table (user-cancel-wins-over-shutdown case) + a recovery test (cancel_requested
  row → canceled while a normal in-flight row still resumes). build/vet/lint(0)/race(service) clean.
  **Upgrade-safety follow-up (2026-06-21):** making `recoverInterruptedJobs` READ `cancel_requested` at startup
  exposed that the column was only in the `CREATE TABLE` (fresh DBs), NOT in `JobsColumnMigrations` — it predates
  the migration system (added pre-778c257) while `priority` correctly lives in BOTH. So a DB created before the
  column existed gets `priority` back-filled but not `cancel_requested`, and the new startup READ (and the
  pre-existing `CancelJob` WRITE) would then fail to boot it. Added `cancel_requested` to `JobsColumnMigrations`
  (PRAGMA-guarded no-op on every current DB; back-fills the rest, legacy rows → 0). Extended the upgrade test to
  assert both columns are added. This is the convention `priority` already follows; the gap was just never closed
  for the older column.
- **★ Graceful shutdown — Close now joins the worker goroutines (2026-06-20).** `Start` launched the worker
  pool + status-bar/evictor loops as FIRE-AND-FORGET goroutines tied to the caller's ctx, but `Close()`
  neither canceled nor WAITED for them — and it tore down the event hub + model pool FIRST. So a worker
  mid-job could write the jobs DB or `publish` to a closed hub after Close returned: a genuine production
  shutdown race, and the cause of the `TestClientConformance_Direct` TempDir `RemoveAll` "directory not empty"
  flake (a worker recreating the SQLite WAL while the test deleted its temp dir). Fix: the service now OWNS
  its background goroutines' lifetime — `Start` derives a cancelable ctx (`bgCancel`) and runs every goroutine
  through `goBG` (a `sync.WaitGroup`); `Close` cancels then `bgWG.Wait()`s BEFORE closing the hub/pool, so the
  teardown order is cancel → drain → close. Cancel propagates into any in-flight job's context (workers also
  bail out of the claim loop the moment `ctx.Err()≠nil`), so shutdown is prompt, not "run the whole queue
  down." Verified: the previously-flaky conformance test now passes 8/8 isolated + 4/4 full-host-package + 2/2
  full `./...`; vet/lint(0)/race all clean. **Restores the doc's "no flaky tests" ground-state invariant.**
- **Pipeline-internal parallelism VERIFIED exhausted (not just assumed).** Read the actual stage code: STT and
  diarization already run CONCURRENTLY in `runTranscribe` (diar in a goroutine, joined at the merge — the
  first point needing both); the 95%-cost speaker embedding is genuinely data-dependent on the merge; and
  `runPipeline`'s transcribe→summarize→index is a real chain because `indexOneMeeting` reads the SUMMARY
  (`LoadSummary`) to make decisions/actions/risks/questions searchable. So summarize∥index is NOT a free
  parallelization (index needs summary). The cross-meeting parallelism (the posture-aware worker pool) is the
  real lever and it's done. The "splitup parallelization" ask is satisfied where data dependencies allow.

## Preprocessing / "cut out noise" — SHIPPED pure-numpy, validated neutral-safe on AMI (2026-06-20)

Delivered the "cut out noise" lever the dependency-fragility blocked: `scripts/audio_preproc.py` — pure-numpy
high-pass (smooth 80 Hz rolloff) + DC removal, no scipy/nara_wpe, so it CAN'T conflict with pyannote. Wired
into the STT server as `NOTO_PARAKEET_PREPROC` (best-effort, timeline-preserving, off by default = default
decode byte-identical) + a `preproc` bench knob; unit-tested $0 (40 Hz killed, 200/1k/4k Hz preserved, DC
removed). **Validated on AMI (run `20260620T163355Z-81129b`, `preproc=highpass:80,dc`): WER 0.2070 ==
clean-baseline 0.2070, DER identical, cpWER neutral** — i.e. SAFE (doesn't hurt) but no gain on AMI (clean
far-field has little sub-speech rumble; the big error is overlap, which this doesn't touch). Value is on
NOISY real-world product audio (laptop mics, rooms), where AMI can't show it. The run SUCCEEDED where the
earlier nara_wpe gate crashed pyannote — confirming the pure-numpy rule. Available as a safe option; WER-gate
on a noisy suite before any production default.

**Preprocessing thread CLOSED (don't re-attempt):** the cheap CPU transforms (high-pass/DC) are
NEUTRAL on AMI (clean far-field, near-ceiling single-speaker WER) — safe but no gain. The one
preprocessing that could help AMI's real degradation (far-field REVERB) is WPE dereverb, but WPE is
(a) dependency-fragile (`nara_wpe`/scipy break the pyannote image) AND (b) **computationally infeasible
on CPU for full meetings** — per-frequency weighted-least-squares over ~225k STFT frames × 257 freqs ×
iterations; a pure-numpy reimpl hits the same wall without windowed chunking. So dereverb is not a viable
cheap lever here. The big accuracy error is OVERLAP (separation, ADR 0007), which no front-end preprocessing
addresses. Pipeline-internal parallelization is also exhausted: `runPipeline` is sequential by DATA
dependency (summarize needs the transcript, index needs the summary); the real parallelism is across meetings
(the posture-aware worker pool, done).

### (earlier) WPE/nara_wpe attempt — blocked by image fragility

User asks repeatedly for "smart preprocessing / cut out noise." Tried the most legitimate version for
far-field AMI: **single-channel WPE dereverberation** (`nara_wpe`) as an STT-server audio transform +
`dereverb` bench knob (followed the `perturb` pattern). The gate run FAILED — but not at dereverb: adding
`nara_wpe` to the shared image pulled numpy/scipy that **broke the pyannote diar server** (it crashed on
load, "server exited mid-request"). Same class of breakage as the Mossformer2/clearvoice attempt — the heavy
`nemo + pyannote + torch` bench image is FRAGILE to new numpy/scipy packages. **Reverted cleanly (no net
code change).** Lessons: (1) don't add numpy/scipy packages to the shared bench image — implement
preprocessing in PURE numpy (already present) or an isolated image/process; (2) dereverb EV on AMI is modest
anyway (single-speaker WER 15.8% is near ceiling — little reverb headroom on the clean parts; the big error
is overlap, which dereverb doesn't address). ~$0.03 GPU on the failed run. If revisited: pure-numpy WPE (no
dep) or a sidecar image; higher EV on genuinely noisy/reverberant real-world audio than on AMI.

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
  - **★ FOUND a parakeet-WEAK venue: EdAcc accented English (2026-06-20, user-greenlit).** Built
    `fetch_edacc.py` + `modal_edacc_wer.py` + `score_edacc.py` (commits 3da521b, 0914e29) and ran a 53-clip
    spread validation: parakeet EdAcc WER **12.9% overall but strongly accent-dependent** — Spanish 28.7%,
    Jamaican 29.6%, Ghanaian 26.3%, Kenyan 19.3%, Nigerian 16.4%, Indian 16.9% vs Irish 4.3% / US 6.1% /
    Lithuanian 7.3%. So UNLIKE AMI, parakeet has **real headroom on the hard L1 accents** (Spanish/Caribbean/
    African, 17-30%, 2-4× clean). **Next:** the repair experiment should fetch a HARD-ACCENT-FOCUSED slice
    (easy accents have no headroom and only dilute a mixed-set gain), run parakeet + canary, and measure the
    B7 gate — canary is multilingual and *may* beat parakeet on L2 accents (the open question; it lost on
    AMI). Per-accent samples in the 53-clip probe are small (Spanish 87w, Kenyan 171w meaningful; Vietnamese
    11w / Ghanaian 19w too small) — fetch more on the hard accents for a solid claim.
  - **★ DEFINITIVE NEGATIVE — canary loses to parakeet on EdAcc too (repair source dead-end CONFIRMED).**
    Ran canary-1b-flash on the SAME 53 clips (paired; `modal_edacc_wer.py --model nvidia/canary-1b-flash`,
    run ap-TDwdzqW): parakeet WER 12.9% vs **canary 26.6%**, and parakeet wins on EVERY meaningful accent —
    Spanish 28.7 vs 36.8, Kenyan 19.3 vs 31.0, Nigerian 16.4 vs 27.6, Indian 16.9 vs 30.5, even US 6.1 vs
    49.4. Oracle pick-better-per-clip ceiling 12.2% = only 0.75 pts under parakeet (noise from random
    per-clip wins, not a systematic repairable signal). **So even on parakeet's WEAKEST audio, no alternate
    model beats it** — model-swap transcription repair has no ROI anywhere tested (clean AMI + accented
    EdAcc). parakeet-tdt-0.6b-v3 is the strongest available STT for this product, full stop. The repair
    MACHINE is validated (correctly rejects canary); there is simply no superior source. ~$0.04 EdAcc GPU
    spent total. **Repair direction CLOSED — do not pursue model-swap repair further without a genuinely
    stronger model than parakeet-v3.**
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

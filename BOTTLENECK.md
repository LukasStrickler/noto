# BOTTLENECK.md — local STT + diarization is too slow/expensive: diagnosis & fix handoff

**Status:** 2026-06-10. Audience: the engineer/model taking over performance + cost work.
**Goal (from the product owner):** the benchmark **must get cheaper**, and we should
**optimize / parallelize the production path itself** to cut per-meeting time+cost — it is
**far too expensive right now** (~$0.61/audio-hour single-stream on an L4, vs AssemblyAI
retail ~$0.12–0.37/audio-hour for a *managed* service that includes diarization).

This doc is the result of a perf investigation. The orchestration/benchmark harness was already
made parallel + cheaper this session (see "What was already done"); what remains is the **engine
+ serving** work, which is where the real cost lives.

---

## 🆕 CONSOLIDATION + EMBEDDING torch.compile + L4 — quality-safe but fusion is NOT the lever; VAD promoted to #1 (2026-06-12, TWENTIETH pass)

Four GPU runs, ≈ $0.67 total. Three headlines:

1. **The serving stack is ONE architecture now.** The v1/v2 split is deleted, not defaulted: the
   NeMo batched server IS `scripts/parakeet_stt_server.py`, the one-process/N-worker pyannote
   server IS `scripts/pyannote_diar_server.py`, and the concurrent Go client took over the
   canonical `pyannote-server`/`cuda-pyannote-server` names. One code path to maintain, gate, and
   optimize.
2. **torch.compile on the embedding ResNet (the "fused static engine" probe) is quality-IDENTICAL
   but throughput-NEUTRAL-to-NEGATIVE at saturation** — and the way it fooled the small gate is a
   methodology lesson worth keeping. Kernel fusion does not remove conv FLOPs from a card that is
   already compute-saturated; the lever that removes FLOPs-seconds is VAD (fewer audio-seconds
   in), now promoted to #1.
3. **L4 measured and settled: it LOSES** — $0.0208/audio-hr vs L40S's $0.0194 (~7% worse) at ~3×
   the wall, quality digit-identical. The stack now FITS L4 comfortably (14.5 GB peak with
   one-process diar + NeMo bf16), so it is a viable fallback when L40S capacity is tight, but
   L40S stays the default. NeMo bf16 (WER-identical on the 30-meeting corpus TWICE, −5 GB VRAM)
   is now the STT server's DEFAULT precision.

**Consolidation (what changed, all local-tested, $0):**

- `scripts/parakeet_stt_server.py` = the NeMo server (the sherpa/ONNX-Runtime python server is
  DELETED). The Go `parakeet-server` engine no longer requires the ONNX ModelDir — the server
  resolves its `.nemo` from the HF cache — and no longer passes the sherpa-era env
  (`NOTO_PARAKEET_ENCODER/…/THREADS/MODEL_TYPE`). The sherpa CUDA python wheel + `libasound2`
  image layers are gone (smaller image; the sherpa **CLI** baseline engines and their model
  fetches are untouched — they are the local product path, selectable via `BENCH_STT_ENGINE`).
- `scripts/pyannote_diar_server.py` = the one-process/N-worker server with the v1 quality helpers
  (waveform decode, emb-bf16 module wrap, batching config, stage timing) absorbed into the file;
  the lock-step v1 server and the v1 pooled Go engine are DELETED. Serial replica loading (the
  19th-pass native-crash fix) is documented in the module docstring.
- Knob renames (the v2 names are gone): `BENCH_STT_SERVER`/`BENCH_DIAR_SERVER` deleted;
  `BENCH_V2_DIAR_WORKERS`→`BENCH_DIAR_WORKERS`, `BENCH_V2_DIAR_STREAMS`→`BENCH_DIAR_STREAMS`,
  `BENCH_V2_PRECISION`→`BENCH_NEMO_PRECISION`, `BENCH_V2_BATCH`→`BENCH_NEMO_BATCH`; container env
  `NOTO_PYANNOTE_V2_{WORKERS,STREAMS,FAKE}`→`NOTO_PYANNOTE_{WORKERS,STREAMS,FAKE}`,
  `NOTO_PARAKEET_V2_{MODEL,PRECISION,BATCH,FAKE}`→`NOTO_PARAKEET_{MODEL,PRECISION,BATCH,FAKE}`.
  Historical pass sections below keep the old spellings — they describe what ran then.
- New knob: **`BENCH_PYANNOTE_EMB_COMPILE`** (`NOTO_PYANNOTE_EMB_COMPILE`) — torch.compile the
  embedding module's forward, composed UNDER the bf16 autocast wrap; `reduce-overhead` rejected
  (CUDA graphs don't compose with per-worker streams). Default OFF (see below).

**Measured (pinned corpus, L40S, jobs=10 unless noted):**

| arm | WER | DER | cpWER | $ run | $/audio-hr | util | peak VRAM |
|---|---|---|---|---|---|---|---|
| 30-meeting baseline (`…dfe7cc`, 19th pass, L40S) | 23.5 | 11.3 | 31.1 | $0.2224 | $0.0194 | 87% | 22.3 GB |
| 30-meeting + emb-compile (`…8511fc`, L40S) | **23.5** | **11.3** | **31.1** | $0.2274 | $0.0199 | 77% | 20.4 GB |
| 4-meeting gate + emb-compile (`…185b45`, jobs=4, L40S) | 25.7 | 13.9 | 32.7 | $0.0637 | — | 45% | 15.0 GB |
| 30-meeting **L4** (`…5bbe09`, workers=6, NeMo bf16) | **23.5** | **11.3** | **31.1** | $0.2375 | $0.0208 | 94% | 14.5 GB |

Quality is identical TO THE DIGIT in both compile arms (the compiled kernels are numerically
equivalent modulo fusion-order float association — measured, not assumed). Throughput: total diar
wall +10% at 30 meetings (per-stream 13.4×→12.2×), +2.2% cost. The compile tax is ~45 s PER WORKER
(visible in the stage timing as `speaker_counting` 44–47 s vs 2.2–3.0 s baseline — it is the first
stage to call the embedding forward), paid once per worker per shape-set; the in-process inductor
cache did NOT dedupe concurrent worker compiles (all 10 paid full price simultaneously).

### The contention artifact (don't get fooled again)

The 4-meeting gate initially looked like a ~1.7× steady-state win: the same meeting's warm
`embeddings` stage ran 29.4 s under compile vs ≈58 s baseline-derived. That comparison was FALSE:
in the compile arm, while one worker's embeddings ran, the other three workers were still in their
~45 s CPU-bound compile phase — so the measured stage ran nearly UNCONTENDED on the GPU, vs the
baseline where all four streams contended. The honest 30-meeting/10-way A/B (all streams warm and
contending) shows fusion buys ~nothing: the card was already at its bf16 conv roofline (the
19th-pass conclusion, re-confirmed from a different angle). **Corollary: a TensorRT/ONNX fp16
static engine — same fusion thesis, heavier build, plus the known fp16 DER risk — is DEMOTED.
Fusion does not remove FLOPs; only fewer audio-seconds (VAD) or a cheaper card ($/FLOP) do.**

### L4 arm: first attempt died of a poisoned CUDA context (and the server is now hardened)

The first L4 30-meeting arm (jobs=10, workers=10, NeMo bf16) failed at $0.1396: a **CUDA "illegal
memory access"** hit the diar process during the serial replica ramp (workers 0–5 up, requests
flowing), and since a poisoned context never recovers, the server then emitted request-level
errors for ALL 24 remaining meetings — the Go client saw application errors, not transport
failures, so it never restarted the process. Two responses shipped:

- **Server hardening (permanent):** `_fatal_cuda()` — a worker that catches an
  illegal-memory-access-class CUDA error now `os._exit(4)`s after answering, so the shared client
  restarts a FRESH process/context and per-caller retries recover the run instead of riding a
  dead context to the end. (The same pattern as the fail-fast-on-CPU guard: never let a broken
  GPU state burn rented time quietly.)
- **Retry shape:** workers capped at 6 on the 24 GB card (less VRAM pressure, less load/infer
  overlap; measurement-valid because aggregate throughput is the card's compute ceiling, not the
  stream count — 19th pass).

### What's next, re-ranked (20th-pass truth)

1. **VAD-gating (TODO-7, M) — now the #1 and essentially the ONLY remaining compute lever.**
   ~1.2× on AMI (mostly-speech corpus); ×1.3–3 on real meetings (30–70% silence). It is also the
   substrate the error-correction/confidence roadmap rides on. Gate on DER/WER as always: VAD
   changes what the models see, so it MUST clear the pinned corpus.
2. **GPU right-sizing: SETTLED this pass.** L4 = $0.0208/audio-hr vs L40S $0.0194 (quality
   identical, 94% util, fits in 14.5 GB) — L40S keeps the best $/FLOP on Modal's menu for this
   compute-bound stack; L4 is the fallback, not the default. Cheaper-card arbitrage is exhausted
   until the FLOPs themselves drop (i.e., after VAD).
3. **emb-compile default stays OFF** — quality-proven knob, kept for long-lived/production warm
   servers where the per-worker tax amortizes over hours (it IS free headroom there), but wrong
   for benchmark-shaped runs.
4. **Cross-meeting coalescing & TRT embedding: DO NOT BUILD** (killed 19th + 20th pass).

---

## 🆕 DIAR SERVING v2 (one process, one CUDA context) — throughput-NEUTRAL: the diar ceiling is COMPUTE, not contexts (2026-06-12, NINETEENTH pass)

Three GPU runs, ≈ $0.36 total. Headline: **diar serving v2 — ONE warm pyannote process / ONE CUDA
context with 10 worker threads replacing the 10-process pool — is quality-IDENTICAL (DER 11.3 /
cpWER 31.1, to the digit) and throughput-NEUTRAL (~130× vs ~133–138× aggregate). The 18th pass's
"context time-slicing" theory is FALSIFIED: per-stream diar is a uniform ~14× under 10-way
concurrency in one context — exactly the fair share of a COMPUTE-saturated card (136× uncontended ≈
140× ÷ 10 streams). The ~140× diar aggregate on L40S is the pyannote pipeline's GPU compute
ceiling; no serving topology will move it.** Sub-$0.01 therefore needs the diar compute itself
made cheaper — fp16/TRT embedding engine + VAD — not another serving change.

**What shipped (all validated, kept as the `BENCH_DIAR_SERVER=v2` knob):**

- **`scripts/pyannote_diar_server_v2.py`** — same JSONL protocol as v1 but responses return OUT of
  order (id-tagged) as meetings finish; N worker threads, each owning a stock pipeline replica on
  its own CUDA stream; per-request compute IMPORTED from the v1 module (`load_waveform`, the
  emb-bf16 wrap, batching config) so the two servers cannot drift — which is why the DER gate came
  back identical to the digit. Replicas load STRICTLY serially (see "crash" below). Fake mode
  (`NOTO_PYANNOTE_V2_FAKE=1`) with deterministic staggered completion for $0 protocol tests.
- **`internal/platform/providers/diarize/pyannote_server_v2.go`** — `cuda-pyannote-server-v2`: all
  pool handles with one config share ONE warm process (package-level registry); Segment dispatches
  CONCURRENTLY (write lock + reader-goroutine demux by id — no per-engine request mutex); restart-
  on-crash with one retry; a caller's ctx cancel abandons only its own wait (the process is shared).
  Race-clean; 6 contract tests run the real script in fake mode (out-of-order demux, shared
  process, crash recovery).
- **Runner**: `BENCH_DIAR_SERVER=v2` mirrors `BENCH_STT_SERVER` (engine default, script swap,
  `NOTO_PYANNOTE_V2_WORKERS` defaulted to jobs, `BENCH_V2_DIAR_WORKERS`/`_STREAMS` forwards).

**Measured (same pinned 30-meeting / 11.44 h corpus, L40S, jobs=10, 8c/24 GiB, STT v2):**

| arm | WER | DER | cpWER | wall | $/audio-hr | util | peak VRAM |
|---|---|---|---|---|---|---|---|
| v1 baseline (`…7d3909`) | 23.9 | 11.3 | 31.5 | 311 s | **$0.01699** | 92% | 39.4 GB |
| STT v2 + diar v1 (`…625acc`) | 23.5 | 11.3 | 31.1 | 303 s | $0.01858 | 87% | 30.2 GB |
| STT v2 + **diar v2** (`…dfe7cc`) | **23.5** | **11.3** | **31.1** | 317 s | $0.01944 | 87% | **22.3 GB** |

The decisive number is per-stream diar: a UNIFORM 12–16×/stream across all 30 meetings (median
14×) — 10 streams × 14× ≈ 140× aggregate, the same card total as the v1 pool's 25×/engine at
4-way-effective concurrency and the same as one engine's 136× alone. One context with per-worker
streams behaves exactly like N contexts ⇒ **the GPU is genuinely saturated by diar compute**
(consistent with the 10th pass: bigger window batches didn't help because the embedding ResNet is
compute-bound). diar-v2's real value: **−8 GB peak VRAM** (one context, one CUDA-libs footprint)
and ONE host python/torch stack instead of 10 — headroom, not dollars. The +4% wall vs `625acc` is
the serial replica ramp (~10 s × 9 replicas of reduced capacity on a 5-min run) — amortizes away
on longer runs.

### Crash found & fixed (cost $0.09 to find)

**Never load pipeline replicas concurrently in one process.** The first 30-meeting attempt died
NATIVELY (no python traceback, stderr cut mid-line ~1 s after ready, on every restart — ~30
restart loops) at the exact moment all 9 extra workers entered `Pipeline.from_pretrained`
simultaneously. The 4-worker gate run (3 concurrent loads) had survived; 9-way died every time.
Prime suspect: concurrent `torch.load`/mmap off the FUSE-backed model volume in one address space
(the v1 pool only ever loads once per process — N concurrent loads across N address spaces are
fine). Fix: one loader thread builds replicas strictly one at a time while requests already flow
to the ready subset. The libavutil/torchcodec `OSError` noise in the stderr tail is ALWAYS present
(v1 too, import-time probe) — it was a red herring here exactly as in the MPS post-mortem.

### What's next, re-ranked (19th-pass truth: it's ALL diar compute now)

1. **fp16/TRT static embedding engine (L — the remaining unlock).** The wespeaker ResNet is 83–86%
   of diar infer and compute-bound; autocast emb-bf16 is already on, but a fused static engine
   (TensorRT or ONNX-CUDA fp16, swapped behind pyannote's embedding seam) is the one lever that
   reduces the FLOPs-seconds per audio-hour. If it nets ~1.7×, diar ≈ 240×; with STT v2 ≈
   $0.011–0.012/audio-hr. **Gate on DER like this pass** (fp16 embedding once broke DER 9→66 via
   exponent range; a static engine with calibrated dynamic ranges may behave differently than
   naive fp16 weights).
2. **VAD-gating (TODO-7, M)** — ~1.2× on AMI (×1.3–3 on real silence-heavy meetings); multiplies
   with #1. #1 + VAD ≈ $0.008–0.010 ⇒ **sub-1¢ in benchmark shape, comfortably sub-1¢ in
   production shape.**
3. **Right-size the container (S)** — at the same ~130–140×, ~5c/16 GiB recomputes to ≈ $0.0165;
   diar-v2's −8 GB VRAM also opens smaller/cheaper GPUs (L4 24 GB now fits the whole stack —
   measure $/audio-hr there).
4. **Cross-meeting window batch coalescing: DO NOT BUILD.** It was lever #1 in the 18th pass;
   the compute-bound measurement kills it — coalescing batches through a saturated card adds
   throughput only if batches are under-filled, and the 10th pass already showed they aren't.

### Infra fixed this pass

- `BENCH_DIAR_SERVER` knob + `BENCH_V2_DIAR_WORKERS`/`BENCH_V2_DIAR_STREAMS` forwards; workers
  default to jobs; v2 engine default `cuda-pyannote-server-v2`.
- Serial replica loading in the v2 server (the native-crash fix above).
- The concurrent Go client (id-demux, shared-process registry) is reusable for any future
  out-of-order warm server.

## 🆕 STT SERVING v2 (NeMo, truly batched) — quality-neutral, and it REFRAMES the wall (2026-06-12, EIGHTEENTH pass)

Six GPU runs, ≈ $2.09 total (of which $1.32 was ONE failed experiment — see "killed", MPS).
Headline: **a NeMo-backed batched STT server (same weights, same Go seam) is accuracy-NEUTRAL and
lifts warm per-stream STT from ~120× to 355–543×** — but $/audio-hr did NOT cross the v1 frontier
(best v2 run $0.0184 vs baseline $0.0170), because the 17th pass's "STT FLOP ceiling" framing was
WRONG: **the multiprocess pyannote diar pool is the real per-card ceiling (~135–140× aggregate on
L40S), and it was the binding constraint in the v1 baseline too.** Sub-$0.01 will come from
re-serving DIARIZATION the way STT just was, not from any further STT or topology tuning.

**What shipped (all validated):**

- **`scripts/parakeet_stt_server_v2.py`** — same JSONL protocol as v1, zero Go changes
  (`BENCH_STT_SERVER=v2` swaps the script). Loads `nvidia/parakeet-tdt-0.6b-v3` via NeMo
  (`.nemo` cached in the HF volume; staged during setup), decodes a `{"wavs": […]}` request as ONE
  padded batch (encoder AND label-looping TDT decoder batched — the leaderboard fast path the
  sherpa/ORT stack lacks). Emits ONE token per word (leading space), so `sherpaResult.words()`
  reconstructs exactly NeMo's word offsets; transcripts are NOT byte-identical to the CLI —
  the AMI A/B is the gate. Stereo inputs are header-checked + downmixed (sherpa's `read_wave`
  did this silently; NeMo crashes — AMI ES2010d is stereo). Protocol pinned for $0 by
  `TestParakeetServerV2Protocol` (runs the real script in `NOTO_PARAKEET_V2_FAKE=1` mode).
- **Pool topology for v2:** `BENCH_STT_POOL=1` + `NOTO_PARAKEET_CHUNK_BATCH=32` (one warm NeMo
  process serves every meeting; a whole meeting's ~18 chunks ride one batched request). Derived
  automatically by the runner when `BENCH_STT_SERVER=v2`.
- **Fail-fast CUDA guards** (both servers + `NOTO_PYANNOTE_DEVICE=<provider>` set by the runner):
  a GPU run now REFUSES to start an engine that can't get CUDA instead of silently decoding on
  CPU. Also `-timeout 110m` on every remote `go test` (the 10m default was an unexploded bomb).

**Measured — quality gate (30-meeting / 11.44 h pinned AMI corpus, fp32):**

| arm | WER | DER | cpWER | note |
|---|---|---|---|---|
| v1 baseline (R `…7d3909`) | 23.9 | 11.3 | 31.5 | $0.01699/audio-hr |
| **v2 fp32** (R `…625acc`) | **23.5** | **11.3** | **31.1** | equal-or-better everywhere |
| v2 bf16 autocast (4-mtg arm) | 25.7* | 13.9* | 32.8* | *4-meeting subset; == its fp32 twin (25.7/13.9/32.7) |

bf16 autocast on the NeMo encoder is **WER-safe** — this closes the fp16 saga: the ONNX export
derailment was the fp16 EXPONENT RANGE, and autocast-bf16 (fp32 weights, bf16 tensor-core math) is
the safe form, exactly as it was for the pyannote embedding. BUT it is **not faster** here (decode
is loop/launch-bound at this batch size, not FLOP-bound): its value is **VRAM** (peak 14.7 vs
19.4 GB on the 4-meeting arm), i.e. headroom for more diar engines. fp32 stays the v2 default.

**Measured — the cost sweep that reframed the problem (same 30 meetings, L40S):**

| run | topology | util | aggregate | $/audio-hr |
|---|---|---|---|---|
| v1 baseline | sttN=4 diarN=10, jobs=10, 4c/16 GiB | 92% | 133× | **$0.01699** |
| v2 R1 | sttN=1 diarN=12, jobs=12, 5c/21.6 GiB | 80% | 123× | $0.01927 |
| **v2 R3 (best)** | sttN=1 diarN=10, jobs=10, 8c/24 GiB | 87% | **138×** | $0.01839 |
| v2 R4 | sttN=1 diarN=10, **jobs=16**, 6c/20 GiB | 76% | 118× | $0.02032 |

Read the four rows together: with STT made ~free (per-stream 139× while sharing the card; warm
355–543× solo), aggregate moved only 133→138×. **The diar pool saturates the card at ~135–140×
aggregate regardless of what feeds it** — per-engine diar is ~25× under 10-way co-residency vs
136× uncontended, because 10 separate CUDA contexts TIME-SLICE the GPU (kernels from different
processes never overlap). More engines (R1), more in-flight meetings (R4), and more host cores
(R3 vs R1) all just redistribute the same ceiling.

### Levers KILLED this pass (measured, don't redo)

1. **CUDA MPS on Modal: DEAD, and it cost $1.32 to learn.** `nvidia-cuda-mps-control -d` starts,
   but no client can create a CUDA context (`torch.cuda.is_available()` → False everywhere). The
   failure was maximally deceptive: NeMo died with a misleading "abstract class ASRModel" error
   (broken CUDA poisons its target-class import) while pyannote SILENTLY fell back to CPU — the
   box ran 31 min of CPU diarization at 0% GPU util before failing. Hence the fail-fast guards
   above. Do not re-try MPS without first proving a context connects on a $0.10 single-meeting run.
2. **Pool oversubscription (jobs > diarN): WORSE.** jobs=16 over diarN=10 → per-stream diar 25×→14×,
   aggregate 138×→118×. The freed-engine-handoff win is real but smaller than the host-side cost of
   6 more in-flight streams (audio decode, chunk temp I/O, merge) on the same cores.
3. **bf16 STT for THROUGHPUT** — accuracy-safe (see above) but 0× speedup at batch 8; VRAM-only lever.
4. **More host cores as a throughput lever** — 8c/24 GiB beat 5c/21.6 by ~12% wall but ate the gain
   in container $/hr; at ~138× the diar contexts, not the host, are the constraint.

### What's next, re-ranked (the path to <$0.01 is now ALL diarization)

1. **Diar serving v2 — the STT-v2 move applied to pyannote (L, the unlock).** One warm process, ONE
   CUDA context, segmentation windows + embedding batches coalesced ACROSS meetings behind the same
   `SegmentEngine` seam, clustering per meeting on host. Kills the context time-slicing that caps
   the pool at ~140×. DER gate vs the pinned corpus, exactly like this pass's WER gate. Target:
   250–350× aggregate ⇒ **$0.0065–0.009/audio-hr on the same card** (with STT v2 already in place).
2. **TRT/ONNX fp16 wespeaker embedding export (L)** — still live (embeddings = 83–86% of diar
   infer); multiplies with #1, not an alternative to it.
3. **Right-size the v2 container (S)** — R3's 8c/24 GiB was a diagnosis shape; ~5c/16 GiB at the
   same 138× recomputes to ≈ $0.0164 (below the v1 frontier even pre-diar-v2). One cheap run.
4. **VAD-gating (TODO-7)** — unchanged: ~1.2× on AMI, the #1 PRODUCTION lever on real (30–70%
   silence) meetings; multiplies with everything above.

### Infra fixed this pass

- `BENCH_STT_SERVER` knob + auto-derived v2 pool/chunk-batch defaults; `BENCH_V2_PRECISION` /
  `BENCH_V2_BATCH` forwards; `.nemo` pre-staged to the model volume during setup.
- `NEMO_CACHE_DIR` is container-LOCAL (`/tmp/nemo`) — never share a mutable extraction cache
  between concurrently-committing runs.
- Stereo/multi-channel inputs downmixed in the v2 server (ES2010d failed v2 R1 with a shape error).
- Fail-fast CUDA guards (servers + runner `NOTO_PYANNOTE_DEVICE`), `go test -timeout 110m`.

## 🆕 STAGE-DECOUPLED POOLS LAND IT — measured on Modal (2026-06-11, SIXTEENTH pass)

Six GPU runs, ~$0.55 total. Headline: **$0.0257 → $0.0197/audio-hr (−23%) with NO precision
change and NO accuracy cost** — fp32, one L40S, GPU mean util 69 → 90%. The two moves that did it:
**stage-decoupled engine pools** (the fifteenth pass's half-finished refactor, completed: a meeting
borrows an STT engine only during STT and a diar engine only during diar, pools sized independently)
and **container right-sizing for the pool shape** (8 vCPU / 28 GiB — pools spawn sttN+diarN
processes, not 2×jobs, so the 4 GiB×jobs auto-size over-bought ~32% of the bill).

**The recommended throughput invocation (validated):**

```
BENCH_STT_POOL=4 BENCH_DIAR_POOL=10 BENCH_CPU=8 BENCH_MEM_GIB=28 \
  .venv-modal/bin/python scripts/modal_benchmark.py run --suite ami --profile prod --jobs 10 --gpu L40S
```

| | jobs=6 paired (15th) | jobs=10 pools 4/10 (R3) | + right-size (R6, the default) |
|---|---|---|---|
| GPU mean util | 69.3% | **90.0%** | **90.2%** |
| $/audio-hr | $0.0257 | $0.0218 | **$0.0197** |
| aggregate throughput | ~100× | **130×** | 130× |
| peak VRAM | 42.3 GB (6 streams) | 40.3 GB (**10 streams**) | 40.4 GB |
| WER / DER / cpWER | 21.9 / — / — | 21.9 / 10.3 / 29.3 | **21.9 / 10.3 / 29.3** (identical) |

(R3 = `20260611T205527Z-cbf69f`, R5 = `20260611T210045Z-9e5cad` (+seg-bf16, DER-regressed, superseded),
R6 = `20260611T210534Z-fecce3` — the confirmed recommended config. Sixteenth-pass Modal spend ≈ $0.64.)

Pool math from the R3 stage walls: the 4-engine STT pool ran **97% busy** (822 s busy of 849 s
capacity) delivering 4×33× = **132× aggregate — AT the card's measured STT FLOP ceiling** — while
the 10-engine diar pool sat at 57%. So jobs=10 / sttN=4 / diarN=10 is the fp32 frontier on a 46 GB
L40S: the card is now **compute-bound at ~90% mean util**, not idle-bound. Don't raise sttN (the
FLOPs are spent); diarN could drop to ~8 to shave VRAM if needed.

### Levers KILLED this pass (measured, don't redo)

1. **fp16 STT is broken IN THE ENCODER, not the decoder.** Encoder-only fp16 (decoder/joiner fp32
   passthrough — `PARAKEET_FP16_ROLES=encoder`, now the converter default, with a
   `conversion.marker` so volume-cached exports rebuild when the recipe changes) still derails
   ES2002a to the **identical 188.7% WER**, and exposed a SECOND derailed meeting (ES2002d, 103%).
   The "long autoregressive decode accumulates fp16 error" theory is dead — the encoder's fp16
   activations themselves blow up on some meetings, deterministically. Widening the op block list
   is a paid trial-and-error loop with no convergence guarantee; **fp16 is parked**. (And the pools
   result removes its motivation: VRAM headroom came from pooling instead.)
2. **Within-meeting STT chunk batching: no win, 3× VRAM.** `NOTO_PARAKEET_CHUNK_BATCH=8` feeds 8 of
   a meeting's 120 s chunks through ONE `decode_streams` call (knob kept, default off; runner
   passthrough `BENCH_STT_CHUNK_BATCH`). Measured jobs=1 A/B (ES2005c+ES2007a): accuracy identical
   (WER 24.2 both, word counts ±1), STT wall NOT faster (23.9 s → 26.8 s on ES2005c), peak VRAM
   **6.7 → 22.1 GB**. A 120 s chunk already saturates the encoder — third confirmation (after e2e
   request batching and pyannote window batching) that **this stack is not launch-bound; batching
   only multiplies activation memory.**
3. **Segmentation bf16: fails the quality gate.** `NOTO_PYANNOTE_SEG_AMP=bf16` (wrap landed in
   `pyannote_diar_server.py`, opt-in) cut diar walls ~9% but **DER 10.3 → 11.5 (+1.2)** on the same
   20 meetings — and the wall didn't move anyway (STT-pool-bound). Unlike the embedding, the
   segmentation output feeds threshold/counting decisions, so precision there is NOT free. Stays a
   diagnostic knob; do not default.
4. **Local clustering offload / "CPU tail" theory: dead on AMI.** Per-stage server timing on real
   AMI meetings (forwarded via `BENCH_PYANNOTE_SERVER_STDERR=1`): embeddings **83–86%** of diar
   infer, segmentation ~11%, clustering (`discrete_diarization`) **~1.5%** (~0.6 s per 35-min
   meeting). Shipping embeddings home to cluster locally would save nothing; the GPU idle the
   fifteenth pass attributed to "the clustering tail" was actually engine warmup + per-stream
   serial gaps, which deeper in-flight concurrency (pools) fills.
5. **Heterogeneous stage-split GPUs (STT card + diar card): analyzed, dominated.** The combined run
   beats the serial-sum bound (~65×) at ~130× precisely because BOTH stage types co-resident raise
   SM occupancy; splitting them onto two cards forfeits that overlap and adds double audio staging.
   Intra-box stage pools capture the same VRAM benefit without renting a second card.

### Infra fixed this pass

- `run_id` now carries a random suffix — two runs launched in the same wall-clock second used to
  get the SAME id and overwrite each other's results dir on the shared volume (cost one wasted A/B).
- `BENCH_CPU` / `BENCH_MEM_GIB` explicit container-shape overrides in the runner (the pool shape
  invalidates the 4 GiB×jobs auto-size; summary still records what was provisioned).
- The fp16 export dir is recipe-stamped (`conversion.marker`) and auto-rebuilds on mismatch.

### What's left, re-ranked after this pass

1. **Validated TRT/ONNX fp16 export of the wespeaker embedding ResNet** — embeddings are 83–86% of
   diar; a ~2× there frees real diar capacity (unlike seg-bf16). The bf16 autocast already banked
   ~2×; this is the next ~1.5–2× on the same bucket, with a DER gate like the bf16 one. (L)
2. **VAD-gating (TODO-7)** — still the #1 PRODUCTION lever (real meetings are 30–70% silence; AMI
   ~19% caps it at ~1.2× there, which is why this benchmark can't see it). (M)
3. **Container/CPU trim beyond 8 cores, diarN→8** — small (~5%) cost shavings now that CPU+mem are
   ~30% of the bill at 90% GPU util. (S)
4. **jobs>10 needs VRAM from somewhere** — either the TRT embedding export (smaller diar procs) or
   a future safe reduced-precision STT. At fp32 the 46 GB card is full at 10 streams + 4 STT engines.

### Production (single-meeting) status after this pass

jobs=1 prod on L40S, fp32, warm engines: a **38-min meeting lands in ~26 s** (max(STT 23.9 s,
diar 25.4 s) — the two branches are balanced within ~6%), ≈ **88× realtime**, ~$0.024–0.028/audio-hr
at the prod container shape. Chunk batching (no win) and fp16 (broken) don't move it; the remaining
single-meeting levers are the TRT embedding export (#1 above), VAD on real-world audio, and the
documented L40S-for-latency / L4-for-cost GPU choice. For multi-tenant production (many meetings in
flight), the pooled benchmark shape IS the serving recipe: 10 concurrent meetings on one L40S at
90% util and ~$0.02/audio-hr.

## 🆕 SMART SCHEDULER + AUTO-FILL — measured on Modal (2026-06-11, FIFTEENTH pass)

Two real GPU runs this pass (fp16 A/B, then scheduler validation). The headline lesson:
**at jobs=4 the L40S was only ~64 % utilized — throughput was idle-bound, not compute-bound — so
precision (fp16) barely moved the wall clock. Concurrency + scheduling is the lever; fp16's value is
the VRAM it frees to ENABLE more concurrency, not its kernels.**

**Shipped (validated):**
- **Smart scheduler** (`bench.RunMeetings`): replaced the lock-step `t.Parallel`+semaphore (all streams
  start together → all hit the CPU clustering tail together → GPU idles) with a **work-stealing pool over
  an LPT-ordered queue** — `jobs` workers each pull the next meeting the instant they free up, longest
  meetings first (minimizes makespan / stranded idle workers). Per-meeting subtest isolation + input-order
  results preserved. Race-clean. Tests in `bench_test.go`.
- **Auto-fill** (`modal_benchmark.py`): with no pinned `BENCH_AMI_MEETINGS`, fetches & scores
  `jobs × BENCH_QUEUE_DEPTH` (default 2) meetings from the dataset manifest, so the scheduler always has a
  backlog deeper than the worker count to stagger.

**Measured (fp32, L40S, auto-filled 12 meetings, jobs=6):**

| | fp32 jobs=4 (lock-step) | fp32 jobs=6 (work-stealing+auto-fill) |
|---|---|---|
| GPU **mean** util | 63.6 %* | **69.3 %** |
| audio processed | 1.10 h | **4.65 h** |
| **$/audio-hr** | $0.0279 | **$0.0257** ✓ |
| WER | 20.1 % | 21.9 % (clean — no regression) |
| GPU peak mem | 27.7 GB | **42.3 / 46 GB (VRAM-capped!)** |

*(*64 % is from the fp16 jobs=4 run; the scheduler lifted it to 69 %.*)

**The wall now: fp32 jobs=6 is VRAM-capped at ~6 streams on L40S (42/46 GB), and even there the GPU is 30 %
idle** — the idle is the per-meeting CPU/diar clustering tail, and hiding it needs MORE concurrent streams
than fp32 VRAM allows. **This is exactly what fp16 unlocks: halved weights → ~12 streams on the same card
→ GPU-phase streams always cover the CPU-phase ones → mean util → ~90 %+.** So the precision lever and the
concurrency lever are the SAME lever: fp16-for-VRAM + jobs=12.

**BUT fp16 has a GPU accuracy regression to fix first (the FOURTEENTH-pass CPU validation could not catch
it — CPU EP upcasts fp16→fp32):** on real GPU fp16 kernels, **ES2002a derails to 188 % WER** (3/4 meetings
stay bit-identical to fp32). The long autoregressive transducer decode accumulates fp16 error and collapses
on that one meeting. Fix options: widen the fp32 `op_block_list` (PARAKEET_FP16_BLOCK) until ES2002a is
stable, or bf16 (fp32 exponent range — needs CUDA-EP support check). Until hardened, fp16 is **not
shippable** — but it's the unlock for filling the GPU.

**Next-lever ranking (updated by measurement):** 1) harden fp16 → run jobs=12 to fill the 30 % idle
(the path to ~$0.018); 2) fp16/bf16 diar segmentation (the diar branch is now the SLOWER one: STT 21× vs
DIAR 18×, and its clustering tail is the idle); 3) VAD. Strict <$0.01 is a stack of all three.

## 🆕 fp16 VALIDATED + $/audio-hr metric (2026-06-11, FOURTEENTH pass — all $0, no GPU)

Done entirely on CPU against cached fp32 weights + real AMI audio (`benchmark/identity/ami/ES2002a.wav`):

1. **The committed fp16 conversion script was BROKEN — caught before it cost a paid run.** A naive
   `convert_float_to_float16()` (even with file-based shape inference) produces an encoder that
   **crashes at model LOAD on every provider** (CPU and CUDA alike):
   `Type Error: Optype (Add) bound to different types (float and float16) in /pre_encode/Add`.
   Root cause: the encoder's int64 `length` input drives a small scalar chain (Cast→Add→Div→Floor…)
   that computes the sub-sampled output length; the converter turns those float *constants* to fp16
   but leaves the int→float Cast fp32, so the Add mixes types. **Fix** (now in
   `scripts/convert_parakeet_fp16.py`): (a) `infer_shapes_path` (file-based, >2 GB-safe) instead of
   `disable_shape_infer`; (b) `node_block_list` = every node reachable from an int input but not a
   float input (the "length island": 29 enc / 4 dec / 0 join nodes) kept fp32 — zero-FLOP scalar math,
   and fp16 can't represent lengths >2048 anyway; (c) strip stale `value_info` so ORT re-infers clean.
2. **fp16 accuracy = fp32 accuracy.** Fixed export decodes 180 s of ES2002a **bit-for-bit identically**
   to fp32 — `0.000 %` word divergence (308/308 words). The "accuracy risk" the TWELFTH pass flagged
   as the open question is **closed**: fp16 STT is numerically free on this model. (Even on CPU, which
   upcasts fp16, it was *faster*: RTF 0.159 vs 0.191 — memory-bandwidth win; GPU native kernels = more.)
3. **`$/audio-hr` is now actually computed.** The runner only ever recorded whole-run
   `estimated_cost_usd`; `hours`/`requested_audio_hours` are `0.0` (="whole corpus") so there was **no
   normalized headline number at all.** Added `processed_audio_hours` (summed from hyp word/turn end
   times) + `cost_per_audio_hour_usd` to the summary + a headline print. **Best run to date recomputes
   to `$0.02790/audio-hr`** (L40S, jobs=4, fp32) → **need 2.79× to reach <$0.01.**
4. **GPU "100% util" was peak-only, which hides idle gaps.** Added `gpu_mean_utilization_pct` +
   `gpu_busy_pct` (% samples ≥50%) per command and run-wide. Peak 100% can sit on a 60% mean if the GPU
   stalls between STT/diar phases or during host audio-decode — mean≪peak is exactly the throughput
   left on the table. The next run will *show* it instead of us guessing.

**Throughput synergy to exploit next:** fp16 **halves the encoder VRAM (2.4→1.2 GB/job)**, so the same
L40S (27.7/46 GB at fp32 jobs=4) fits **more concurrent meetings** — more jobs in flight keeps a
meeting always in the GPU's STT *or* diar phase, pulling mean util toward peak. So the fp16 run should
also **raise `--jobs` (try 6)** and read the new `gpu_mean_utilization_pct`/`gpu_busy_pct` to confirm
the card stays fed (and `cost_per_audio_hour_usd` to confirm the cut). One command tests precision,
concurrency, container right-size, and the new metrics at once:

```
BENCH_STT_PRECISION=fp16 BENCH_SUITE=ami BENCH_PROFILE=prod BENCH_JOBS=6 \
BENCH_GPU=L40S BENCH_SEED=1 make model-benchmark
```

Honest math: fp16 (~1.5–2×) + filling idle gaps (mean→peak) + right-size is plausibly the 2.79× to
cross <$0.01, but it's a STACK — if it lands ~$0.013–0.016, the next cut is fp16/bf16 diar
segmentation (same technique, the other GPU branch). The accuracy half is already banked (0.000%).

## 🆕 AMI TRACK — real-meeting benchmark: findings + next levers (2026-06-11, TWELFTH pass)

**Everything above/below this section is the SYNTHETIC track.** We have switched the primary
benchmark to the **real AMI Meeting Corpus** (`benchmark/e2e/ami_test.go`, capture/score split:
the GPU only does inference and streams tiny hyp JSON back; WER/DER/cpWER are scored LOCALLY off
the references, for free). Read THIS section first — the cost/accuracy reality on real audio is
different from the dense synthetic corpus, and the next big levers are different too.

### What was done this pass (all landed)

- **Pre-staged all 43 AMI wavs to the bench volume** (`modal volume put noto-benchmark-cache
  benchmark/identity/ami identity/ami`). AMI uses the persistent volume exactly like the synthetic
  corpus (symlinked, `modal_benchmark.py:prepare_layout`), so **no GPU run ever cold-downloads
  again**. The infamous "146 s fetch on the GPU" in the first AMI runs was a **one-time cold-cache
  artifact** (the volume was empty for those meetings), NOT a per-run cost — `fetch.py` is
  idempotent ("wav present" → skip). Steady-state AMI runs never pay it.
- **Per-worker warm-engine pool in `TestAMICapture`** (`benchmark/e2e/ami_test.go`): meetings now
  run concurrently up to `-jobs`, each on its **own** Parakeet + pyannote process. A single shared
  warm server serializes every request at its mutex (`e.mu`), so `-jobs` alone overlapped nothing;
  independent processes let meeting B's GPU embedding run during meeting A's CPU clustering tail.
  **Byte-identical scores** vs `-jobs=1` (verified: WER 22.1 / DER 10.4 / cpWER 28.8 on both) — it
  is a pure GPU-packing change, accuracy-neutral.
- **AMI container auto-sizes with `-jobs`** (`modal_benchmark.py:launch`): the pool holds `eff_jobs`
  process pairs, which prod's 4 vCPU / 8 GiB can't host, so cores+RAM scale with the concurrency
  (`2*jobs` cores, `6 GiB*jobs`). GPU dominates the bill, so the extra CPU/RAM is nearly free. Other
  suites keep the profile shape (they share one warm server per engine).

### Measured baseline on REAL AMI (L40S, fp32, prod, both warm servers)

- **Accuracy is HEALTHY and real** (vs the overlap-inflated synthetic 45 % WER): WER **20–22 %**,
  DER **10–13 %**, cpWER **24–29 %** — in line with published AMI numbers for a 0.6 B model. The
  gap between cpWER and WER (the **attribution tax, +4 to +7 pts**) is the diarization/merge
  mismatch, NOT STT — that is the main accuracy lever (see below).
- **Per-stage speed, jobs=1 uncontended:** **STT 121×, DIAR 136×** realtime. Both stages are fast;
  the earlier "diar 50× vs STT 100×" was the **first-meeting pipeline warmup** (~12 s on the first
  diar call — kernel/cuDNN autotune), which amortizes to noise over a real corpus. Warm, diar is
  *faster* than STT.
- **Cost (warm cache, inference only): ~$0.020–0.022/audio-hr** — already **6–18× under AssemblyAI
  retail** ($0.12–0.37) single-stream, and below the research-estimated $0.03–0.07 floor.
- **AMI silence fraction ≈ 19 % avg** (computed from the reference RTTMs; the short 'a' opener
  sessions are heavier, 27–52 %). This bounds the VAD win on AMI; real production meetings are
  30–70 % silence, where VAD is much larger.

### Why GPU-jobs is NOT the big lever (the concurrency ceiling)

Clean A/B on the SAME 3.87 h, 8-meeting sample (seed 1):

| jobs | wall | throughput | $/audio-hr | container | scores |
|---|---:|---:|---:|---|---|
| 1 | 142.3 s | 98× | $0.0225 | 4 vCPU / 8 GiB | 22.1 / 10.4 / 28.8 |
| 4 | 111.4 s | **125×** | **$0.0203** | 8 vCPU / 24 GiB | 22.1 / 10.4 / 28.8 |

Concurrency buys **~28 % wall but only ~10 % $/audio-hr** (the bigger container eats the rest), and
throughput **tops out ~125× with the GPU at 100 % util**. We are now **GPU-COMPUTE-bound**, not
idle-bound — more jobs just queue at the card's FLOP ceiling. VRAM also caps the pool (~6 process
pairs on a 48 GB L40S; fp32 STT is ~5 GB each). **Verdict: concurrency is a ~1.1–1.3× lever, near
its ceiling. To go meaningfully cheaper we must reduce GPU FLOPs or change the GPU/components.**

### The BIGGER levers (push cost DOWN and accuracy UP) — ranked

| # | lever | $ effect | accuracy effect | effort | why it's bigger than jobs |
|---|---|---|---|---|---|
| **1** | **fp16 / TensorRT STT export (TODO-5)** | **~2–3×** | neutral/＋ | **L** | STT is the dominant GPU stage; we run fp32 (int8 is a CPU-only opt). A validated fp16/TensorRT Parakeet export cuts the biggest FLOP bucket — **the lever that actually crosses <$0.01**. Upstream fp16 HF repo is an empty stub → needs a real ONNX/TRT export + WER validation. |
| **2** | **VAD-gating front pass (TODO-7)** | 1.25× on AMI, **1.5–3× in prod** | **＋ (less ASR hallucination)** | M | Skip silence before BOTH STT and diar. Small on dense AMI (19 % silence) but the #1 PRODUCTION lever (real meetings 30–70 % silence) AND it improves WER + cuts diar embeddings. Silero VAD is bundled with sherpa-onnx; run it once per meeting, feed only speech spans. |
| ✗3 | ~~Bigger/faster GPU~~ | **WORSE** | neutral | S | **Measured (below): NO.** H100 is the SAME wall as L40S (72.4 vs 72.7 s) at 2× cost — the models are too small to use its FLOPs; jobs=1 is fixed-overhead-bound, not FLOP-bound. L4 ≈ cheapest, L40S = fast iteration, A100/H100 dominated. Bigger GPU is a cost driver, not a lever. |
| **4** | **Long-form Parakeet, delete 120 s chunking (TODO-10)** | 1.1–1.2× | **＋ (no chunk-boundary errors)** | M | v3 does local attention to ~3 h in one pass; removes chunk stitch + per-chunk overlap recompute AND the boundary word errors. |
| **5** | **Enrolled-speaker attribution (TODO-8)** | diar ↓ | **＋ (supervised > clustering)** | M | For known rosters, skip unsupervised clustering: embed VAD spans, cosine-match enrolled centroids. noto already ships the ECAPA embedder + matching. Cheaper AND more accurate; closes the attribution tax for recurring teams. |
| **6** | **Smaller representative subsample (workflow)** | iteration $ ↓ | — | S | For cheap TDD, run a fixed diverse mini-set (1 'a' opener + 1 full session from each of ES/IS/TS = ~6 meetings, ~1.5 h) instead of a fresh `-hours` draw, so each iteration is ~$0.01–0.02 and comparable run-to-run. References already scored off-GPU for free. |

### Accuracy levers specifically (the "accuracy up on subsample" ask)

- **Attribution tax (cpWER − WER, +4 to +7 pts) is the top accuracy target** — it's pure
  diarization/merge, STT is already at ~20 % WER. Improve (a) `merge.Attribute` word→speaker
  assignment at turn boundaries, (b) DER via better speaker counting / clustering, (c) VAD to clean
  segment edges. Enrolled-speaker (TODO-8) collapses it for known teams.
- **VAD → WER down** (kills hallucinated tokens over silence) AND **DER down** (cleaner edges).
- We currently pass the **oracle speaker count** (from the RTTM) to match the synthetic methodology;
  production won't have it, so speaker-counting robustness is a real accuracy item once VAD lands.

### GPU sweep — does a bigger GPU pay now? (AMI, 1.79 h sample, jobs=1, fp32)

| GPU | $/hr (gpu+4c+8g) | wall | throughput | **$/audio-hr** | GPU peak mem |
|---|---|---:|---:|---:|---:|
| **L4** | $1.06 | 147.8 s | 44× | **$0.0242** | 7.2 GB |
| **L40S** | $2.21 | 72.7 s | 89× | $0.0248 | 7.8 GB |
| A100-40GB | $2.36 | 80.0 s | 81× | $0.0293 | 6.7 GB |
| H100 | $4.20 | 72.4 s | 89× | $0.0472 | 7.0 GB |

**Verdict: bigger GPUs still do NOT pay — but the REASON changed, and it's the key insight.**
**H100 is byte-for-byte as slow as L40S (72.4 s vs 72.7 s)** at 2× the price, and A100 is slower
*and* pricier. The 0.6 B Parakeet + pyannote models are far too small to use an H100's FLOPs, so at
**jobs=1 the wall is bound by per-meeting FIXED OVERHEAD** (model warmup, CUDA/IPC, the CPU
clustering tail, chunk stitching), **not GPU compute** — and a faster card cannot accelerate
overhead. Peak VRAM is only ~7 GB everywhere, so the big cards' extra memory buys nothing at
jobs=1 either. **Use L4 for the cheapest $/audio-hr (≈ tied with L40S); L40S for fast iteration
(2× the wall speed at ≈ the same cost); never A100/H100 for this stack.**

**This reframes the whole problem.** Two regimes:

- **Single-stream (jobs=1) is FIXED-OVERHEAD-bound, not FLOP-bound** (proof: H100 = L40S). The
  levers here are *amortize/remove overhead*: run a longer corpus (warmup → noise), pre-warm the
  servers, fewer+larger chunks (long-form Parakeet, TODO-10), and **VAD to shrink the work itself**.
  A faster STT kernel does almost nothing here.
- **Concurrent (jobs≥4) approaches FLOP-bound** (~125× ceiling, GPU 100 % util — see the table
  above). Only HERE does a faster STT kernel (fp16/TensorRT, TODO-5) convert to throughput, and
  only here would a bigger-VRAM card help by hosting a larger pool — but H100's 2× price still kills
  the $/audio-hr unless it delivers >2× the concurrent throughput, which it won't (it isn't even
  faster single-stream).

So the cheapest path to **<$0.01/audio-hr** is the STACK: **VAD (shrink the work) → concurrency to
fill the idle GPU (done, ~125×) → fp16/TensorRT STT to lift the FLOP ceiling**, on **L4 or L40S**.
Not a bigger GPU.

### 🆕 Free local accuracy lab — what's actually recoverable (THIRTEENTH pass, 2026-06-11)

The captured hyp JSON already carries **raw STT word timings + raw diarization turns**, so the
merge/attribution step can be re-run and re-scored **entirely off-GPU, for $0**
(`benchmark/e2e/ami_attribution_test.go`, `TestAMIAttributionLab`; dev set = 13 cached fp32
meetings, 4.66 h, in `/tmp/ami_dev/hyps`). This let us measure the accuracy ceiling **before**
spending a single GPU-minute — and it **redirects the accuracy strategy** the section above assumed.

cpWER by attribution strategy (overall, 13 meetings, WER **21.6 %**):

| strategy | cpWER | tax vs WER | vs current | what it is |
|---|---:|---:|---:|---|
| baked / midpoint | 27.9 % | +6.3 | — | current production `merge.Attribute` |
| max-overlap primary | 27.8 % | +6.1 | **−0.2** | overlap, not midpoint, as the primary rule |
| overlap + 1-word smoothing | 28.0 % | +6.4 | +0.1 | collapse single-word speaker islands |
| **ORACLE (perfect per-word speaker)** | **26.3 %** | **+4.7** | **−1.6** | assign each hyp word to the RTTM speaker — the floor |

**Verdict — the merge algorithm is a DEAD END (≤0.2 pts), and this corrects lever-table row #1's
accuracy framing.** Even *perfect* attribution recovers only **1.6 of the 6.3-pt tax**; realistic
merge changes recover ≤0.2. The remaining **+4.7 pts is structural, not mis-attribution**:

- **Speaker count is always correct** (hyp = ref = 4 everywhere — we feed the oracle count), so the
  tax is not a diarization-count error.
- **The oracle residual tracks overlapped speech almost perfectly**: low-overlap meetings
  (ES2012a 3 %, IS1008a 4 %, TS3010a 5 %) carry only +1.6–2.1 pts; high-overlap (ES2010d 19 %,
  IS1007d 16 %) carry +8.4–8.6 pts. A single Mix-Headset stream physically cannot transcribe two
  simultaneous talkers, and cpWER charges the lost words to BOTH speakers. **Unrecoverable** without
  multi-channel / target-speaker ASR (out of scope).

**VAD's accuracy benefit on AMI is ~0, too:** only **2 % of hyp words** land in reference silence
(0.25 s collar), and most of those are real words near RTTM boundaries, not hallucinations — AMI is
dense, near-continuous discussion. VAD stays a **compute** lever here (~1.2× on AMI; the WER/silence
win is a *production* phenomenon, not an AMI one).

**So the only accuracy lever that moves the needle is WER itself (the 21.6 % foundation)** — every
point of WER is a point of cpWER. "Error corrections / confidences" must target WER (LM/domain
correction of the transcript), NOT the merge. But any correction that calls an LLM **adds cost**,
fighting the <$0.01 goal — so on this benchmark, accuracy and cost both point back to the **STT
engine**, not the post-processing.

### Sharpened next lever (what to push now)

Levers eliminated this session, all for $0: ❌ merge tweaks (≤0.2 pts), ❌ perfect turns (≤1.6 pts,
and we already feed oracle count), ❌ VAD-for-accuracy on AMI (2 % silence words), ❌ int8 on GPU
(re-confirmed: fp32 is 3.6× faster — int8 is CPU-only), ❌ bigger GPU (H100 = L40S wall). What's left,
ranked by ROI toward <$0.01:

1. **fp16 STT (the linchpin).** STT is the *slower* of the two parallel GPU branches (121× vs diar
   136× warm) and runs fp32. Unlike int8, the ONNX-Runtime **CUDA EP has real fp16 kernels**, so an
   fp16 Parakeet export should give ~1.5–2× STT throughput — the only lever that crosses <$0.01 under
   concurrency. **UPDATE (FOURTEENTH pass): export built, fixed, and accuracy-validated for $0 — fp16 =
   fp32 bit-for-bit (0.000 % divergence on AMI). Accuracy risk is CLOSED.** Only the GPU
   throughput/$ number is still unmeasured (needs one paid run). See the FOURTEENTH-pass section up top.
2. **Right-size the concurrency container (free tightening).** The jobs=4 auto-size (`2*jobs` cores,
   `6 GiB*jobs`) raised the container 14.5 % and ate ~half the throughput gain (concurrency netted
   only −10 % $/audio-hr vs +28 % wall). GPU is ~77 % of the bill at the FLOP ceiling; CPU is used
   only by the bursty clustering tail (STT/embedding are on-GPU), so cores can likely drop toward
   `jobs+2` and RAM toward `3–4 GiB*jobs`. Needs one measurement run to confirm no OOM / clustering
   starvation — fold it into the fp16 validation run.
3. **VAD compute-gating** — still the top *production* lever (30–70 % silence there), ~1.2× on AMI.

---

## OPEN TODOs — start here if you're taking over (2026-06-10)

Everything below is open; everything else in this doc is done (see the dated "Done" sections).
Read ".docs/benchmarks.md → Cost levers" and "Researched 2026-06-10" (below) before starting.
Current state (2026-06-10, EIGHTH pass — one-GPU benchmark sweep + corrected default):
`make model-benchmark` now defaults to **quick e2e only** (`BENCH_QUICK=1`) on **one L40S**,
`prod` profile, fp32 STT, and both warm servers. The current measured routine benchmark on the
rebuilt 10-meeting synthetic corpus (`BENCH_SYNTH_MEETINGS=10`, `BENCH_HOURS=1`, seed 1 selects
9/10 meetings = **~0.94 h audio**) is **~55.6 s command wall / $0.034** with KPIs **WER 45.2 /
DER 9.0 / cpWER 48.1** and peak VRAM **8.4 GB**. Running the old full suite on the same corpus
repeats STT+diar atomically and then again in e2e: **111.0 s / $0.068**. So the safe recurring win
is **no double-pass: ~2× cheaper/faster routine runs**. Cross-meeting e2e batching was scaffolded
(`BENCH_E2E_BATCH=1`, `BENCH_BATCH_SIZE=N`) but the measured implementation is **not a win yet**:
batch=4 was only ~4 % faster than batch=1 (52.2 s vs 54.7 s command wall repeat) while spiking VRAM
to **39.1 GB**; batch=2 and batch=8 were slower. Root cause: STT can use sherpa `decode_streams`,
but pyannote still effectively processes the wav list one file at a time, and diarization dominates.
Keep cross-meeting e2e batching **experimental opt-in**, not the default, until diarization is truly
batched or replaced.

Current decision (2026-06-10, NINTH pass — runtime-only scope clarification): do **not** spend the
next iteration on warm endpoints, persistent deployed startup paths, more GPUs, or a Sortformer fast
path that changes the speaker-count contract. Those can be valid product/serving levers later, but
they do not answer the current question: **how do we reduce actual one-GPU runtime for the same
audio and same quality target?** The next implementation should therefore instrument pyannote
diarization internals, measure where the 41.8 s atomic diar wall actually goes, and then optimize
the dominant runtime stage while keeping the same pyannote model/quality gate. The expected
runtime-only prize is conservative **10–20 %**, good **25–40 %**, with a target quick-e2e wall of
**35–45 s** for the current ~0.94 h corpus if diarization can be made truly batched or its CPU tail
can be shortened.

Current state (2026-06-10, TENTH pass — diarization PROFILED; two tempting levers ruled out without
a quality compromise): the ninth pass's #1 ask is **done** — `scripts/pyannote_diar_server.py` now
has per-request, per-stage timing (load / segmentation / speaker_counting / embeddings / clustering /
serialize), forwarded to benchmark output under `NOTO_PYANNOTE_SERVER_STDERR=1` (gated; off by
default; the Go engine `io.MultiWriter`s the server stderr). It immediately localized the diar cost:
on the ~0.94 h corpus, per meeting diar infer ≈ **4.0 s, of which embeddings are ~3.9 s (~93 %)**;
segmentation ~0.18 s (~4 %), clustering ~0.11 s (~3 %), speaker_counting/load/serialize ≈ 0. **The
diar critical path is the embedding ResNet, and it is COMPUTE-bound** (GPU saturated at fp32). Two
levers were then measured and **rejected because they fail the quality gate or do nothing**:
(1) **window batch size is NOT the lever.** Forcing pyannote segmentation+embedding batch 32→128
processed identically (output unchanged, DER 9.0) but gave **no speedup** (e2e 55.6 s → 60.5 s, i.e.
not faster) and **doubled VRAM (8.4 → 15.2 GB)** — embeddings are FLOP-bound, not launch-bound, so a
bigger batch just over-allocates. Knob kept (`NOTO_PYANNOTE_SEG_BATCH`/`_EMB_BATCH`), default **0 =
pyannote's own default**.
(2) **reduced precision on the embedding is not shippable on this pyannote build.** fp16 (autocast,
embedding-only AND whole-pipeline give the **byte-identical** bad result) ~halved embeddings (3.9 →
1.9 s, e2e → 44.5 s, VRAM 7.4 GB) but **wrecked DER 9 → 66 / cpWER 48 → 113** — fp16's narrow exponent
range degenerates the embedding geometry → mis-clustering. **bf16** keeps fp32's range but **errors
inside pyannote** (it converts the embedding tensor to numpy, which has no bfloat16: "unsupported
ScalarType BFloat16"). Knob kept (`NOTO_PYANNOTE_FP16=emb|emb-fp16|all|all-fp16`), default **0 = off**.
Net: the shipped default is unchanged-and-clean (DER 9.0, ~55 s, 8.4 GB) PLUS the new instrumentation;
**no regression shipped, no net single-stream speedup found** — the embedding stage is effectively
fp32-locked through pyannote's standard API. The next quality-safe diar lever is therefore NOT a flag:
it is (a) a **validated low-precision embedding export** (TensorRT/ONNX fp16 of just the wespeaker
ResNet, checked against the fp32 embeddings, not naive autocast), (b) **fewer embeddings** via VAD /
enrolled-speaker on real audio (TODO-7/8 — invisible on this dense synthetic corpus), or (c)
**cross-stream concurrency** (TODO-9) to amortize the fixed embedding FLOPs across meetings (throughput,
not single-stream). Tenth-pass Modal spend ≈ $0.16 (4 runs).

Current state (2026-06-11, ELEVENTH pass — the quality-safe diar speedup is LANDED): the tenth pass's
"bf16 is unshippable" verdict was WRONG, and the dominant single-stream diar lever is now SHIPPED as
the default. Root cause of the earlier bf16 error: the autocast wrapped `pipeline._embedding.__call__`,
which already returns a NUMPY array — so under autocast the `.numpy()` ran on a bf16 tensor (numpy has
`float16` but no `bfloat16` → "unsupported ScalarType BFloat16"; that is also why fp16 "ran" and bf16
didn't). Fix (`scripts/pyannote_diar_server.py` `wrap_embedding_amp` + `_embedding_module`): wrap the
**inner wespeaker ResNet `nn.Module.forward`** — not the numpy-returning `__call__` — and **cast its
output back to fp32**. The conv/matmul (~93% of diar) then run bf16 on tensor cores while pyannote's
clustering still sees fp32, and bf16 keeps fp32's exponent range (the thing fp16 lacked that caused the
DER 9→66 collapse). MEASURED on the same 0.94 h corpus (L40S, prod, warm servers): **DER 9.0 unchanged,
WER 45.2, cpWER 48.1→48.2 (+0.1 = run noise), peak VRAM 8.4→7.7 GB, quick-e2e command wall 60.5→44.7 s
(−26%), est. cost $0.034→$0.0275 (−19%)**; per-warm-meeting wall 4.0–5.6 s → 2.9–4.0 s (~30%). It is now
the DEFAULT (`NOTO_PYANNOTE_FP16=emb-bf16`, CUDA-gated so it is a pure-fp32 no-op on CPU; the benchmark
injector `BENCH_PYANNOTE_FP16` default flipped to match; set `0` to revert). Also landed: per-stage
**SPEED factors** (STT-only / DIAR-only, audio ÷ stage wall) in `benchmark/e2e/synth_test.go`, because
the overlapped e2e wall was hiding STT under the longer diar stage and making "STT looks slow vs the
994× leaderboard" unanswerable — STT is the fast stage; diar is and remains the critical path. NEXT
quality-safe diar levers, in order: **cross-stream concurrency** (TODO-9 — the trailing GPU sample was
only **33% util**, so a serial single stream leaves the card mostly idle; pipelining meeting B's GPU
embedding under meeting A's CPU clustering is a throughput win with byte-identical scores), a
**validated TRT/ONNX fp16 embedding export** (another ~2× on top of bf16, harder), or **fewer
embeddings** (VAD/enrolled, TODO-7/8, real-audio only). Eleventh-pass Modal spend ≈ $0.03 (1 run).

Previous state (2026-06-10, SEVENTH pass — the 3× STT win is LANDED + new defaults): a bare
`make model-benchmark` now runs **fp32 STT on L40S, prod profile, both warm servers** and costs
**≈ $0.020/audio-hr** (measured: e2e 33 s for 1.04 h of audio = RTF 0.0089, GPU 100 % util), KPIs
WER 50.3 / DER 9.6 / cpWER 54.8. That is **~3.4× cheaper than the sixth-pass $0.068 (L4 int8)** and
1 h of audio benchmarks in ~33 s. The three default flips (`scripts/modal_benchmark.py` + Makefile):
**(a) fp32 on GPU** (int8 stays CPU-only) — measured **3.6× faster AND 3.6× cheaper** than int8 on
L40S (e2e 121 s→33 s, $0.074→$0.021) with WER *better* (51.7→50.3); the old "mtg00 82 % collapse"
did NOT reproduce, and fp32 was already proven cleaner on LibriSpeech (1.69 vs 2.07). **(b) L40S
default** — 2× faster than L4 at ~equal per-run cost now the GPU is the bottleneck. **(c) prod
default** — `fast`/`-jobs` measurably loses with warm servers (serialized, 1.31× wall / 1.51× cost).
The sixth-pass measurements that justified all three (below) still hold; their numbers were on L4
int8, the new defaults are L40S fp32. Also fixed: the synthetic builder now CLEANS its output dir
(`*.words.json` ghosts from a larger prior build were silently re-added by the bench's glob).

Earlier sixth-pass state (L4 int8 baseline, for the comparison the flips are measured against): prod
single-stream ≈ $0.068/audio-hr on L4, RTF 0.064, GPU 100 % util at jobs=1, KPIs 51.7 / 9.1 / 54.3 —
identical across every GPU and profile (concurrency/GPU never move the score). **Three findings that
drove the flips:**
(1) **`fast`/`-jobs` is now OBSOLETE — prod wins.** Measured on the same 1 h corpus, fast (jobs=8) is
**1.31× the wall and 1.51× the cost** of prod (315 s vs 240 s; $0.102 vs $0.068/audio-hr). The warm
servers serialize through one process (`e.mu`), so parallel meetings just queue at the mutex while a
single warm stream ALREADY pins the GPU at 100 %; fast only adds its bigger container's cost. Real
throughput now comes from **horizontal scaling (N Modal containers, linear cost ÷ wall)** or
**server-side dynamic batching** (TODO-9), not `-jobs`.
(2) **GPU verdict in the saturated regime: L4 cheapest, L40S 2× faster at break-even, A100 still
dominated.** prod on the 1 h corpus: L4 240 s / $0.068, **L40S 121 s (2.0× faster) / $0.071 (+5 %)**,
A100-40GB 223 s / $0.140 (2× cost, Ampere int8-ONNX weakness). Now that the GPU is the bottleneck, a
2× card finally pays — **use L40S for fast TDD iteration** (2× turnaround at ~break-even); keep **L4
the default for cost**; **never A100/H100** for this int8 stack.
(3) **the precision lever is unblocked** (TODO-2): fp32 is clean on LibriSpeech, so fp16/bf16 (~3× STT)
is safe — the single biggest single-stream win left, since **STT is now the entire critical path**
(diar's 0.04 RTF runs concurrently under STT's 0.06 and is fully hidden). The warm STT loader was also
reconciled to sherpa's own `read_wave` (TODO-3); the residual +1.4 pt server-vs-CLI WER was NOT the
loader (it produced identical WER) — it's GPU decode nondeterminism, within the CLI's own band.
`fast` was right-sized 8/32→6/16 GiB and `concurrent_pkgs` gated off for `*-server` (5th pass).

---

## 📊 SCOREBOARD (eighth pass, 2026-06-10) — done, open, and what to do next

### Status at a glance

| TODO | what | status |
|---|---|---|
| TODO-1 | warm pyannote diarizer (RTF 0.37→0.04) | ✅ DONE (4th) |
| TODO-2 | precision: **fp32 on GPU, 3.6× faster/cheaper** | ✅ DONE (7th) |
| TODO-3 | persistent serving — warm STT server + loader reconcile | 🟡 PARTIAL; **not next** for runtime-only optimization |
| TODO-4 | Sortformer gated fast diar (4-spk cap) | ⬜ optional only if product accepts the cap/fallback behavior |
| TODO-5 | TensorRT / NeMo STT serving | ⬜ open (biggest lift) |
| TODO-6 | split CPU-tail to CPU-only Modal fns | ⬜ open (small) |
| TODO-7 | **VAD-gating** (pay for speech, not silence) | ⬜ open — biggest *real-audio* lever |
| TODO-8 | enrolled-speaker attribution (skip clustering) | ⬜ open — biggest *diar-algo* win |
| TODO-9 | **server-side batching / `@modal.concurrent`** | 🟡 scaffolded; current e2e batching not a win until diarization batches |
| TODO-10 | long-form Parakeet (delete 120 s chunking) | ⬜ open |
| TODO-11 | GPU clustering / committed-GPU arbitrage | ⬜ open (small) |

Also landed but not a TODO: parallel orchestration, container right-size, **L40S + prod defaults**,
synthetic-builder ghost-file fix, and **quick e2e as the routine default**. The current next lever is
**real diarization throughput with no quality/product-contract compromise**: profile pyannote,
then optimize true batching, waveform/decode overhead, or clustering/CPU-tail work based on measured
stage timings. NOT horizontal fan-out: it rents more GPUs for the same total GPU-seconds and remains
explicitly excluded. NOT warm endpoint work for this iteration: that reduces startup/repeated-call
latency, not the actual runtime of the benchmark workload.

### Speed gains landed — measured evals

| optimization | metric | before → after | gain | pass |
|---|---|---|---|---|
| parallel orchestration | benchmark wall | 19.7 min → 3.5 min | ~5.6× | 1st |
| container right-size (prod) | $/hr | $1.44 → $1.06 | −26 % | 2nd |
| warm pyannote diarizer | diar RTF | 0.37 → 0.04 | **~9×** | 4th |
| warm STT server | STT cold-load/mtg | ~11 s → 0 (amortized) | — | 5th |
| prod ⟶ over fast (warm regime) | e2e wall, cost | fast is 1.31× / 1.51× worse | use prod | 6th |
| L40S ⟶ over L4 (GPU saturated) | e2e wall | 240 s → 121 s | **2.0×** | 6th |
| **fp32 ⟶ over int8 (GPU)** | **e2e wall + $** | **121 s → 33 s / $0.074 → $0.021** | **3.6× + 3.6× cheaper** | **7th** |
| **quick e2e default** | routine benchmark rental | 111 s / $0.068 → 55.6 s / $0.034 | **~2× cheaper/faster** | 8th |
| experimental e2e batch | benchmark wall | 54.7 s → 52.2 s, but 14.5→39.1 GB VRAM | **not worth defaulting** | 8th |
| **bf16 embedding (DER-neutral)** | **quick e2e wall + $** | **60.5 s → 44.7 s / $0.034 → $0.0275** | **−26% / −19%, DER 9.0 unchanged** | **11th** |
| **CUMULATIVE** | **$/audio-hr** | **$0.61 → ~$0.029/audio-hr** | **~21× cheaper** | — |
| **CUMULATIVE** | **wall per 1 h audio** | **~6 min → ~45 s** | **~8× faster** | — |

### Where the wall goes NOW (per ~0.94 h audio · L40S · fp32 · prod · both warm servers)

```text
 routine quick e2e wall ≈ 55.6 s for 0.94 h audio
 (RTF ≈ 0.016, GPU 100% util, peak 8.4 GB, $0.034/run)

   e2e chain (STT ∥ diar per meeting)  █████████████████████████████  ~47.9 s test elapsed
   Go test / harness overhead          ████                            ~7.7 s
   Modal setup inside container        ▏                               ~0.3 s warm-volume path
   one-off corpus rebuild              █████████████                   ~22.4 s ONLY with --rebuild-synth

   full suite, same corpus:
     atomic STT       22.6 s command
     atomic diar      41.8 s command
     e2e chain        46.7 s command
     total            111.0 s / $0.068

   quick e2e deletes the first two commands and keeps the e2e signal:
     e2e-only         55.6 s / $0.034

   ⚠ POST-eighth-pass correction: benchmark duplication was the biggest cheap
     lever and is now removed by default. Inside the e2e chain, diarization is
     the practical ceiling: atomic diar is ~1.85× STT wall (41.8 s vs 22.6 s),
     and current "batch" requests do not make pyannote process multiple meetings
     as one efficient GPU batch.

   ⮕ GPU reaches 100% util, but util≠efficient batching. The next wall-time win
     must reduce the diarization critical path or make diarization truly batched.
```

### Eighth-pass batch-size sweep (one L40S, 10-meeting synthetic corpus)

All rows use `BENCH_SYNTH_MEETINGS=10`, `BENCH_HOURS=1`, `BENCH_SEED=1`, quick e2e,
fp32 STT, warm Parakeet server, warm pyannote server. The sample is 9/10 meetings,
~0.94 h audio.

| mode | command wall | test elapsed | est. cost | peak VRAM | KPIs |
|---|---:|---:|---:|---:|---|
| default quick e2e, no e2e batch | **55.6 s** | 47.9 s | $0.034 | 8.4 GB | 45.2 / 9.0 / 48.1 |
| full suite (atomic STT + atomic diar + e2e) | 111.0 s | — | $0.068 | 8.4 GB | 45.2 / 9.0 / 48.1 |
| `BENCH_E2E_BATCH=1`, batch=1 | 52.6–54.7 s | 48.4–50.3 s | $0.034–0.046 | 14.5 GB | 45.4 / 9.0 / 48.3 |
| `BENCH_E2E_BATCH=1`, batch=2 | 57.7 s | 52.7 s | $0.036 | 22.7 GB | 45.4 / 9.0 / 48.3 |
| `BENCH_E2E_BATCH=1`, batch=4 | 52.2–53.4 s | 48.0–49.0 s | $0.032–0.033 | **39.1 GB** | 45.4 / 9.0 / 48.3 |
| `BENCH_E2E_BATCH=1`, batch=8 | 58.7 s | 54.1 s | $0.036 | 12.4 GB | 45.2 / 9.0 / 48.1 |

Verdict: leave `BENCH_E2E_BATCH` off by default. It proves the Go/Python protocol seam, but it is
not a throughput win until the diarizer itself batches.

### Best next options — scored 1–10 (THROUGHPUT lens: more work per RENTED GPU-second)

**The goal (clarified): minimize the TIME one GPU is rented to do the workload = maximize throughput
(audio processed per rented GPU-second), by doing the work FASTER on a single card.** Two things are
deliberately OFF this list: **horizontal fan-out / more GPUs** — cuts wall but rents N GPUs for the
same total seconds, so $0 saved ("multiple GPUs is counterintuitive"); and **VAD / less audio** —
that's doing *less work*, not the same work faster (still the #1 *production* lever on real audio,
just off-target for this goal). The real levers pack more work into each rented second:

| rank | option | throughput ↑ | effort | why |
|---|---|---|---|---|
| **1** | ~~profile pyannote runtime~~ ~~quality-safe embedding speedup~~ **bf16 embedding SHIPPED (11th)** | **8** | ✅ DONE | Tenth pass localized diar to **embeddings ~93 %** (COMPUTE-bound) and wrongly ruled out bf16. Eleventh pass FIXED it: wrap the inner ResNet **module** (not the numpy-returning `__call__`) in bf16, cast output to fp32 → **DER 9.0 unchanged, quick e2e 60.5→44.7 s (−26%), VRAM 8.4→7.7 GB**. bf16 keeps fp32's exponent range; fp16 didn't (that was the DER 9→66 cause). A further ~2× is still possible via a **validated TRT/ONNX fp16 embedding export** (harder). |
| **1b** | **cross-stream concurrency (TODO-9): fill the 33%-idle GPU** | **7** | M | NEW top open lever. The trailing GPU util sample is **33%** — a serial single stream leaves the L40S mostly idle (CPU clustering / IPC / segmentation gaps). Pipelining meeting B's GPU embedding under meeting A's CPU tail raises throughput with **byte-identical scores** (no math change). Needs the warm diar server to process requests concurrently (or N worker procs sharing the card), not the per-request mutex it has now. This is the path past 100× on the long corpus. |
| **2** | **no double-pass: `BENCH_QUICK=1` (e2e-only) for routine runs** | **7** | ✅ DONE | Measured same corpus: full suite 111.0 s / $0.068 → quick e2e 55.6 s / $0.034. Keep atomic STT/diar as explicit diagnostics (`BENCH_QUICK=0`). |
| **3** | **true pyannote batching / shorter CPU tail** | 6 | M/L | If timings show segmentation/embedding dominate, batch windows/meetings inside the pyannote runtime instead of looping wav files. If clustering dominates, parallelize or replace the clustering tail. This keeps the model and speaker contract unchanged. |
| **4** | **TODO-10 long-form Parakeet** | 4 | M | Deletes 120 s chunking/stitch overhead and may improve boundary robustness. STT is no longer dominant, so expected e2e gain is modest: **5–15 %** unless STT regressions vanish too. |
| **5** | **TODO-5 TensorRT / fp16 kernels** | 4 | L | Faster STT kernels help, but STT is below diarization now. A 2× STT win alone likely saves **≤10–15 s** on this corpus unless paired with diarization work. |
| **6** | **experimental cross-meeting e2e batching** | 2 | ✅ scaffolded | Protocol and Go seams exist (`BENCH_E2E_BATCH=1`), but measured batch=4 was only ~4 % faster and used 39.1 GB VRAM; not a default until diarization really batches. |
| **7** | **faster chip, only if work/$-positive** (L40S = default; never A100/H100 sans TensorRT) | 2 | S | L40S is already the throughput pick. Bigger chips are not justified for this stack without a TensorRT/fp16 path or a larger batchable workload. |
| parked | **TODO-3b deployed warm `@modal.cls` / persistent endpoint** | n/a | M | Parked for this runtime-only pass. It can improve startup and repeated tiny requests, but it does not reduce the measured compute path for 1 h of benchmark audio. |
| parked | **TODO-4 Sortformer gated fast diar** | n/a | M | Parked unless the product explicitly accepts a ≤4-speaker fast path with pyannote fallback. It is a different behavior contract, not the best no-compromise next step. |
| ✗ | horizontal fan-out / more GPUs | **0** | — | EXCLUDED — cuts wall, not rented-seconds; rents N GPUs for the same total time. |
| ✗ | VAD / less audio (TODO-7), enrolled-speaker (TODO-8) | n/a | — | EXCLUDED here (they reduce *work*, not raise throughput) — but they are the top **production / real-audio** cost levers; keep for that track. |

**Do-next recommendation:** implement **pyannote stage timing first**, then optimize the measured
dominant substage. Add per-request timing in `scripts/pyannote_diar_server.py` for waveform load /
decode, pipeline compute, clustering or post-processing where separable, and response serialization.
Run the same 0.94 h quick-e2e benchmark plus a non-quick atomic split. Pick the first implementation
from the measured bottleneck:

- If segmentation/embedding GPU time dominates: implement true window-level batching in the pyannote
  process instead of request-level wav-list batching.
- If clustering CPU time dominates: parallelize or replace clustering, or move it off the critical
  GPU-rented path only if total rented cost improves.
- If decode/temp WAV handling is material: remove repeated temp-file decode and pass waveform tensors
  through directly.

Acceptance target: same pyannote model and speaker-count hints, DER/cpWER within ~1 pt of current
pyannote on the synthetic benchmark, quick e2e command wall **≤45 s** for the 0.94 h sample as the
first milestone and **≤42 s** as the stronger target, without raising peak VRAM above the current
8–15 GB safe band.

---

- [x] **TODO-1 — DONE 2026-06-10 (fourth pass): warm pyannote diarizer MEASURED, passes acceptance.**
  L4 prod full corpus: **diar RTF 0.37 → 0.04** (~9×), STT WER 45.5% (identical), e2e cpWER 49.9%
  (+0.6) / DER 9.6% (+0.8) — both within the ~1 pt gate. **Prod e2e RTF 0.076 → ~$0.08/audio-hr**
  (was ~$0.40). Atomic diar DER (auto speaker count) also improved 29% → 9.6%. One decode fix was
  needed (pyannote 4.x torchcodec `AudioDecoder` missing FFmpeg libs → the server now loads the WAV
  itself and passes a waveform tensor). Details in the fourth-pass Done section below. Remaining:
  flip `DIAR_ENGINE` default + re-run `make model-sweep` once fast-mode is validated too.
  <details><summary>original TODO-1 (for context)</summary>
  The engine is fully
  wired + unit-tested (`cuda-pyannote-server`: `scripts/pyannote_diar_server.py` +
  `internal/platform/providers/diarize/pyannote_server.go`) but NEVER MEASURED.
  **Model distribution (decided): NO tokens, NO HF accounts, for anyone.** The default pipeline is
  the **ungated** community mirror `pyannote-community/speaker-diarization-community-1`
  (verified: anonymous download works; CC-BY-4.0; self-contained — config.yaml + segmentation +
  embedding + plda bundled in one repo). Same weights as pyannote's gated original — the gate
  there is a marketing form, not a license term, and CC-BY-4.0 makes mirrors legal. This is the
  exact pattern we already ship today (the sherpa segmentation-3.0 ONNX from `noto models
  download` is k2-fsa's mirror of a gated pyannote model). Weights cache into the Modal model
  volume (`HF_HOME`) on first use; `BENCH_PYANNOTE_OFFLINE=1` serves from cache with no hub
  contact. Two follow-ups for whoever lands this: (a) since the mirror is third-party, treat the
  benchmark DER/cpWER acceptance check as the weight-integrity check, and (b) for supply-chain
  control + user distribution, copy the snapshot into noto's own model channel (beside the sherpa
  tarballs, with the CC-BY attribution file) and point `NOTO_PYANNOTE_PIPELINE` at the local dir.
  Ungated fallbacks if the mirror ever vanishes: **Sortformer streaming v2.1** (NVIDIA Open Model
  License — hard 4-speaker cap) or **3D-Speaker/FunASR** (Apache-2.0).
  </details>
  Accept if: DER/cpWER within ~1 pt of the sherpa baseline (DER 8.8 e2e / cpWER 49.3) and diar
  RTF drops 0.37 → ≤0.10. Then flip it to the GPU default (`DIAR_ENGINE` default in
  `scripts/modal_benchmark.py`) and re-run `make model-sweep`. Expected: e2e → ~$0.10–0.15/audio-hr.
- [x] **TODO-2 — DONE 2026-06-10 (sixth pass): the fp32 mtg00 collapse is NOT a precision bug.**
  Ran the clean LibriSpeech WER bench (`benchmark/stt/libri_test.go`, 170 single-speaker clips, no
  overlap, clips short enough to skip the 120 s chunker) under both precisions: **int8 2.07 % WER,
  fp32 1.69 % WER** — fp32 is actually marginally *cleaner*, and both are pristine. So fp32 itself is
  fine; the mtg00 82.3 % collapse is an **overlapped-speech / chunk-boundary decode interaction**,
  not full-precision degeneration (libri exercises neither overlap nor chunking and fp32 is perfect
  there). **This green-lights the precision lever** (fp16/bf16, GPU-native, ~3× STT — the biggest
  single-stream win left): export fp16, then fix the chunk-boundary decode (or adopt long-form
  Parakeet, TODO-10, which removes chunking entirely and is the likely root cause). VRAM caveat
  still applies under `fast` (fp32/fp16 OOM'd full concurrency on a 24 GB L4 — use `prod`, the now-
  preferred profile anyway).
  **SHIPPED 2026-06-10 (seventh pass): fp32 is now the GPU default and the mtg00 collapse did NOT
  reproduce.** Clean L40S head-to-head (prod, warm server, 10-meeting corpus): int8 e2e 121 s /
  WER 51.7, **fp32 e2e 33 s / WER 50.3** — **3.6× faster, 3.6× cheaper, WER better**, no per-meeting
  collapse (overall STT 47.2 % ≈ int8). So the 3× win needed NO chunk-overlap fix at all — fp32 just
  works now. A true fp16 export (half the VRAM, same speed) is the only remaining nicety; fp32 is
  fine on `prod` (peak ~8 GB). int8 stays the CPU/Mac default.
- [~] **TODO-3: persistent batched serving (#1b, the prod-architecture prize).** PARTIALLY DONE
  (fifth pass): the warm STT server (`cuda-parakeet-server`, `scripts/parakeet_stt_server.py` +
  `internal/platform/providers/stt/parakeet_server.go`) loads the OfflineRecognizer ONCE per run and
  is now the GPU default — atomic STT RTF 0.08 → 0.06, the per-meeting ~11 s cold load is gone.
  STILL OPEN: (a) a DEPLOYED Modal `@modal.cls` with `@modal.enter` preload (+ GPU memory snapshots)
  serving across REQUESTS/containers, not just across meetings within one run. DONE (sixth pass):
  (b) the STT server's loader was reconciled to sherpa-onnx's OWN `read_wave` (the exact C++ reader
  the CLI uses) so the input samples are provably byte-identical — but this produced the SAME 46.9 %
  WER, proving the loader was NOT the source of the +1.4 pt server-vs-CLI drift (stdlib-wave and
  read_wave both scale int16 by 1/32768, so they were always equal). The residual drift is GPU
  decode nondeterminism (the CLI itself varies 43.1–45.5 % run-to-run), so the warm-server default
  is safe; `read_wave` stays as correct hygiene that removes the loader as a variable.
  The Go seams are ready: `stt.BatchSTTEngine` / `RegisterSTTEngine`, `diarize.RegisterSegmentEngine`.
  Interim zero-rewrite option for STT only: sherpa-onnx offline websocket server (`--max-batch-size`;
  beware the CUDA VRAM leak, sherpa-onnx#2631).
- [ ] **TODO-4: Sortformer v2.1 as a gated fast path (optional).** ~3–5× cheaper diarization than
  even pyannote-native, but hard 4-speaker ceiling (DER ~41% at 5–9 spk) → only with an
  auto-fallback to pyannote when >4 speakers are suspected. Use the streaming v2.1 (NVIDIA Open
  Model License); the offline 4spk-v1 is CC-BY-NC — unusable commercially.
- [ ] **TODO-5: TensorRT / NeMo serving for STT (#4, biggest lift).** Only after TODO-1–3; the
  cheap 80% comes from those.
- [ ] **TODO-6 (small): split CPU-bound tail work to CPU-only Modal functions** (clustering/merge)
  once the diarizer is re-architected — right shape, small absolute saving.

New levers researched 2026-06-10 (second pass) — see that section below for sources. These are
**production-shaped**: the dense-speech synthetic corpus hides some of them, so most need a
real-meeting corpus to measure, not the current benchmark.

- [ ] **TODO-7 (highest *production* value): VAD-gate the pipeline — pay for speech, not silence.**
  Real meetings are 30–70% non-speech (pauses, dead air, hold, one person talking while others are
  muted); a 1-hour recording is often 10–15 min of actual speech. Both STT and diarization cost
  scale with the audio duration fed to the GPU, so a cheap front VAD (Silero/pyannote VAD, ~free on
  CPU) that feeds only speech regions cuts RTF roughly in proportion to the silence fraction — this
  is exactly what WhisperX does (VAD pre-segmentation + batched chunks → 60–70× realtime, *no WER
  degradation*). **The synthetic benchmark will NOT show this win** (it's dense, overlapped speech
  by construction) — it needs a real-meeting corpus to measure, and is the single biggest lever
  that's invisible in our current numbers. Wire it ahead of STT and diarization both; it also
  reduces ASR hallucination on long silences. Pairs with the chunker (feed VAD spans, not fixed
  120 s windows).
- [ ] **TODO-8 (noto-specific, biggest diarization algorithm win): enrolled-speaker attribution
  skips unsupervised clustering for known rosters.** The diarization cost is dominated by the
  CPU-bound, single-threaded *clustering* stage (§3) — and clustering only exists because the
  speaker set is unknown. But noto already ships the pieces to make it known for the common
  "same team every week" case: the in-process ECAPA embedder
  (`internal/platform/providers/speaker/ecapa.go`, `embedder.go`) and cosine/centroid matching with
  a precision-first policy (`internal/core/speakers/matching.go` — `CosineSimilarity`, `Centroid`,
  `MatchConfident`; decision 0006). For a meeting whose participants are enrolled, attribution
  becomes VAD-segment → embed → cosine-match against enrolled centroids — **no agglomerative/spectral
  clustering at all**, which is cheaper AND more accurate (supervised ID beats unsupervised
  clustering). Fall back to full clustering for unenrolled speakers (hybrid: match the knowns, cluster
  the rest). This reuses code noto already has and directly attacks the stage that idles the GPU.
- [ ] **TODO-9: multi-tenant input concurrency on Modal (`@modal.concurrent`) — the serverless form
  of "batch across streams".** TODO-3 loads the model once; this *shares* that one warm
  GPU across many simultaneous meetings. `@modal.concurrent(max_inputs=, target_inputs=)` lets one
  container process N requests at once (Modal autoscales more containers past the target), so the
  $0.80/hr L4 is amortized across concurrent users instead of one-GPU-per-user. This is what takes
  per-user cost well below the single-stream figure; combine with the warm pyannote/STT servers
  (TODO-1/TODO-3) and `min_containers≥1` for live-session warmth. Implement on top of TODO-3, not
  separately.
- [ ] **TODO-10: long-form Parakeet-v3 to delete the 120 s chunking on the GPU path.** NVIDIA's
  model card confirms v3 does **full attention to ~24 min** and **local attention to ~3 h in a
  single pass**. Our 120 s chunking exists only to bound memory; switching the GPU path to
  local-attention long-form removes the chunk-stitch + per-chunk overlap recompute entirely (keep
  chunking only for the CPU/Mac local path with tight RAM). Likely also resolves part of TODO-2 (the
  fp32 mtg00 collapse may be a chunk-boundary decode interaction). Watch VRAM — long-form full
  attention is the memory-hungry mode.
- [ ] **TODO-11 (small): GPU clustering for the diarization tail / cheaper-capacity arbitrage.**
  (a) The clustering stage is single-threaded CPU and stalls the GPU (§3, util≠efficiency at
  batch=1); RAPIDS cuML can run agglomerative/spectral clustering on the GPU — an alternative to
  TODO-6's "move it to a CPU function" (do whichever the re-architected diarizer makes natural). (b)
  Modal's premium buys scale-to-zero + per-second billing — ideal at spiky/low volume. At *sustained*
  high utilization, a reserved/committed L4/L40S (RunPod/Lambda/Vast, or a cloud committed-use
  discount) is materially cheaper per GPU-hour. Model the crossover once real volume is known; don't
  migrate prematurely (you'd lose the scale-to-zero that makes low volume cheap).

Hygiene notes for whoever takes over: scores must stay byte-comparable (cpWER 49.3 / DER 8.8 /
WER 45.5 on the synthetic corpus; concurrency/serving changes must never move them) · don't
"upgrade" to A100/H100 (measured slower AND pricier for this stack) · the synthetic WER level
itself is corpus-inflated, not a model problem (see "KPI issue") · `fast`-profile GPU peak is
already ~21 GB on L4 at jobs=8 — watch VRAM before adding concurrency.

---

## TL;DR — the gap and why

> **Eighth-pass correction:** the older analysis below correctly identifies batching as the path to
> leaderboard-class throughput, but the measured `BENCH_E2E_BATCH=1` implementation only batches at
> the request/protocol level for diarization. It did **not** improve current e2e wall time. Treat
> "batching is #1" below as **true runtime/model batching**, especially for diarization, not merely
> sending several wav paths in one JSON request.

We run **open-source small models** (Parakeet-TDT-0.6B int8 STT + pyannote/CAM++ diarization) yet
measure **RTF ~0.42** (≈2.4× real-time) end-to-end single-stream. Headline leaderboard numbers say
Parakeet does **RTFx ~3000** (RTF 0.0003). That headline is **batched offline throughput on an
A100/H100, compute-only, no diarization** — a fundamentally different metric. The realistic
single-stream ceiling for this model is **RTF ~0.02–0.05**, so we have **~3–10× headroom on STT**,
and **diarization is the bigger half of the cost**. The dominant, fixable bottlenecks, in order:

1. **batch=1 / no true runtime batching** — biggest lever only if the model runtime really batches.
2. **Cold subprocess per meeting** — model load + CUDA-context init paid *every meeting*.
3. **Diarization dominates** the e2e cost and is largely CPU-bound (clustering/embedding).
4. **ONNX-Runtime int8 on L4** vs an optimized TensorRT/fp16 path.
5. **Container over-provisioning** (8 vCPU / 32 GB) — ~44% of the bill; cost-only, easy win.

---

## Measured data (real Modal runs, this session)

Synthetic chained pipeline (`--quick` e2e = STT→diarize→merge), **full corpus = 3 meetings,
1115.8 s = 18.6 min = 0.31 h** of audio, 4 speakers each. Scores identical across all
GPUs/profiles (cpWER ≈ 49.3–49.8%) — concurrency never changes accuracy.

### Per-GPU × profile (prod = 1 stream `-jobs=1`; fast = parallel `-jobs=3`)

| GPU | profile | wall | GPU util | GPU mem | est $/run | RTF | per-meeting latency | $/audio-hr |
|---|---|---|---|---|---|---|---|---|
| L4 | prod | 474 s | 71% | 4.4 GB | $0.19 | 0.42 | 158 s | **$0.61** |
| L4 | fast | 208 s | 100% | 13.2 GB | $0.08 | 0.19 | 69 s | **$0.27** |
| L40S | prod | 405 s | 76% | 4.6 GB | $0.29 | 0.36 | 135 s | $0.94 |
| L40S | fast | 164 s | 100% | 13.9 GB | $0.12 | 0.15 | 55 s | $0.38 |
| A100-40GB | prod | 605 s | 73% | 4.7 GB | $0.46 | 0.54 | 202 s | $1.49 |
| A100-40GB | fast | 263 s | 100% | 13.8 GB | $0.20 | 0.24 | 88 s | $0.64 |

Key reads:
- **Parallelism is a free win** (~2.3–2.5× faster *and* cheaper everywhere; same compute, less wall).
- **A100 is Pareto-dominated for this int8/CPU-bound workload** — slower *and* pricier than L40S/L4.
  Do NOT scale to A100/H100 for the current stack; revisit only with large/fp16 models or real batching.
- **Cost model:** `$/audio-hr = container_$/hr × RTF`. Container = GPU + 8 vCPU ($0.38/hr) + 32 GB
  ($0.26/hr). On L4 that's $1.44/hr, of which **CPU+mem = $0.64/hr ≈ 44%** (over-provisioned for 1 stream).

### Per-stage split (STT vs diarization) — **measured, new warm-batch code, L4, 1 meeting (mtg00, ~426 s)**

| stage | RTF | wall | of which model-load (CUDA init) | notes |
|---|---|---|---|---|
| **STT** (Parakeet int8) | **0.10** | 53.7 s | ~11 s (~20%) | improved from 0.14 (warm chunk-batching) |
| **diarization** (pyannote+CAM++) | **0.37** | 172.4 s | ~14 s | **~79% of single-stream e2e wall** |
| e2e (sequential per meeting) | ~0.50 | 212.5 s | — | STT then diarize |

**This changes the priority depending on the goal:**
- **Prod single-stream latency/cost → diarization is the #1 target** (3.7× the STT cost; halving it
  saves ~0.18 RTF vs ~0.05 for STT). STT is already cheap.
- **Benchmark throughput cost → true batching across streams is still valuable, but not yet achieved.**
  The eighth-pass `BENCH_E2E_BATCH=1` protocol path did not improve wall time because pyannote still
  effectively processes files one at a time. The next implementation should make diarization truly
  batched or use a faster diarizer.
GPU util on these single-stream stages was ~93% peak but that's bursty — the average is far lower
(idle during CPU clustering / feature extraction / model load); util≠efficiency at batch=1.

---

## The bottlenecks, ranked — root cause + exact location + fix

### 1. No true runtime batching (batch=1). **Biggest lever only after diarization batches.**
- **Why:** a 0.6B model on a single utterance leaves the GPU ~90% idle — latency-bound on tiny
  kernel launches, not throughput-bound. The 3000× leaderboard number is batch=128–256. We feed
  one file at a time.
- **Where:** `internal/platform/providers/stt/sherpa_engine.go` (`Recognize` → `transcribeFiles`
  passes the chunks of ONE meeting). The `sherpa-onnx-offline` CLI *can* take many files per process
  and we already exploit that *within* a meeting (warm model across its chunks), but **not across
  meetings/requests**, and ONNX-RT doesn't batch the encoder across utterances here.
- **Fix options (in increasing effort/payoff):**
  - **(a)** Batch *across meetings* in the atomic STT bench: feed all meetings' chunks to one CLI
    process. Cheap; benchmark-only; big benchmark-cost win. (Not valid for e2e, which must stay a
    real per-meeting chain.)
  - **(b)** A **persistent batched inference server** (the real fix, helps prod too): load the model
    once, accept many concurrent requests, batch encoder calls. Candidates: sherpa-onnx
    non-streaming websocket server, or NVIDIA Triton/NeMo with dynamic batching, or the sherpa-onnx
    Go C-API in-process (cgo behind a build tag — the seam is ready, see §"Architecture seam").
  - Expected: **5–20× GPU throughput** → benchmark $/audio-hr drops by that factor; prod throughput
    per GPU rises so one GPU serves many concurrent users.

### 2. Cold subprocess per meeting (model load + CUDA init every time)
- **Why:** each meeting (and each bench package) spawns a fresh `sherpa-onnx-offline` /
  `sherpa-onnx-offline-speaker-diarization` process that initializes a CUDA context (+cuDNN) and
  loads ONNX weights onto the GPU before any compute. On CPU this is ~3 s ("recognizer created in
  3.145 s"); on GPU CUDA init is typically **5–15 s**. For a ~6 min meeting that can be 20–40% of wall.
- **Where:** `sherpa_engine.go` `transcribeFiles` (`exec.CommandContext(... e.bin ...)`),
  `internal/platform/providers/diarize/sherpa_segment.go` `Segment` (same pattern). One process per
  `Recognize`/`Segment` call.
- **Fix:** a **long-lived/warm model process** (same persistent-server work as #1) — load once, reuse
  across all meetings/requests. For prod, a warm worker pool (also see Modal `min_containers=1` /
  scaledown for live sessions, `.docs/benchmarks.md` runtime policy). Removes the per-meeting load entirely.

### 3. Diarization is the dominant stage and largely CPU-bound
- **Why:** RTF ~0.38 (vs STT ~0.14). pyannote segmentation (GPU) + CAM++ speaker embedding (GPU) +
  **clustering (CPU)** + the CLI's orchestration. The embedding runs per speaker-window; clustering
  is single-threaded CPU work that doesn't use the GPU at all.
- **Where:** `sherpa_segment.go` `Segment` — shells to `sherpa-onnx-offline-speaker-diarization`.
  Unlike STT, this binary takes ONE file (no multi-file batching), so the only current lever is
  per-meeting parallelism (already wired via `-jobs`).
- **Fix:** profile the seg vs embed vs cluster split; consider a diarizer that batches embeddings,
  fewer/cheaper embedding windows, GPU clustering, or a different diarization stack. This is the
  least-explored area and probably the **highest-value accuracy+speed target after batching**.

### 4. ONNX-Runtime int8 on L4 vs optimized TensorRT/fp16
- **Why:** the prebuilt sherpa-onnx CUDA build uses ONNX-RT's CUDA EP with int8; the leaderboard uses
  NeMo/TensorRT fp16 with tuned kernels. ~3–5× difference plausible. L4 is also a modest card.
- **Where:** runtime selected in `scripts/fetch-bench-deps.sh` (sherpa-onnx CUDA tarball) +
  `NOTO_SHERPA_PROVIDER=cuda`. Engine alias `cuda-parakeet`/`cuda-pyannote`.
- **Fix:** evaluate TensorRT / NeMo serving for STT; or fp16 ONNX. Bigger lift; do after #1–#3.

### 5. Container over-provisioning (cost only, easy)
- **Why:** the Modal function is fixed at `cpu=8, memory=32768` (`scripts/modal_benchmark.py`
  `@app.function`). For single-stream prod that's ~44% of the bill wasted.
- **Fix:** right-size a prod path to ~2–4 vCPU / 8 GB. Illustrative: L4 prod drops from ~$0.61 to
  **~$0.40–0.44/audio-hr** just from this. (Keep 8 vCPU for the `fast` benchmark where parallel
  decoders need the cores.)

---

## Current recommended fix order (runtime-only, one GPU)

This supersedes the older fix order below it in the investigation notes. For the current task,
optimize actual benchmark/runtime work, not startup or horizontal fan-out:

1. **Add pyannote stage timing.** Measure waveform load/decode, pyannote pipeline compute,
   clustering/post-processing where separable, and response serialization in
   `scripts/pyannote_diar_server.py`.
2. **Run the same 0.94 h benchmark twice.** Use quick e2e for the product-shaped wall-time signal and
   non-quick only when an atomic STT/diar split is needed.
3. **Optimize the largest measured pyannote bucket.**
   - Pipeline GPU time dominates: implement true window-level batching inside the pyannote process.
   - Clustering CPU tail dominates: parallelize/replace clustering, or move it off the GPU-rented
     critical path only if total rented cost improves.
   - Decode/temp WAV handling is material: avoid repeated temp-file decode and pass waveform tensors
     directly.
4. **Only then revisit STT kernel work.** Long-form Parakeet, fp16/TensorRT, and chunk deletion are
   useful, but STT is no longer the dominant wall-time stage on the current measured corpus.

Acceptance target: same model behavior and quality gate, **≤45 s** quick-e2e command wall as the
first win and **≤42 s** as the stronger target on the current ~0.94 h corpus.

---

## Architecture seam (use this — don't rewrite the pipeline)

The runtime is already abstracted, so a runtime-optimized engine drops in **without touching the
pipeline or benchmark scoring**:
- `internal/platform/providers/stt/local.go` — `LocalSTT` delegates to an `STTEngine` interface
  (`Recognize(ctx, audio, opts) []EngineWord`). Implement a new engine (e.g. `stt-server`) that talks
  to a warm/batched backend; register it with `RegisterSTTEngine`. Select via `BENCH_STT_ENGINE`.
- `internal/platform/providers/diarize/local.go` — same shape: `SegmentEngine.Segment(...)`,
  `RegisterSegmentEngine`, `BENCH_DIAR_ENGINE`.
- Batch entry points are already scaffolded behind optional interfaces (`RecognizeBatch` /
  `DiarizeBatch` / `SegmentBatch`) and fall back to per-call execution. The current request-level
  batch path is correctness-preserving but not yet a throughput win because pyannote still loops
  files internally.
- cgo (sherpa-onnx Go C-API) is acceptable **behind a build tag** — the project keeps the default
  build cgo-free and tested with fakes; real runtimes drop in behind tags. (Per project convention.)

---

## Known issues / gotchas encountered

- **`go test` flag ordering:** `-hours`/`-seed`/`-jobs` are TEST flags → must come **after** the
  package path, behind `-args`; `-parallel` is a go-test flag → **before** the path. The runner's
  `go_test()` already does this. Get it wrong and go looks for a package named `.`.
- **CPU thread oversubscription:** with `-jobs>1`, each sherpa process defaults to `nproc` threads;
  N parallel × nproc threads thrashes the cores (local CPU run: RTF 0.51 at 3×6 threads/6 cores).
  The runner divides `NOTO_SHERPA_THREADS` by `jobs` — preserve this when changing concurrency.
- **A100 slower than L4/L40S here** — counterintuitive but measured; don't "upgrade" to it.
- **Warm-batch correctness:** `sherpa_engine.go` falls back to per-chunk decode if the CLI returns a
  result count ≠ file count, so word time-offsets can't be misattributed. Keep that guard.
- **GPU attribution under `fast`:** concurrent packages share one GPU, so per-command `gpu_peak_*`
  over-attributes (whole-GPU sample during overlap). Per-command numbers are clean only in `prod`.
- **Pyright noise** in `scripts/modal_benchmark.py` (`modal` import, `with_options`/`remote`) are
  false positives — no modal type stubs locally; both run fine on Modal.

---

## KPI issue (accuracy, separate from perf — flag for the KPI owner)

The synthetic **STT WER ~45–48% is NOT a model failure.** Evidence: mtg01 alone = 12.3% WER, and
transcripts are clean LibriSpeech English. It's **overlapped speech + reference alignment** in the
synthetic corpus (built with `--overlap-frac 0.18`; the reference counts both overlapping speakers'
words). Also the **atomic DER (29%) ≫ e2e DER (8.8%)** because e2e passes the known speaker count
(`NumSpeakers` → exact clusters) and the atomic bench doesn't. Fix these in the corpus/scoring, not
the model. Don't let the scary WER drive STT model changes.

---

## How to measure / validate (tooling already built this session)

- **Local CPU repro** (slow, ~hours for full suite; fine for one-stage checks): deps in
  `~/.cache/noto-bench`, env via the pattern in `scripts/fetch-bench-deps.sh` output;
  synthetic corpus in `benchmark/dataset/synthetic_meetings/` (3 meetings). One STT meeting ≈ 60 s.
- **Remote GPU** (`scripts/modal_benchmark.py`, auth via `~/.modal.toml` + `.venv-modal`):
  - `make model-benchmark` — current routine signal: one L40S, `prod`, `BENCH_QUICK=1`, quick e2e
    only, no duplicate atomic passes.
  - `BENCH_SYNTH_MEETINGS=10 make model-benchmark BENCH_HOURS=1` — reproduce the current ~0.94 h
    benchmark sample.
  - `BENCH_QUICK=0 BENCH_SYNTH_MEETINGS=10 make model-benchmark BENCH_HOURS=1` — explicit atomic
    STT + atomic diar + e2e split when you need stage attribution. This roughly doubles rental time.
  - `BENCH_E2E_BATCH=1 BENCH_BATCH_SIZE=4 make model-benchmark` — experimental protocol batching
    only; not a default and not currently a throughput win.
  - `make model-sweep` — GPU×profile perf+price table (the data above; `scripts/modal_benchmark.py
    sweep --gpus L4,L40S,A100-40GB --profiles prod,fast`).
  - Knobs: `BENCH_PROFILE` (fast|prod), `BENCH_GPU`, `BENCH_JOBS`, `BENCH_QUICK`, `BENCH_HOURS`,
    `BENCH_SEED`, `BENCH_E2E_BATCH`, `BENCH_BATCH_SIZE`. Results in
    `.modal-results/<run_id>/summary.json` (KPIs, util, mem, `duration_ms`, `estimated_cost_usd`).
    `_kpi`/`_print_sweep_table` format the comparison.
  - **To decompose the current bottleneck:** add pyannote server timing first, then run a non-quick
    synthetic run so atomic diar has its own wall time. Startup timing is explicitly out of scope for
    the next pass; the useful split is actual decode/pipeline/clustering/runtime work.
- **Validate any engine change is score-neutral:** re-run; cpWER/WER/DER must be unchanged (parallel
  vs serial already proven identical). The e2e regression gate (`BENCH_DEEP=1`) + `runs.jsonl` trend
  catch regressions.

---

## Researched 2026-06-10: GPU-native serving options (with sources)

Deep-dive into model/runtime alternatives. Headline: **our models are fine; the serving is the
problem.** Published numbers put pyannote-class diarization at RTF 0.02–0.11 on T4/A6000-class
GPUs vs our 0.37 — sherpa-onnx runs the same pyannote+CAM++ models window-by-window with CPU
clustering, no cross-window GPU batching, in a cold process. The realistic e2e floor on an L4
($0.80/hr) is **~$0.03–0.07/audio-hr batched, ~$0.10–0.15 single-stream** — 10–30× below our
$0.61 and below AssemblyAI retail. Ranked:

1. **Replace sherpa-onnx diarization with pyannote.audio 4.0 `community-1` in a warm GPU process**
   (Python sidecar/server behind the `SegmentEngine` seam, or a Modal `@modal.cls`). Same model
   lineage we ship → no quality regression risk, **no speaker cap**, CC-BY-4.0 (gated HF repo).
   Expected diar RTF 0.37 → ~0.04–0.08 on L4 (extrapolated from 45× RTFx on A6000 / 0.11 on T4 —
   benchmark before trusting). This single change takes e2e to ~$0.10–0.15/audio-hr single-stream.
   It also gets within-meeting chunk-parallel diarization for free (pyannote batches windows
   internally; that's why "clip into 100 clips" is unnecessary for us — and naive chunked
   diarization breaks on speaker-label permutation; the known fix is EEND-VC-style global
   re-clustering of per-chunk embeddings, arXiv:2010.13366).
2. **Warm serving / kill cold starts**: Modal `@modal.cls` + `@modal.enter` preload; GPU **memory
   snapshots** (experimental, `enable_gpu_snapshot`) snapshot VRAM incl. CUDA context — directly
   removes our ~11 s × 2 per-meeting model loads. The sherpa-onnx **offline websocket server**
   (`--max-batch-size`) is the zero-rewrite warm+batched option for **STT only** (it does not
   serve diarization; CUDA build has a reported VRAM leak, sherpa-onnx#2631).
3. **STT batching**: NeMo Parakeet-v3 with `@modal.batched` (no license cost) → ~$0.002–0.01/audio-hr
   for STT. Modal's own batch-transcription writeup (Parakeet on Modal, duration-sorted batching)
   claims ~"$1 per week of audio". Parakeet-v3 with **local attention takes up to 3 h in one pass**
   — removes our 120 s chunking on the GPU path entirely.
4. **fp16 ONNX on GPU, not int8** (same-day experiment): int8 is a CPU optimization; on GPU it
   often runs *slower* than fp16 (dequant overhead). fp16 exports of parakeet-tdt-0.6b-v3 exist
   upstream. Keep int8 for the CPU/Mac local path.
5. **Sortformer streaming v2.1** (NVIDIA Open Model License; the offline 4spk-v1 is CC-BY-**NC** —
   unusable): another ~3–5× diarization cut (RTF ~0.005–0.02), **but hard 4-speaker ceiling**
   (5–9 spk DER ≈ 41%) — only viable with an auto-fallback to pyannote when more speakers are
   suspected. Community ONNX export exists (cgus/diar_streaming_sortformer_4spk-v2.1-onnx).
6. **Skip Riva/NIM**: best published perf (Parakeet single-stream RTFx ~354 on H100, Triton dynamic
   batching, combined ASR+Sortformer container) but requires NVIDIA AI Enterprise (~$2/GPU/hr) —
   triples L4 compute cost; only pencils at very high utilization.
7. **CPU-only Modal functions for CPU-bound tail work** (clustering/merge/alignment): right shape
   ($0.047/core-hr vs holding a $0.80/hr GPU), small absolute saving — do it when the diarizer is
   re-architected, not before. Alternative models (Canary-Qwen, Kyutai, WhisperX/faster-whisper)
   don't beat Parakeet on accuracy-per-$ for our use; stay on Parakeet.

Pricing sanity (Modal, confirmed): L4 $0.7992/hr, CPU $0.0472/core-hr, mem $0.008/GiB-hr;
per-second billing. Sources: modal.com/pricing, modal.com/blog/fast-cheap-batch-transcription,
modal.com/docs/examples/gpu_snapshot, modal.com/docs/guide/dynamic-batching,
huggingface.co/pyannote/speaker-diarization-community-1, huggingface.co/nvidia/parakeet-tdt-0.6b-v3,
huggingface.co/nvidia/diar_streaming_sortformer_4spk-v2.1, arXiv:2509.26177 (independent diarization
benchmark: pyannote 45× RTFx / Sortformer ~210× on A6000; pyannoteAI best DER 11.2%),
k2-fsa.github.io/sherpa/onnx/websocket/offline-websocket.html, arXiv:2010.13366 (EEND-VC).

## Researched 2026-06-10 (second pass): production-shaped cost levers (with sources)

The first research pass attacked *serving* (warm models, batching, the runtime). This pass is about
levers that change **what work the GPU does at all** — and most are invisible in our synthetic
benchmark because it's dense, fully-overlapped speech with an unknown speaker set, i.e. the
worst case for every lever here. They map to TODO-7…TODO-11.

1. **VAD-gating (TODO-7) is the biggest lever our benchmark can't see.** Production meeting audio is
   mostly *not* speech; published guidance puts a 1-hour recording at ~10–15 min of actual speech,
   and STT/diarization cost is linear in audio fed to the GPU. WhisperX is the proof point: VAD
   pre-segmentation + batched inference on speech-only spans hits 60–70× realtime on large-v2 *with
   no WER degradation*, and the VAD also cuts ASR hallucination over long silences. A Silero/pyannote
   VAD pass is near-free on CPU. Net: production RTF drops ~proportionally to the silence fraction,
   on top of every serving win — but you must measure it on a real-meeting corpus, not the synthetic
   one (which has ~no silence to skip).
2. **Enrolled-speaker attribution (TODO-8) removes the CPU-bound clustering stage for known rosters.**
   When speaker identities are known, the literature uses supervised classification from one
   enrollment sample per speaker rather than unsupervised clustering; the unsupervised path exists
   precisely because the speaker set is unknown and is the single-threaded CPU stage that idles the
   GPU (§3). noto already ships the enrollment + match primitives (ECAPA embedder + cosine/centroid
   matching, decision 0006), so for recurring teams this is a code-reuse win, not new research:
   embed VAD spans, cosine-match to enrolled centroids, cluster only the leftover unknowns. Cheaper
   *and* more accurate for the common case.
3. **Modal `@modal.concurrent` (TODO-9) is how serving cost actually amortizes across users.** The
   decorator sets `max_inputs` (how many requests one container handles at once) and `target_inputs`
   (the autoscaler's aim; containers burst to max while new ones spin up). That's the serverless
   shape of "batch across streams" — one warm GPU serves many concurrent meetings, so per-user cost
   falls toward (GPU $/hr ÷ concurrent users) × RTF instead of one GPU per user. Modal is built for
   this (scales to ~20k containers, sub-second cold start). It sits on top of the warm-server work
   (TODO-1/TODO-3); without a loaded-once model, concurrency just multiplies cold starts.
4. **Long-form Parakeet-v3 (TODO-10) deletes our 120 s chunking on the GPU path.** NVIDIA's card:
   full attention transcribes up to ~24 min in one pass (A100-80GB reference), local attention up to
   ~3 h. Our chunking is a memory workaround; long-form removes chunk stitching and per-chunk overlap
   recompute (and may explain part of TODO-2's chunk-boundary fp32 anomaly). Keep chunking for the
   CPU/Mac local path only.
5. **GPU clustering / capacity arbitrage (TODO-11).** RAPIDS cuML can move the agglomerative/spectral
   clustering off the single CPU thread onto the GPU (alternative to TODO-6's CPU-function split).
   Separately: Modal's per-second + scale-to-zero pricing is optimal at spiky/low volume; at
   sustained high utilization a reserved/committed L4/L40S elsewhere is cheaper per GPU-hour — model
   the crossover once volume is known, don't migrate early.

Sources: picovoice.ai/blog/complete-guide-voice-activity-detection-vad (silence fraction),
github.com/m-bain/whisperX + arXiv:2303.00747 (VAD + batched, 60–70× realtime, no WER loss),
modal.com/docs/guide/concurrent-inputs (`@modal.concurrent` max_inputs/target_inputs),
modal.com/resources/best-sandbox-infrastructure-multi-tenant-ai-apps (concurrency scale),
huggingface.co/nvidia/parakeet-tdt-0.6b-v3 (24 min full / 3 h local attention, single pass),
arXiv:2509.14128 (Parakeet-v3/Canary efficiency); enrolled-vs-unsupervised framing from the 2025
diarization surveys + noto's own decision 0006 (precision-first matching).

## Reference: what's achievable

- Parakeet-TDT-0.6B single-stream: **RTF ~0.02–0.05** (vs our STT ~0.14). Batched: far lower
  (the 3000× regime needs batch≈128–256 on A100/H100, compute-only, no diarization).
- AssemblyAI retail ~$0.12–0.37/audio-hr is a *managed, batched, optimized* service incl. diarization
  + margin — not a like-for-like floor, but the bar to beat by owning + batching the GPU.
- Cost identity to optimize against: **$/audio-hr = container_$/hr × RTF.** Lower RTF (batching, warm
  model, TensorRT) and right-size the container (fewer idle vCPU/GB) — both multiply down the cost.

## What was already done this session (don't redo)
- Warm STT batching within a meeting (model loads once per meeting, not per 120 s chunk):
  `sherpa_engine.go` (`transcribeFiles`, `parseSherpaResults`).
- Per-meeting parallelism: `benchmark/internal/bench/bench.go` `RunMeetings` + `-jobs` flag
  (`benchmark/internal/sample/sample.go`); stt/diarize/e2e synth tests refactored to it.
- Runner profiles/GPU/cost/sweep: `scripts/modal_benchmark.py`; Makefile `model-benchmark`/`model-sweep`.
- Docs: `benchmark/README.md`, `.docs/benchmarks.md` ("Measured: parallelism, per-GPU perf & cost").
- Result: benchmark 19.7 min → ~3.5 min on L4, 100% util; this is the *orchestration* win. The
  *engine/serving* wins above are still open and are where the cost actually is.

## Done 2026-06-10 (second pass) — #1a, #5, and STT∥diar overlap

- **#5 container right-sizing**: `scripts/modal_benchmark.py` `PROFILE_SHAPES` — prod = 4 vCPU /
  8 GiB via `with_options(cpu=, memory=)` (L4 container $1.44 → $1.06/hr); fast/CPU baseline keep
  8/32. Cost estimate + summary now use the actual shape (`cpu_cores`, `memory_gib`).
- **#1a cross-meeting STT batching (benchmark)**: `stt.BatchSTTEngine` optional interface +
  `sherpaEngine.RecognizeBatch` (all meetings' chunks in ONE warm CLI pass, per-meeting fallback
  guard preserved) + `LocalSTT.TranscribeBatch`/`BatchCapable`. The atomic synth bench uses it
  under `BENCH_STT_BATCH=1`, which the runner sets for the `fast` profile; `prod` keeps honest
  per-meeting RTF. This interface is the seam the future persistent server (#1b) satisfies.
- **STT ∥ diarization within a meeting**: `benchmark/e2e/chain.go` `RunChain` and the synth e2e
  test run the two stages concurrently (both consume raw audio; merge is the join). Single-stream
  e2e RTF ~0.50 → ~max(0.10, 0.37)+merge ≈ 0.38 (~25% prod latency/cost cut). `StageTiming.Wall`
  is the measured chain wall; `Total()` returns it when set.
- Open and still the big prize: **#1b persistent batched server**, **#3 diarization stack**,
  **#4 TensorRT/fp16**.

## Done 2026-06-10 (third pass) — warm pyannote engine wired; fp32 measured

- **Warm pyannote-server diarization engine (the #3 fix, wired end-to-end, pending HF token to
  measure):** `scripts/pyannote_diar_server.py` (loads `pyannote/speaker-diarization-community-1`
  ONCE, serves JSONL requests over stdin/stdout, GPU-batched windows by pyannote itself) +
  `internal/platform/providers/diarize/pyannote_server.go` (`pyannote-server` /
  `cuda-pyannote-server` engines: lazy spawn, serialized requests, restart-once-on-crash,
  ctx-cancel kills the process). Unit-tested against a `sh` fake server (handshake, noise
  skipping, error response, crash-restart) — no python needed in `make test`. Modal runner
  installs `pyannote.audio` into the image and passes `HF_TOKEN` as a secret only when
  `BENCH_DIAR_ENGINE=cuda-pyannote-server`. **One-time setup to actually run it:** accept the
  gated repo terms on huggingface.co, `export HF_TOKEN=hf_…`, then
  `BENCH_DIAR_ENGINE=cuda-pyannote-server make model-benchmark`. Expected from published numbers:
  diar RTF 0.37 → ~0.05 on L4 — THE remaining cost fix.
- **fp32 STT on GPU measured (`BENCH_STT_PRECISION=fp32`; default stays int8):**
  - **Speed confirmed: 3× —** batched atomic STT RTF **0.04 vs 0.12** int8 (L4, like-for-like).
    fetch-bench-deps.sh pulls the fp32 export (no fp16 export is published upstream — the
    HF fp16 repo is an empty stub).
  - **VRAM caveat:** fp32 under the `fast` profile's full concurrency OOM'd a 24 GB L4 (BFC arena
    abort in the e2e STT procs; peak 22.5 GB). Single-stream `prod` is fine (peak 8.0 GB,
    exit 0, $0.123 vs $0.134 int8, wall 418 s vs 457 s).
  - **Accuracy anomaly — do NOT flip the default yet:** synthetic e2e WER 50.3% vs 45.5% int8,
    driven by mtg00 collapsing to **82.3% WER** under fp32 (mtg01 24.8%, mtg02 35.8% — deterministic
    across runs). Either an fp32-decode degeneration on that audio or an interaction with the
    120 s chunking; investigate with the LibriSpeech WER bench (`benchmark/stt/libri_test.go`,
    clean non-overlapped metric) before trusting either direction. The synthetic corpus WER is
    overlap-inflated and a poor judge of a few points, but 82% is pathological.

## Done 2026-06-10 (fourth pass) — warm pyannote-server MEASURED; diar RTF 0.37 → 0.04

The serving fix that §3 + the first research pass pointed at is now measured and lands the cost
target. Runs: L4, int8 STT, `cuda-pyannote-server`, full synthetic corpus.

- **Decode fix (required before it ran at all):** the first attempt failed every diarization request
  with `name 'AudioDecoder' is not defined`. pyannote.audio 4.x decodes audio files through
  **torchcodec**, whose `AudioDecoder` silently fails to initialize when FFmpeg's shared libraries
  aren't in the image — and the pipeline *loaded* fine (model load + CUDA init ~14 s), so only the
  per-request file decode broke (GPU stayed ~0 MB). Fix in `scripts/pyannote_diar_server.py`:
  `load_waveform()` decodes the WAV itself (stdlib `wave` for PCM; ffmpeg-binary fallback via
  `NOTO_FFMPEG` for anything else) and hands the pipeline a `{"waveform", "sample_rate"}` dict, which
  bypasses torchcodec entirely. No new image deps (numpy/torch ship with pyannote). The Go unit tests
  (sh fake) are unaffected.
- **Measured (prod, full corpus, run `20260610T163000Z`):**
  | metric | sherpa baseline | pyannote-server | Δ |
  |---|---|---|---|
  | STT WER | 45.5% | 45.5% | 0 (identical) |
  | e2e cpWER | 49.3% | 49.9% | +0.6 pt |
  | e2e DER | 8.8% | 9.6% | +0.8 pt |
  | **diar RTF (atomic)** | **0.37** | **0.04** | **~9× faster** |
  | diar atomic DER (auto speaker count) | ~29% | 9.6% | also more accurate |
  | **prod e2e wall RTF** | ~0.38 | **0.076** | ~5× |
  | **prod $/audio-hr** (L4 @ $1.056/hr) | ~$0.40 | **~$0.08** | ~5× cheaper |
  Both KPI deltas are within the ~1 pt acceptance gate; diar speed beat the ≤0.10 target (0.04).
  Diarize-atomic util 100% @ 2.2 GB; the pipeline batches windows on the GPU as expected.
- **Throughput:** prod single-stream now clears **~13 audio-min per wall-minute** (RTF 0.076), up
  from ~2.6. Below AssemblyAI retail ($0.12–0.37) on the honest single-stream number.
- **Sub-sample-first method (per the perf owner):** confirmed on a 1-meeting `BENCH_HOURS=0.1` run
  (decode works, diar RTF 0.07 incl. unamortized load, $0.03) BEFORE the full-corpus acceptance
  ($0.07) — cheap signal first, scale only once the optimum holds.
- **Open after this:** (a) flip `DIAR_ENGINE` default to `cuda-pyannote-server` + re-run
  `make model-sweep` once fast/benchmark-mode is validated; (b) the warm server serializes requests
  through one process (`e.mu`), so under the `fast` profile concurrent meetings queue — fine for GPU
  feeding, but one meeting's CPU-clustering tail can't overlap the next's GPU work; measure whether a
  warm-server pool is worth the extra ~2.2 GB/proc before building it.

### Benchmark-mode (fast) measured with the warm diarizer — and why it barely beats prod now

Fast profile, e2e-only (`BENCH_QUICK=1`), jobs across meetings, run `20260610T163831Z`:
- **Aggregate RTF ~0.051** (3 meetings concurrent: 1116 s audio in ~57 s wall), GPU **100% util**,
  peak 15.8 GB, KPIs identical to prod (WER 45.5 / DER 9.6 / cpWER 49.9). **$/audio-hr ≈ $0.076**
  (fast container $1.44/hr) — i.e. ~equal to prod's $0.08 despite running 3 in parallel.
- **Why parallelism stopped paying:** (1) the warm pyannote server serializes requests through one
  process (`e.mu`), so 3 meetings' diar queues — GPU stays fed (good util) but wall can't drop below
  the serial diar sum; (2) prod got so cheap that the fat benchmark container (8 vCPU/32 GB =
  $1.44/hr vs prod $1.056/hr) eats the ~1.5× wall gain. The old "fast is 2.5× cheaper" held when diar
  was CPU-heavy; it doesn't with a warm GPU diarizer.
- **The non-quick fast profile OOMs** (22.4 GB on a 24 GB L4): it runs STT-atomic ∥ diarize-atomic ∥
  e2e concurrently, and batched-STT alone nearly fills the card, leaving nothing for the torch
  process to even load the pipeline. With a GPU diarizer, benchmark throughput must be measured as
  e2e-across-meetings (one warm server), not the 3-package pileup. **Action:** gate `concurrent_pkgs`
  off (or force `--quick`) when `DIAR_ENGINE` is a `*-server`, and/or right-size the fast container.
- **To make benchmark mode actually scale on one GPU:** a small **pool of warm diar servers**
  (2–3 × ~2.2 GB fits L4) to break the serialization, + a warm STT server to remove the per-meeting
  cold load, + right-size the fast container now that clustering moved onto the GPU. Measure the pool
  size against VRAM before committing (this is the `@modal.concurrent` / TODO-3/9 direction).

## Done 2026-06-10 (fifth pass) — warm STT server unblocked + MEASURED; both warm servers are the GPU default; fast container right-sized

The warm STT server (TODO-3, STT half) was wired but had NEVER run — every request died at
`import sherpa_onnx failed: libasound.so.2: cannot open shared object file`. Same class of bug as
the fourth-pass pyannote torchcodec/FFmpeg fix: a missing system shared library in the Modal CUDA
image. The sherpa-onnx Python wheel bundles audio-device C++ that `dlopen`s ALSA at IMPORT time, and
`nvidia/cuda:12.4.1-cudnn-devel-ubuntu22.04` ships no sound stack (we never touch a mic — the import
just has to resolve the linkage).

- **Fix:** `scripts/modal_benchmark.py` — `image.apt_install("libasound2")` inside the
  `NEEDS_SHERPA_PY` block, placed AFTER the heavy wheel `pip_install` so it's a small new apt layer
  that doesn't invalidate the ~5 GB cached pyannote/sherpa wheel layers. (`libasound2` is the 22.04
  package; on 24.04 it's `libasound2t64`.) The Go engine needed no change.
- **Measured (prod, full synthetic corpus, int8, both warm servers, run `20260610T170822Z`, $0.062):**
  | metric | CLI `cuda-parakeet` | warm `cuda-parakeet-server` | Δ |
  |---|---|---|---|
  | **atomic STT RTF** | 0.08 | **0.06** | ~25% faster (cold load amortized) |
  | STT WER overall | 45.5% (runs ranged 43.1–45.5) | 46.9% | +1.4 pt |
  | mtg00 / mtg01 / mtg02 STT WER | 71.5 / 13.3 / 44.8 | 75.0 / 9.5 / 48.7 | ±~4 pt per mtg |
  | diar DER / RTF (warm pyannote) | — | 9.6% / 0.04 | unchanged from 4th pass |
  | e2e cpWER | 49.9 (4th pass) | 51.3 | +1.4 pt |
  Both stages warm; GPU 100% util @ 5.7 GB; pipeline ≈ $0.07–0.08/audio-hr.
- **NOT bit-exact — carried forward as the TODO-3 follow-up.** The doc's earlier claim that the
  server is "byte-identical" to the CLI is NOT borne out: WER moved +1.4 pt overall and ±~4 pt per
  meeting. Context: the CLI itself varies 43.1–45.5% run-to-run (GPU/cuDNN nondeterminism), so 46.9
  is only marginally outside that band, and mtg01 actually IMPROVED — this is per-utterance decode
  drift, not a uniform regression. Prime suspect: the server's `load_waveform()` reads the WAV with
  stdlib `wave` and scales by 32768.0, whereas the sherpa CLI uses its own loader — tiny input-feature
  differences flip a few greedy-decode tokens, loudest on the hard overlapped meetings. **Before
  trusting it as bit-exact:** either reconcile the loader (match sherpa's WAV read/scale exactly) or
  run it 2–3× and accept the drift the way the warm diarizer's +0.8 DER was accepted.
- **Both warm servers are now the GPU default:** `STT_ENGINE` → `cuda-parakeet-server`, `DIAR_ENGINE`
  → `cuda-pyannote-server`. A bare `make model-benchmark` now runs the cheap warm pipeline; override
  `BENCH_STT_ENGINE=cuda-parakeet` / `BENCH_DIAR_ENGINE=cuda-pyannote` for the dependency-free CLI
  baseline. (CPU baseline behavior is unchanged — it always required explicit `sherpa-*` engines.)
- **fast container right-sized 8 vCPU/32 GiB → 6/16 GiB** (`PROFILE_SHAPES["fast"]`). Justified now
  that clustering is GPU-resident and the STT server is one process (not N parallel CLI decoders);
  the corpus is small so 16 GiB RAM is ample, and system RAM does NOT touch the 24 GB L4 VRAM
  ceiling. `DEFAULT_SHAPE` was decoupled to an explicit 8/32 GiB so the CPU baseline keeps the big
  shape. Pure cost cut (~15% off the fast container), no perf claim.
- **`concurrent_pkgs` gated off for `*-server` engines** was ALREADY in the runner (lines ~556–564)
  — confirmed in place; it prevents the 3-package VRAM OOM the fourth pass flagged.
- **Warm-server POOL considered and rejected.** Fast mode already hits 100% GPU util through one
  queued warm server (4th-pass measurement), so serialization is NOT the wall ceiling — a pool only
  stacks resident models and adds VRAM pressure for ~no gain. The remaining benchmark-cost lever was
  the container right-size (done), not parallelism. (If a real-meeting corpus ever shows GPU idle
  during a CPU-clustering tail, revisit — that's the only scenario a pool helps.)
- **Open after this:** (a) the TODO-3 bit-exactness reconcile above; (b) deployed `@modal.cls`
  cross-request serving (TODO-3/TODO-9); (c) the precision lever (TODO-2/int8→bf16) is now the
  biggest single-stream STT win left.

## Done 2026-06-10 (sixth pass) — prod vs fast + GPU sweep on a real ~1 h corpus; precision unblocked; loader reconciled

This pass MEASURED the questions the prior passes reasoned about: is fast still worth it, is L4 still
the right GPU now that the GPU is saturated, and is fp32 safe. To measure honestly at scale the
synthetic corpus was grown 3→**10 meetings = 3 747 s = 1.04 h** (deterministic, `--seed 7`; the first
3 meetings reproduce the old 1 115.8 s corpus exactly). New knob: `BENCH_SYNTH_MEETINGS`
(`scripts/modal_benchmark.py`, threaded as a `run_remote_benchmark` param so it reaches the Modal
container; default 3) + `BENCH_REBUILD_SYNTH` (Makefile → `--rebuild-synth`). The corpus lives in a
persistent Modal volume, so a non-default size must pass rebuild and be restored to 3 after (done).

- **prod vs fast (L4, 1.04 h corpus, quick e2e) — fast LOST; prod is the profile now:**
  | profile | jobs | container | e2e wall | RTF | $/audio-hr | GPU util | WER/DER/cpWER |
  |---|---|---|---|---|---|---|---|
  | **prod** | 1 | 4 vCPU / 8 GiB ($1.056/hr) | **240 s** | **0.064** | **$0.068** | 100 % | 51.7 / 9.1 / 54.3 |
  | fast | 8 | 6 vCPU / 16 GiB ($1.216/hr) | 315 s | 0.084 | $0.102 | 100 % | 51.7 / 9.1 / 54.3 |
  Fast is **1.31× the wall and 1.51× the cost** of prod. Root cause: the warm STT + diar servers each
  serialize requests through one process (`e.mu`); a SINGLE prod stream already saturates the GPU
  (100 % util, STT chunk-batching + pyannote window-batching keep it fed), so jobs=8 just queues 8
  meetings at the mutex AND pays for the bigger fast box. **Scores identical** → concurrency is
  score-neutral, confirmed again. Action taken in the doc/guidance: prefer `prod`; treat `-jobs`/fast
  as legacy. Real throughput = horizontal containers (Modal autoscale, linear cost ÷ wall) or
  server-side dynamic batching (TODO-9), NOT `-jobs`.
- **GPU sweep (prod, same 1.04 h corpus) — saturation finally makes L40S pay; A100 still dominated:**
  | GPU | $/hr (gpu+4c+8g) | e2e wall | RTF | $/audio-hr | vs L4 speed | vs L4 cost | util |
  |---|---|---|---|---|---|---|---|
  | **L4** | $1.056 | 240 s | 0.064 | **$0.068** | 1.0× | 1.0× | 100 % |
  | **L40S** | $2.206 | **121 s** | 0.032 | $0.071 | **2.0× faster** | +5 % | 98 % |
  | A100-40GB | $2.356 | 223 s | 0.059 | $0.140 | 1.08× | +106 % | 94 % |
  The old "A100 is Pareto-dominated" verdict HOLDS (Ampere int8-ONNX is weak; 2× the cost for ~no
  speed). What CHANGED with the warm servers: the GPU is now the bottleneck, so **L40S's ~2× throughput
  converts to a real 2× latency cut at ~break-even cost** — it was pure waste in the old CPU-bound
  regime. Guidance: **L4 = default (cheapest); L40S = fast iteration / latency-sensitive prod
  (2× turnaround, +5 %); A100/H100 = no** (H100 only ever pencils with TensorRT, TODO-5).
- **Economics — 1 h and 100 h of audio, vs paid:**
  | path | 1 h wall | 1 h cost | 100 h wall (1 container) | 100 h cost |
  |---|---|---|---|---|
  | L4 prod | 3.8 min | **$0.068** | 6.4 h | **$6.77** |
  | L40S prod | 1.9 min | $0.071 | 3.2 h | $7.12 |
  | A100 prod | 3.6 min | $0.140 | 5.9 h | $14.00 |
  | AssemblyAI retail (managed) | — | $0.12–0.37 | — | $12–37 |
  So benchmarking **1 h of meeting costs us ~$0.068 (L4) vs ~$0.12–0.37 paid** — ~2–5× cheaper even
  single-stream, before any multi-tenant amortization. **100 h of audio ≈ $6.77 of GPU** on L4. Cost is
  fixed by RTF (per-GPU-second), so horizontal scaling cuts the 100 h WALL by K containers at the SAME
  $6.77; the 100 h-wall column is the one-container serial figure.
- **int8 vs fp32 on clean read speech (CPU, LibriSpeech, 170 clips, 2 953 ref words):** int8 **2.07 %**
  WER, fp32 **1.69 %** — both pristine, fp32 marginally cleaner. Proves fp32 is NOT what breaks mtg00
  (82.3 % under fp32) → that collapse is overlap/chunk-boundary, and the **precision lever is
  green-lit** (TODO-2 resolved). Also re-confirms the synthetic 45–52 % WER is corpus overlap-inflation,
  not a model failure (clean speech is ~2 %).
- **Loader reconcile (TODO-3b):** `scripts/parakeet_stt_server.py` `load_waveform()` now goes through
  `sherpa_onnx.read_wave` (the CLI's own C++ reader) first, stdlib-wave/ffmpeg only as fallback. WER
  was UNCHANGED (46.9 %) → the loader was never the drift source; kept as hygiene. Verified by the 1 h
  prod/fast runs whose KPIs match across the board.
- **Wall-time contribution now (single-stream prod):** STT ≈ **the entire critical path** — measured
  prod RTF 0.064 ≈ STT's atomic 0.06; diar's 0.04 runs concurrently (`RunChain`) and is fully hidden
  underneath, contributing ~0 to wall. So the ONLY single-stream lever left is STT speed:
  **precision (fp16/bf16, ~3×) → TensorRT → long-form Parakeet** (which also likely fixes the mtg00
  chunk bug). Everything else (diar, merge) is already free under the STT shadow.
- **Cleanup:** local + Modal-volume corpora restored to the canonical 3 meetings; `BENCH_SYNTH_MEETINGS`
  defaults to 3 so nothing changed for a normal run. Sixth-pass Modal spend ≈ $0.45.

## Done 2026-06-10 (seventh pass) — the 3× STT win LANDED: fp32 on GPU + L40S + prod are the new defaults

The sixth pass green-lit fp32; this pass MEASURED it head-to-head, found it's a clean ~3.6× win with
no accuracy cost, and made it (plus L40S and prod) the default. A bare `make model-benchmark` went
from L4/int8/fast (~$0.068/audio-hr, 1 h in ~6 min) to **L40S/fp32/prod (~$0.020/audio-hr, 1 h in
~33 s)** — ~3.4× cheaper and ~10× faster wall, same-or-better accuracy.

- **fp32 vs int8, clean L40S head-to-head** (prod, warm `cuda-parakeet-server`, 10-meeting/1.04 h
  corpus, quick e2e):
  | precision | e2e wall | $/run | $/audio-hr | WER / DER / cpWER |
  |---|---|---|---|---|
  | int8 | 121.0 s | $0.074 | $0.071 | 51.7 / 9.1 / 54.3 |
  | **fp32** | **33.4 s** | **$0.021** | **$0.020** | **50.3** / 9.6 / 54.8 |
  **3.6× faster, 3.6× cheaper, WER *better* by 1.4 pt** (cpWER +0.5 / DER +0.5 are diar-side noise —
  STT precision can't move diar; run-to-run band). GPU 100 % util, peak 8.4 GB (fp32 fits `prod`
  comfortably on a 24 GB L4, tons of room on L40S's 48 GB). The full-suite fp32 run also showed STT
  atomic RTF 0.01 and per-meeting STT WER with **no collapse** (overall 47.2 % over 11 015 words ≈
  int8) — the sixth-pass libri result (fp32 1.69 % vs int8 2.07 %) plus this kills the old "mtg00
  82 %" worry: it was a transient/version artifact, not reproducible, and needed NO chunk-overlap fix.
- **Diar does NOT become the new bottleneck on L40S.** Predicted concern was that 3× STT would expose
  diar (RTF 0.04) as the floor and cap the e2e win at ~30 %. It didn't: on L40S diar is also fast
  (atomic RTF ~0.01), so it stays hidden under STT's concurrent branch (`RunChain`) and the e2e win is
  the full 3.6×. On a slower card diar would re-emerge — revisit per-GPU.
- **New defaults (`scripts/modal_benchmark.py` + Makefile `model-benchmark`):** GPU `L4→L40S`,
  profile `fast→prod`, precision default is now **accelerator-aware** (`fp32` on cuda/gpu/modal,
  `int8` on cpu — int8 is a CPU optimization, pointless on the GPU). Each is overridable
  (`BENCH_GPU`, `BENCH_PROFILE`, `BENCH_STT_PRECISION`). The `fast`/`-jobs` path and L4 stay supported
  (CPU baseline, cheapest-absolute run, A/B history) — just no longer the default.
- **Builder ghost-file bug fixed (`build_synthetic_meetings.py`):** the benches enumerate meetings by
  globbing `*.words.json` (`benchmark/e2e/synth_test.go`, `stt/synth_test.go`), but the builder never
  cleaned its output dir — so rebuilding a SMALLER corpus left `mtgNN` files from a larger prior build
  that the glob silently re-added (this is why the sixth-pass "restore to 3" left 10 in place). The
  builder now clears `mtg*`/`meetings.json` before writing. Local + Modal-volume corpora rebuilt clean
  to 3 meetings.
- **Updated economics (fp32, the new default path):**
  | path | 1 h wall | 1 h cost | 100 h wall (1 container) | 100 h cost | vs paid (100 h) |
  |---|---|---|---|---|---|
  | **L40S fp32 prod** | **~33 s** | **$0.020** | 0.9 h | **~$2.0** | vs $12–37 |
  | L4 int8 prod (old default) | 3.8 min | $0.068 | 6.4 h | $6.8 | |
  So benchmarking **100 h of meetings now costs ~$2 of GPU** (cost is RTF-bound, so horizontal
  containers cut the wall further at the same $). ~6–18× under AssemblyAI retail, single-stream.
- **Still open / next:** (a) true fp16 export (half VRAM, same speed — a nicety, not a blocker);
  (b) `@modal.concurrent` dynamic batching for multi-tenant throughput (TODO-9); (c) VAD-gating
  (TODO-7) and enrolled-speaker attribution (TODO-8) remain the biggest *production* (real-meeting)
  levers, invisible in this synthetic corpus. Seventh-pass Modal spend ≈ $0.35.

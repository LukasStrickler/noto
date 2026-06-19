# Benchmarks

Reader-facing summary of noto's benchmark work. It links to the two reproducible suites under
[`benchmark/`](../benchmark/) and reports their headline numbers, the engineering decisions they
justified, and the failure modes they expose.

> **Read these as signals, not published benchmarks:** one machine, one degradation recipe / seed,
> ~24–52 speakers, and (for the persona suite) ground-truth diarization. Where a number is
> fragile, this document says so. Corpus provenance and licensing are in
> [§4 Datasets & provenance](#4-datasets--provenance).

- **What's measured:** [`benchmark/voiceprint/`](../benchmark/voiceprint/) (model selection) and [`benchmark/identity/`](../benchmark/identity/) (cross-meeting accuracy)
- **Hardware:** AMD EPYC 7702P (64-core / 128-thread, Zen 2), **CPU-only — no GPU**
- **Runtime:** `onnxruntime` with `intra_op_num_threads=4`; the cross-meeting suite runs through noto's production Go matcher
- **Production model:** ECAPA-TDNN-512 (WeSpeaker ONNX), 192-d embeddings

For the speech compute benchmark loop, use [`speech-compute.md`](./speech-compute.md).
GPU cost optimizations are adopted only through `noto bench run` → `noto bench compare`
→ `noto bench ledger`; raw Modal summaries are evidence, not adoption records.

## Speech compute benchmark gate

The speech compute gate measures cost, GPU utilization, stage attribution, WER, DER, and cpWER.
The current economic target is to beat **`$0.0194 / processed audio-hour`** on L40S batch
without WER/DER/cpWER regression.

Developer loop:

```bash
export NOTO_CONFIG_DIR=/tmp/noto-bench-dev
export NOTO_AGENT_ID=<agent-id>

./bin/noto bench preflight --suite smoke@2026-06-18 --tier smoke --mode batch_queue --json
./bin/noto bench run --suite smoke@2026-06-18 --tier smoke --mode batch_queue --json
./bin/noto bench compare --baseline <run_id> --candidate <run_id> --json
./bin/noto bench audit --run <run_id> --json
```

Latest validation, 2026-06-18:

| Check | Result |
| --- | --- |
| local `go test ./...` | pass |
| integration-only smoke | pass |
| live L40S synthetic smoke | raw Modal pass, `$0.0351`, 57.355 s, WER 50.9, DER 9.1, cpWER 54.1 |
| live image hardening | `ffmpeg` shared libraries added after a torchcodec/libavutil failure |
| AMI ramp | blocked until image-fixed smoke returns normal `noto bench` artifacts |

Absolute latency / RTF numbers are tied to the CPU above; **accuracy** numbers (EER, rank-1,
false-merge rate) are hardware-independent. Reproduce with the exact commands in each section.

---

## 1. Embedding-model selection — `benchmark/voiceprint/`

**Question:** which speaker-embedding model should ship, given CPU-only inference and noisy,
short, real-meeting audio?

**Method.** Six candidates were measured for load time, per-segment latency (3 s), sustained
throughput / **RTF** (compute ÷ audio) over a ~5-min concatenated clip, footprint, and
**discrimination** (EER / AUC / mean same- vs different-speaker cosine) on a 24-speaker ×
4-clip LibriSpeech `validation.clean` trial set (144 genuine, 4 416 impostor pairs). Two
conditions: **clean**, and a deliberately **hard** condition — 3 s turns + reverb (RT60 0.2 s)
+ babble @ 8 dB SNR. Clean LibriSpeech saturates near ~2 % EER, so the **hard** column is what
actually separates the field.

| id | model | dim | size | RTF | EER clean | **EER hard** | decision |
|----|-------|-----|------|-----|-----------|--------------|----------|
| `ecapa` | ECAPA-TDNN-512 | 192 | 24 MB | 0.029 | 2.08 % | **7.96 %** | **shipped** — fastest, lightest, 192-d, best robustness/compute |
| `resnet293` | ResNet293-LM | 256 | 110 MB | 0.152 | 2.08 % | **7.48 %** | only ~0.5 pp better at 7× compute — not worth defaulting |
| `resnet34` | ResNet34-LM | 256 | 26 MB | 0.035 | 2.07 % | **9.99 %** | dropped — *dominated*: slower than ecapa **and** worse hard EER |
| `resemblyzer` | GE2E 256-d | 256 | 17 MB | 0.052 | 5.42 % | **20.4 %** | dropped — far behind on both |
| `campp` | CAM++ | 512 | 28 MB | 0.034 | 46.1 % | — | dropped — ONNX export wouldn't discriminate under any frontend variant |
| `titanet` | TitaNet-large | 192 | ~100 MB | — | — | — | dropped — NeMo/torch dependency; resnet293 covers the SOTA tier in pure ONNX |

**Findings**

- **`ecapa` ships.** Fastest (RTF 0.029), lightest (24 MB), its 192-d output already matches the
  stored profile-vector dim, and it has the best robustness per unit of compute.
- **`resnet34` is dominated** — slower than ecapa *and* worse where it matters (hard EER 9.99 %
  vs 7.96 %). Its better *clean* VoxCeleb number does not survive noise + short turns.
- **`resnet293` is the only defensible "more,"** and only marginally (7.48 % vs 7.96 %), at
  ~7× the compute and 110 MB.
- All shipped models run **3–30× faster than real time on CPU**, and `identify` only embeds a
  few windows per speaker (seconds per meeting).

**Reproduce** → [`benchmark/voiceprint/README.md`](../benchmark/voiceprint/README.md)

```bash
cd benchmark/voiceprint
uv venv --python 3.11 venv && source venv/bin/activate
uv pip install torch torchaudio --index-url https://download.pytorch.org/whl/cpu
uv pip install numpy onnxruntime soundfile huggingface_hub resemblyzer librosa scipy
python bench.py        # full table + EER → results.json
python iso.py ecapa    # isolated load / RAM / throughput per model
```

**Caveat:** one recipe, one seed, 24 speakers — a *signal*, not a final EER. The ranking
(ecapa ≫ resnet34, ecapa ≈ resnet293) was stable and decisive enough to curate on.

---

## 2. Cross-meeting persona accuracy — `benchmark/identity/`

**Question:** does noto link the **same person to the same profile across different meetings**,
and avoid confusing different people? This is the product-critical metric, and LibriSpeech
(one speaker per clip, read speech) cannot answer it.

**Method.** A curated subset of the [AMI Meeting Corpus](https://groups.inf.ed.ac.uk/ami/corpus/)
— **52 speakers across 39 meetings**, each speaker appearing in 3 sessions (1 enroll + 2 test).
AMI records the **same participants across a session series** with ground-truth RTTM diarization
keyed by **global participant IDs** that are consistent across sessions, giving an unambiguous
"same person?" answer. Evaluation runs through noto's **production** embedding + matching code
(`internal/platform/providers/speaker` → `internal/core/speakers`) on the single-channel
`Mix-Headset` mix, using ground-truth turns so it isolates the part noto owns — voice
**embedding + cross-meeting matching** — from the upstream diarizer. See
[§4](#4-datasets--provenance) for the full dataset card.

### Deep benchmark — 52-speaker gallery, 104 test personas, 5 304 impostor pairs

Numbers use the **robust (medoid-anchored) centroid** that is the default aggregation;
pre-change plain-`mean` figures are shown in parentheses.

| metric | value |
|---|---|
| identification rank-1 (rank-5) | **104/104 = 100.0 %** (100 %) |
| verification EER | **0.00 %** @ 0.623 *(mean: 0.09 %)* |
| score separation | genuine min **0.623** > impostor max **0.596** — clean gap *(mean: 0.568 < 0.671, overlap)* |
| shipped `auto 0.70 / pend 0.55` | true-link auto **99.0 %** *(96.2 %)* · 100 % surfaced · impostor ≥auto **0.00 %** |
| calibrated `0.58 / 0.50` | true-link auto 100 % · impostor ≥auto 0.06 % — **not adopted** (starts false-merging) |

**Headline:** every test persona's nearest profile in a 52-way gallery is the right person,
and the strongest impostor stays **below** the shipped auto threshold ⇒ **zero false
auto-merges**, while pending surfaces **100 %** of true links for one-click confirm.

### Condition sweep — where it breaks

Re-embeds the same 52 speakers under realistic degradations:

| condition | rank-1 | EER | stranger-FAR@0.55 | verdict |
|---|---|---|---|---|
| baseline (clean GT turns) | 100.0 % | 0.09 % | 5.13 % | reference |
| short speech, 6 s | 94.2 % | 1.84 % | 3.85 % | mild |
| short speech, 3 s | 93.3 % | 1.92 % | 3.85 % | mild — ID survives |
| **diarization noise, 15 %** | 93.3 % | 2.88 % | **39.74 %** | open-set collapses |
| **diarization noise, 30 %** | **58.7 %** | 9.62 % | **62.82 %** | catastrophic |
| **telephone band, 8 kHz** | 100.0 % | 0.05 % | **52.56 %** | ID fine, rejection collapses |

**The embedder is not the bottleneck — the pipeline around it is.** Identification shrugs off
short speech and even telephone-band narrowing. Two things actually break it: **(a) upstream
diarization speaker-confusion** (poisons the enrollment centroid; stranger false-accepts jump
5 % → 40 %), and **(b) channel mismatch / open-set rejection** (8 kHz keeps rank-1 at 100 % but
lets strangers in 53 % of the time). Neither is visible in the happy-path numbers.

### Engineering decisions this data justified

| change | effect | status |
|---|---|---|
| **Robust centroid** (medoid-anchored, bimodality-guarded) | closes genuine/impostor overlap (EER 0.09 % → 0.00 %), auto-recall 96 % → 99 %, stranger FAR 5.13 % → 3.85 % | **default** in `LocalEmbedder.EmbedSpeakers` |
| **Top-1-vs-top-2 margin gate** (`MatchConfident`, margin 0.05) | downgrades same-gender collisions from auto → pending; **0 %** recall cost on AMI | **live** in `matchSpeakers` |
| **Min-enrollment-speech gate** (`MinEnrollSpeech=3 s`) | holds auto-confirm when a speaker contributed too little voice | **live** |
| **Precision-safe thresholds** (`auto 0.70 / pending 0.55`) | LibriSpeech-calibrated 0.58/0.50 raises recall but starts false auto-merging | **kept** — not recalibrated |
| **AS-Norm** score normalization (`ScoreASNorm`) | marginal on AMI (scores already separate) | implemented + unit-tested, **not enabled** live |

**Reproduce** → [`benchmark/identity/README.md`](../benchmark/identity/README.md)

```bash
python3 benchmark/identity/fetch.py                                              # ~1.7 GB into ami/
go test ./benchmark/identity/ -run Persona -v                                    # quick ES2002 checks
PERSONA_DEEP=1 go test ./benchmark/identity/ -run Benchmark50 -v -timeout 900s   # full 52-speaker deep bench (~200 s)
PERSONA_DEEP=1 go test ./benchmark/identity/ -run Persona_Conditions -v          # degradation sweep (~14 min)
PERSONA_DEEP=1 go test ./benchmark/identity/ -run Persona_SOTA -v                # robust × AS-Norm comparison
```

(Needs the model installed via `noto speaker-model download`, or the dev copy in
`tools/voiceprint/`; override with `ECAPA_MODEL` / `ORT_LIB` / `FFMPEG`.)

---

## Modal GPU benchmark runtime

To **run** the remote suite (suites, `-hours`/`-seed` subsampling, where results
land), see [`benchmark/README.md` → Running on Modal](../benchmark/README.md#running-on-modal-gpu--cpu).
This section covers the runtime *policy* the Modal path implements.

The Modal path must measure accuracy separately from infrastructure latency. Keep benchmark
audio staged near the GPU, keep model weights cached separately from user audio, and report cold
start costs as first-class benchmark metadata.

### Current default and runtime target (2026-06-10)

`make model-benchmark` now defaults to the routine performance signal:

- one **L40S**
- `prod` profile
- fp32 Parakeet STT
- warm Parakeet and pyannote in-run servers
- `BENCH_QUICK=1`, so the suite runs quick e2e only and does not repeat atomic STT + diarization

On the rebuilt 10-meeting synthetic corpus, `BENCH_HOURS=1` and seed 1 select 9 meetings
(~0.94 h audio). The measured current default is **55.6 s command wall / $0.034**, with
WER **45.2**, DER **9.0**, cpWER **48.1**, and peak VRAM **8.4 GB**. The old full suite repeats
atomic STT and atomic diarization before e2e; on the same corpus it is **111.0 s / $0.068**.
Routine runs should therefore stay quick e2e unless you explicitly need stage attribution.

The current runtime-only optimization target is pyannote diarization. Atomic diarization is
**41.8 s** on the same ~0.94 h sample, while atomic STT is **22.6 s**. Experimental cross-meeting
e2e batching is scaffolded (`BENCH_E2E_BATCH=1`, `BENCH_BATCH_SIZE=N`) but is not a default: batch=4
only improved command wall by ~4 % and used **39.1 GB** VRAM because pyannote still loops wav files
internally. The next pass should add pyannote stage timing, then optimize the largest actual runtime
bucket. Warm endpoints and persistent deployed startup paths are useful for first-request latency,
but they are not the next lever for reducing actual one-GPU benchmark runtime.

### Measured: parallelism, per-GPU perf & cost (2026-06-10)

Sweep of the synthetic chained pipeline (`--quick` e2e, the **full** synthetic corpus =
**3 meetings, 1115.8 s = 18.6 min = 0.31 h** of audio, 4 speakers each; local sherpa
Parakeet-TDT int8 STT + pyannote diarization), single-stream **prod** (`-jobs=1`) vs
parallel **fast** (`-jobs=3`). Each row is one pass over that 0.31 h. (This is a small
corpus — the *relative* ranking is robust, but widen it with `BENCH_HOURS`/AMI before
trusting absolute RTF to two digits.) Scores were **identical across every GPU and profile**
(cpWER ≈ 49.3–49.8%; the sub-1-pt spread is int8 op-ordering, not the profile) — concurrency
changes only wall time and utilization, never accuracy. `est_$` is the runner's GPU+CPU+mem
estimate; RTF = wall ÷ audio (lower is faster than real time); latency = wall ÷ 3 meetings.

| GPU | profile | wall | GPU util | GPU mem | est_$ | RTF | per-meeting latency |
|---|---|---|---|---|---|---|---|
| **L4** | prod (1 stream) | 474 s | 71% | 4.4 GB | $0.19 | 0.42 | 158 s |
| **L4** | fast (parallel) | 208 s | 100% | 13.2 GB | **$0.08** | 0.19 | 69 s |
| **L40S** | prod | 405 s | 76% | 4.6 GB | $0.29 | **0.36** | 135 s |
| **L40S** | fast | **164 s** | 100% | 13.9 GB | $0.12 | **0.15** | 55 s |
| **A100-40GB** | prod | 605 s | 73% | 4.7 GB | $0.46 | 0.54 | 202 s |
| **A100-40GB** | fast | 263 s | 100% | 13.8 GB | $0.20 | 0.24 | 88 s |

What the numbers say:

- **Parallelism is a free win.** `fast` is ~2.3–2.5× faster *and* cheaper than `prod` on every
  GPU (same compute, less wall time, and Modal bills by time), and saturates the card (100% util,
  ~13–14 GB of 23+ GB). Before this work the same suite ran sequentially at 71% peak util in
  ~19.7 min; the parallel L4 run is ~3.5 min.
- **A100-40GB is Pareto-dominated for this workload** — *slower and more expensive* than both L4
  and L40S. int8 inference of small models plus CPU-bound work (ffmpeg decode, feature extraction,
  clustering, subprocess orchestration) does not use the A100's fp/HBM advantage; the Ada-class
  **L40S** (INT8 tensor cores + high clock) is the right match. Reach for an A100/H100 only if the
  models grow large or move to fp16.
- **L40S is the speed leader** (fast 164 s; best single-stream latency, RTF 0.36). **L4 is the
  value leader** (fast $0.08/run, only ~25% slower than L40S).

### Current recommendations

| Use case | Pick | Why |
|---|---|---|
| **Default benchmark / TDD loop** | **L40S + `prod` + `BENCH_QUICK=1`** | Current measured default: ~55.6 s / $0.034 for ~0.94 h audio, no duplicate atomic passes. `make model-benchmark`. |
| **Stage attribution** | **`BENCH_QUICK=0` on the same corpus** | Runs atomic STT, atomic diarization, and e2e. Use only when you need the split; it roughly doubles rental time. |
| **Experimental cross-meeting batch path** | **`BENCH_E2E_BATCH=1 BENCH_BATCH_SIZE=N`** | Correctness seam exists, but it is not a throughput win until pyannote truly batches internally. |
| **One-user production latency shape** | **`prod`** (`-jobs=1`) | Production wants one meeting as fast as possible on one GPU. Avoid `fast`/`-jobs` for this warm-server path; it queues at the server mutex and costs more. |
| **Avoid** | A100/H100 for the current int8 stack | Slower *and* pricier here; revisit only for large/fp16 models. |

Re-run this characterization with `make model-sweep` (or
`scripts/modal_benchmark.py sweep --gpus … --profiles prod,fast`) whenever the models or runtime
change — the cost/perf ranking is workload-specific and was verified empirically, not assumed.

### Cost levers (2026-06-10, second pass)

Three structural cuts on top of the parallelism work above — none change any score:

- **STT ∥ diarization within a meeting.** The e2e chain (`benchmark/e2e/chain.go` `RunChain`,
  mirrored in the synth e2e test) runs STT and diarization **concurrently** — both consume only
  the raw audio; the merge is the first join point. Diarization is the long, largely CPU-bound
  stage (RTF ~0.37) while STT leans on the GPU (RTF ~0.10), so the meeting's wall drops from the
  stage *sum* (~0.50 RTF) to ~max(stt, diar) (~0.38 RTF) — a ~25% single-stream cut, which is the
  production path's shape too. `StageTiming.Wall` records the true chain wall; per-stage RTFs are
  still attributed.
- **Cross-meeting STT batching in the atomic bench** (`BENCH_STT_BATCH=1`, set automatically by
  the `fast` profile). `LocalSTT.TranscribeBatch` + the optional `stt.BatchSTTEngine` interface
  (implemented by the sherpa engine) feed **every meeting's chunks through ONE warm CLI pass** —
  one model load + CUDA-context init per *run* instead of per *meeting*. Per-meeting RTF is not
  attributable in this shape, so `prod` keeps the per-meeting path. The same optional interface is
  the seam a future persistent batched inference server will satisfy.
- **Per-profile container shapes** (`scripts/modal_benchmark.py` `PROFILE_SHAPES`). CPU+memory
  were ~44% of an L4 container's bill at the old fixed 8 vCPU / 32 GiB. `prod` now provisions
  4 vCPU / 8 GiB (container $/hr drops $1.44 → $1.06 on L4); `batch` scales from that floor with
  the pooled engine count (~1.2 GiB/engine, ~0.4 cores/job — measured at jobs=10: 4 vCPU/16 GiB
  runs the full 14-engine pool at the same wall as 8/28); `fast` keeps the big shape its parallel
  decoders need; CPU baselines keep the big shape because there the CPU is the compute.
  `summary.json` reports `cpu_cores`/`memory_gib`, echoes every forwarded tuning knob under
  `"knobs"`, and the cost estimate uses the actual shape.

Third-pass additions (same date):

- **Warm pyannote diarizer engine** (`BENCH_DIAR_ENGINE=cuda-pyannote-server`): a long-lived
  `pyannote.audio` process (`scripts/pyannote_diar_server.py`) behind the `SegmentEngine` seam —
  pipeline loads once per run, windows GPU-batched by pyannote itself. This targets the dominant
  cost (diarization ≈ 79% of single-stream e2e wall; published pyannote-native numbers are ~5–10×
  our sherpa CLI integration). The benchmark now uses the ungated community mirror path by default,
  with model files cached in the Modal model volume.
- **Historical third-pass precision finding:** `BENCH_STT_PRECISION=fp32` measured **3× faster STT
  on L4** (batched RTF 0.04 vs 0.12 — int8's dequant overhead is a CPU optimization, not a GPU one).
  Later passes cleared the accuracy concern and fp32 is now the GPU default; int8 remains useful for
  CPU/Mac paths. Details in `BOTTLENECK.md`.

Later passes changed the defaults: fp32 is now the GPU default, L40S + `prod` is the benchmark
default, and quick e2e is the routine default.

**Eighteenth pass (2026-06-12) — STT serving v2 and the corrected bottleneck.** An experimental
NeMo-backed warm STT server (`BENCH_STT_SERVER=v2`, same JSONL protocol and Go seam as the sherpa
one, but truly batched: one padded encoder+decoder pass per multi-chunk request) measured
**accuracy-neutral on the pinned 30-meeting AMI corpus (WER 23.5 / DER 11.3 / cpWER 31.1 vs
23.9 / 11.3 / 31.5)** and lifted warm per-stream STT from ~120× to **355–543×** realtime, freeing
~10 GB VRAM. The $/audio-hr headline did **not** move (best v2 run $0.0184 vs the $0.0170
frontier): with STT made nearly free, aggregate throughput only rose 133×→138×, which corrects the
earlier "STT FLOP ceiling" diagnosis — **the per-card ceiling is the multiprocess pyannote diar
pool** (~25× per engine under 10-way co-residency vs 136× uncontended; separate CUDA contexts
time-slice the card). bf16 autocast on the STT encoder is WER-identical (a VRAM knob, not a speed
knob); CUDA MPS on Modal and pool oversubscription (`jobs > diarN`) were measured and killed. The
path below $0.01/audio-hr is therefore a **batched single-context diarization server** (the same
v2 move applied to pyannote) plus the TRT/fp16 embedding export — tracked, with the full run
table, in [`BOTTLENECK.md`](../BOTTLENECK.md).

**Nineteenth pass (2026-06-12) — diar serving v2 falsifies the context theory.** That
single-context diarization server was built (`BENCH_DIAR_SERVER=v2`: one warm pyannote process,
ten worker threads each holding a pipeline replica on its own CUDA stream, shared by the whole
diar pool through a concurrent id-demultiplexing Go client) and measured **quality-identical to
the digit (WER 23.5 / DER 11.3 / cpWER 31.1) but throughput-NEUTRAL: ~130× aggregate vs the v1
pool's ~133–138×, with a uniform ~14× per stream at 10-way concurrency in both topologies.** The
eighteenth pass's context-time-slicing diagnosis is corrected: ~140× aggregate is the pyannote
pipeline's **GPU compute ceiling** on L40S (136× uncontended ≈ 140÷10 fair-share), so no serving
topology moves it. diar-v2's measured value is **peak VRAM 30.2 → 22.3 GB** and one host
python/torch stack instead of ten — headroom (smaller GPUs now fit the stack), not dollars. The
sub-$0.01 path is now unambiguous: make the diar compute cheaper — **fp16/TRT static embedding
engine** (the wespeaker ResNet is 83–86% of diar infer), **VAD gating**, and container/GPU
right-sizing. One process-killing pitfall documented for reuse: concurrent
`Pipeline.from_pretrained` across threads in one process dies natively (FUSE-volume mmap
suspected) — replicas must load serially. Full tables in [`BOTTLENECK.md`](../BOTTLENECK.md).

**Twentieth pass (2026-06-12) — one serving architecture; fusion is not the lever.** The v1/v2
serving split was consolidated away: the NeMo batched server **is** `parakeet_stt_server.py`, the
one-process/N-worker pyannote server **is** `pyannote_diar_server.py`, the concurrent Go client
owns the canonical `pyannote-server` names, and the generation knobs (`BENCH_STT_SERVER`/
`BENCH_DIAR_SERVER`, `BENCH_V2_*`) are gone — one code path, with the sherpa CLI engines kept as
the local-product baseline. The "fused static embedding engine" thesis was then probed cheaply
with `torch.compile` on the wespeaker forward (`BENCH_PYANNOTE_EMB_COMPILE`): **quality identical
to the digit on both the 4-meeting gate and the 30-meeting corpus, but throughput-neutral-to-
negative at 10-way saturation** (+10% diar wall, +2% cost; ~45 s one-time compile tax per worker,
not deduped across concurrent workers). A small-arm reading that suggested ~1.7× was diagnosed as
a **contention artifact** (the measured stream ran while its neighbors were still CPU-bound
compiling). Conclusion, re-confirming the nineteenth pass from a different angle: the card is at
its conv roofline, so kernel fusion — including the heavier TensorRT route — does not cut
FLOPs-seconds; **VAD (fewer audio-seconds) is promoted to the #1 lever**. GPU right-sizing was
measured and settled the same day: the slimmed stack (−8 GB from one-process diar, −5 GB from
WER-identical NeMo bf16, now the default precision) fits an L4 in 14.5 GB and runs it at 94%
util, quality digit-identical — but at **$0.0208/audio-hr vs L40S's $0.0194**, so L40S stays the
default and L4 is the capacity fallback. The first L4 attempt also surfaced (and fixed) a
robustness gap: a CUDA illegal-memory-access poisons the context permanently, so the diar server
now exits on fatal CUDA errors and the shared Go client restarts a clean process instead of
failing every subsequent meeting. The compile knob is kept, default-off — it is free headroom
only for long-lived warm servers where the per-worker tax amortizes. Full tables in
[`BOTTLENECK.md`](../BOTTLENECK.md).

### Runtime policy

| Data | Default | Rationale |
|---|---|---|
| Model weights | `NOTO_MODAL_MODEL_CACHE=volume` | Download once during setup into the model-cache Volume; runtime containers mount/read the cache instead of downloading weights per request. |
| User audio | `NOTO_MODAL_USER_TRANSFER_MODE=http`, temporary above `NOTO_MODAL_TEMPORARY_THRESHOLD_MB=128` | Small clips can use the Web Function body. Large recordings should stage through `modal.Volume.ephemeral()` so slow client upload does not hold a GPU container open. |
| Benchmark corpus | `NOTO_MODAL_BENCHMARK_TRANSFER_MODE=permanent` | The benchmark corpus is public/reproducible and expensive to re-upload; store it in the dedicated benchmark Volume and reuse it across runs. |
| Warm containers | `NOTO_MODAL_SCALEDOWN_WINDOW_SECONDS=300`, `NOTO_MODAL_MIN_CONTAINERS=0` | Keep post-meeting iteration warm for five minutes without paying for an always-on GPU. Set `min_containers=1` only for live transcription sessions that need low first-token latency. |

Modal's cold-start docs separate queueing for a warm container from first-container
initialization. They also document `scaledown_window`, `min_containers`, and `buffer_containers`
as the knobs that trade cold-start latency for cost. Modal's model-weight guide recommends
storing weights in a Modal Volume, and `modal.Volume.ephemeral()` creates an anonymous Volume for
the context lifetime. See:

- <https://modal.com/docs/guide/cold-start>
- <https://modal.com/docs/guide/model-weights>
- <https://modal.com/docs/reference/modal.Volume#ephemeral>
- <https://modal.com/pricing>

### What the Modal probe must record

Every benchmark run should emit one summary JSON plus per-file details. The summary must include:

| Field | Meaning |
|---|---|
| `container_cold_start_ms` | Request arrival to container warm/ready. |
| `model_cache_read_ms` | Time to stat/read model files from the mounted cache. |
| `model_load_ms` | Time to construct the inference runtime and move weights to GPU memory. |
| `audio_stage_upload_ms` | Client to Modal staging time for `http`/`temporary`/`permanent` modes. |
| `audio_stage_read_ms` | GPU container read time from the staged object or Volume. |
| `inference_ms` | Actual transcription/diarization compute time. |
| `cleanup_ms` | Temporary object deletion/Volume context teardown. |
| `gpu`, `model_cache`, `transfer_mode`, `scaledown_window_seconds`, `min_containers` | Runtime knobs needed to compare runs. |

Run three probes before trusting a number:

1. Cold request after scale-to-zero: measures full queue + init + model load.
2. Warm request within `scaledown_window`: measures steady-state transfer + inference.
3. Large-file staged request: measures ephemeral/permanent Volume read throughput and cleanup.

### Storage and cost estimates

Use the public benchmark cache only for corpora and immutable model/runtime assets.

| Item | Estimate |
|---|---|
| AMI persona subset | `~1.7 GB` from `benchmark/identity/fetch.py`. |
| 100 h normalized mono WAV | `~11.5 GB` at 16 kHz, 16-bit PCM. |
| 100 h compressed speech | typically lower than WAV; plan `5-8 GB` until measured. |
| 100 h stereo 48 kHz WAV | `~69 GB`; avoid storing this form long-term. |
| Results JSON/CSV/details | small compared with audio; keep all run summaries, prune raw duplicated audio. |

Modal lists Volume storage at `$0.09/GiB-month` with `1 TiB/month free` on the pricing page as
of 2026-06-10. A 25 GB benchmark cache is about `$2.25/month` before free allowances. A full
terabyte is about `$92/month`, so the benchmark setup should keep one current corpus copy, one
model cache, and small result artifacts rather than accumulating raw audio versions.

GPU warm-idle cost is dominated by the selected GPU. Modal lists L4 at `$0.000222/sec` and A10
at `$0.000306/sec` as of 2026-06-10. Keeping one L4 warm for five idle minutes costs about
`300 * 0.000222 = $0.0666`; keeping it warm continuously would cost about `$19/day`. That is
why the default is `min_containers=0` plus a five-minute `scaledown_window`, not an always-on GPU.

---

## 3. Limitations

The persona suite isolates the embed + match step; several real-world factors are not exercised
by these numbers and bound how far they generalize:

| limitation | why it matters |
|---|---|
| **Production (non-GT) diarization** | the persona suite feeds ground-truth turns; AssemblyAI's real speaker-confusion/overlap errors are the dominant production risk. The condition sweep *simulates* this adversarially as an upper bound, but does not measure the live diarizer. |
| **Cross-device / cross-room acoustics** | AMI records the same people on the same headsets in the same room; linking a laptop mic to a phone call would lower genuine scores. Not measured. |
| **Very short speech (one line)** | AMI participants all speak for minutes; a one-utterance speaker is expected to be much weaker. Not measurable on this corpus. |
| **STT and summary quality** | summaries cite transcript text, so transcription errors propagate. `summary.v1` is hand-tuned, not metric-optimized; there is no WER or citation-precision harness yet. |

---

## 4. Datasets & provenance

> **No media is committed to this repository.** All audio (and the AMI RTTM ground truth) is
> re-downloadable via the `fetch.py` scripts and is `.gitignore`d. Only the dataset *specs*
> (`dataset.json`), the *fetchers*, and the *test code* live in git — keeping the repo small and
> avoiding redistribution of corpora under their original licenses.

| Dataset | Used for | Speakers | Committed? | Fetch | License |
|---|---|---|---|---|---|
| **AMI subset** (`ami-personas`) | Cross-meeting speaker accuracy | 52 / 39 meetings | spec only (`dataset.json`) | `benchmark/identity/fetch.py` (~1.7 GB) | CC BY 4.0 (AMI) |
| **LibriSpeech trial set** | Embedding-model selection | 24 × 4 clips | manifest only (`audio_manifest.json`) | `benchmark/voiceprint/` (HF datasets-server) | CC BY 4.0 |
| **Synthetic 2-speaker** | End-to-end pipeline smoke test | 2 | spec only (`meeting_2spk.json`) | `benchmark/dataset/fetch.py` | generated locally |

**AMI personas subset** (`benchmark/identity/`) — a curated subset of the
[AMI Meeting Corpus](https://groups.inf.ed.ac.uk/ami/corpus/) chosen so the *same participants
recur across multiple meetings*. AMI records scenario series (e.g. `ES2005a/b/c/d`) with the same
people, and [pyannote/AMI-diarization-setup](https://github.com/pyannote/AMI-diarization-setup)
ground-truth RTTM labels turns with **global participant IDs consistent across sessions** — an
unambiguous "same person?" answer that read-speech corpora cannot provide. Uses the single-channel
`Mix-Headset` mix (realistic overlap/crosstalk). `dataset.json` is the committed spec;
`build_dataset.py` greedily picks 3 sessions per series until ~50 speakers are covered, and
`fetch.py` pulls exactly those `Mix-Headset` WAVs + RTTMs. **CC BY 4.0** — noto redistributes
nothing; cite AMI if you publish results.

**LibriSpeech trial set** (`benchmark/voiceprint/`) — a 24-speaker × 4-clip set from
`clean/validation` (144 genuine, 4 416 impostor pairs) for the model-vs-model discrimination
study in §1. Clean read speech is ideal for a controlled comparison but unsuitable for
cross-meeting identity (no recurrence, no overlap) — hence AMI for §2. Exact clips pinned in
`audio_manifest.json`. **CC BY 4.0** (LibriVox public domain); WeSpeaker ONNX models carry their
own upstream licenses.

**Synthetic 2-speaker clip** (`benchmark/dataset/`) — a generated 2-speaker meeting plus per-speaker
LibriSpeech clips, used as a fast end-to-end pipeline smoke test (import → transcribe → identify →
summarize → index). Verified: cross-meeting separation 0.886 vs 0.174; importing the same video
twice mints 2 profiles then auto-matches them (confidence 1.0), so the profile count stays 2.

**Privacy.** Speaker embeddings derived from these datasets are **biometric data**. In the
product, noto keeps all audio and embeddings local / server-side (audio goes to AssemblyAI for
*transcription* only, never *identity*), supports per-person delete/forget, and can disable voice
identity entirely. Only public research corpora are used for benchmarks — no private meeting audio
is committed or redistributed.

---

## How to read these numbers

1. **Identification (rank-1) ≠ verification (open-set).** noto is excellent at *ranking* the
   right person and at *not silently merging strangers* (0 false auto-merges); the residual
   risk is the ~5 % open-set **pending-band** suggestions a human reviews, which channel
   mismatch inflates.
2. **The happy path is not the product.** The numbers that matter operationally are the
   degraded ones, and the dominant lever is **upstream diarization quality**, not the embedder.
3. **Thresholds are precision-first by design.** `auto 0.70 / pending 0.55` is tuned to never
   merge a stranger silently, at the cost of a small pending-review rate — the right trade for
   biometric identity.

See [speaker-identity.md](speaker-identity.md) for the design rationale and decisions behind the
pipeline these numbers measure.

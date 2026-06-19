# `benchmark/` — local meeting→transcript TDD harness

The measuring stick for noto's **local-first** pipeline (local STT + local
diarizer + in-process ECAPA identity), meeting audio → attributed transcript,
**stopping before the LLM summary**. Every model or processing change is meant
to be proven here: better accuracy *and* faster, on **CPU as the worst case**.

## Layout

| Dir | What | Input | Measures |
|-----|------|-------|----------|
| [`metrics/`](metrics/) | Pure scorers — WER, DER (+Hungarian), cpWER, SA-WER, attribution, RTF | — | (library) |
| [`dataset/`](dataset/) | Ground-truth loaders + fetchers (synthetic + LibriSpeech; AMI words) | — | (assets) |
| [`stt/`](stt/) | **Atomic** — `STTProvider.Transcribe(audio)` | golden audio | WER + STT-RTF |
| [`diarize/`](diarize/) | **Atomic** — `Diarizer.Diarize(audio)` | golden audio | DER + attribution + diar-RTF |
| [`identity/`](identity/) | **Atomic** — ECAPA + matcher (the former `bench/personas`) | golden turns | EER / rank-1 |
| [`voiceprint/`](voiceprint/) | Embedding-model selection (Python, former `bench/voiceprint`) | — | EER / RTF sweep |
| [`e2e/`](e2e/) | **Chained** — STT→diarize→merge→identity, real handoffs | upstream output | cpWER / SA-WER + per-stage & total RTF |

## Atomic vs. chained

The whole design turns on **one knob — the input source per stage:**

- **`golden`** (atomic): feed a stage *ground-truth inputs* so its own error is
  isolated. Use this for local-optima work on one subsystem.
- **`upstream`** (chained): feed a stage the *previous stage's real output* so you
  see **compounding error** and total who-said-what quality (`e2e/`).

One set of scorers (`metrics/`), two wiring modes.

## Download (datasets + models)

Media is **gitignored** and re-downloadable; only specs and loaders are
committed. Tests `t.Skip` when an asset is absent, so nothing here *requires* a
download — fetch only what the bench you want to run needs.

```bash
# Go toolchain is in-repo; prefix once per shell:
export PATH="$PWD/.tools/go/bin:$PATH"

# AMI meetings (Mix-Headset wav + diarization RTTM, ~1.7 GB) → benchmark/identity/ami/
python3 benchmark/identity/fetch.py            # all in dataset.json
python3 benchmark/identity/fetch.py ES2002a    # or a subset, by meeting id

# Synthetic 2-speaker smoke (built from LibriSpeech clips) → benchmark/dataset/synthetic/
python3 benchmark/dataset/fetch.py
```

| Asset | Fetch | Feeds | Committed? |
|-------|-------|-------|------------|
| AMI wav + RTTM | `benchmark/identity/fetch.py` | identity, diarize, e2e | spec only (`dataset.json`) |
| Synthetic meeting | `benchmark/dataset/fetch.py` | DER smoke | spec only (`meeting_2spk.json`) |
| LibriSpeech clips | `benchmark/dataset/fetch.py` | voiceprint, embedder | manifest only |
| AMI word refs (`.words.json`) | — (produced when the STT bench is wired) | stt WER, e2e cpWER | no |
| Local model weights | per-engine setup (runtime is pluggable) | local STT/diarizer | no |

See [`dataset/README.md`](dataset/README.md) for the on-disk formats and why
model weights aren't fetched by one script.

## Running

```bash
make bench-metrics   # fast: the pure scorers, no assets (also part of `make test`)
make bench           # full atomic + chained; t.Skip when datasets/providers absent

# individual benches:
go test -v ./benchmark/stt/        # atomic WER   (skips without an STT provider + word refs)
go test -v ./benchmark/diarize/    # atomic DER   (skips without a diarizer + AMI assets)
go test -v ./benchmark/identity/   # cross-meeting speaker accuracy (needs ECAPA + AMI)
go test -v ./benchmark/e2e/        # chained cpWER/SA-WER (harness tests run with no assets)

# the regression gate (and how to re-pin it):
BENCH_DEEP=1 go test -run TestBaselineRegression -v ./benchmark/e2e/
BENCH_DEEP=1 BENCH_REPIN=1 go test -run TestBaselineRegression ./benchmark/e2e/
```

### Quick runs by audio budget — `-hours` / `-seed`

Don't wait on 100 h of audio to sanity-check a change. `-hours=n` caps a run to
the **whole meetings** that fit ~`n` hours of audio (always ≥ 1 meeting), and
`-seed` makes the draw reproducible (default `1`, so two runs sample the *same*
meetings unless you change it):

```bash
# NOTE: -hours/-seed are test flags — they MUST come AFTER the package path.
go test ./benchmark/diarize/ -v -hours=1            # ~1 h of meetings (≈2 AMI sessions)
go test ./benchmark/stt/     -v -hours=0.5          # ~30 min, the fastest real signal
go test ./benchmark/e2e/     -v -hours=1 -seed=3    # same 1 h every run (seed 3)

# easier: the make wrapper puts the flags in the right place for you
make bench HOURS=2            # a 2 h slice across every bench (SEED=1 by default)
make bench HOURS=1 SEED=3     # …and pin which meetings get drawn
#                             (no HOURS → the whole corpus)
```

> Why "after the package path"? `go test` only forwards a flag it doesn't itself
> recognise (`-hours`, `-seed`) to the test binary *once it has seen the packages*.
> Put them first and go looks for a package literally named `.` and prints
> `no Go files in …`. `make bench HOURS=…` exists so you never have to remember this.

Whole meetings are the unit (a meeting is transcribed and scored in full, so audio
never desyncs from its reference); the cap is therefore approximate, rounding down
to meeting boundaries — at least one full meeting always runs, so there is always
something complete to score against. The draw is a **seeded** shuffle-then-fill
(not the alphabetical prefix), so a 1 h sample is a representative slice, and the
chosen subset is logged (`sample: -hours=1 -seed=3 → N/M meetings (~H h audio)`).

### Comparing runs over time — `runs.jsonl`

The e2e regression gate records every deep run to `benchmark/e2e/runs.jsonl`
(gitignored — local history, not a committed artifact) and, on the next run,
prints a trend table against the previous run of the same suite:

```
trend[e2e] vs 2026-06-10T11:17:23Z:
  metric             prev    current        Δ
  e2e.cpwer         0.182      0.150   -0.032 ▼   # ▼ better, ▲ worse, = unchanged
```

This answers "is the system actually improving?" independent of the fixed
`baseline.json` gate: the gate says *don't regress past the pin*, the trend says
*did this change move the needle vs last time*. Re-pin the gate with `BENCH_REPIN=1`
once an improvement is real.

### Running on Modal (GPU / CPU)

The same benches run remotely on a Modal GPU (the dev/CI accuracy+speed harness on
real hardware). The runner (`scripts/modal_benchmark.py`) copies the repo into a
CUDA image, keeps datasets + model weights in Modal Volumes, and invokes the
**same `go test` benches with the same `-hours`/`-seed` flags** — so a remote run
samples exactly like a local one.

**Three profiles, one suite.** `--profile prod` (default) runs **one meeting at a
time, packages sequential**: the honest single-stream latency/RTF a real user gets.
`--profile batch` is the **value-for-money throughput shape** for the AMI suite:
`jobs` meetings in flight over stage-decoupled engine pools (STT≈0.4×jobs,
diar=jobs, derived automatically), container right-sized to the engine count —
the validated $/audio-hr configuration (BOTTLENECK.md). `--profile fast` is the
legacy paired-engine parallel shape, kept for the CPU baseline and A/B history.
The scores are identical in every profile — only wall time, utilization and cost
differ. RTF/latency numbers you quote for the product should come from `prod`;
$/audio-hr numbers from `batch`.

```bash
# GPU (default accelerator=cuda); needs Modal auth (~/.modal.toml) + .venv-modal.
# Start small + subsampled for quick TDD, scale the GPU up only once it's fed:
make model-benchmark BENCH_QUICK=1 BENCH_HOURS=0.3       # L4, e2e-only, ~1 meeting — cheapest loop
make model-benchmark                                     # L4, full synthetic, fast (parallel)
make model-benchmark BENCH_GPU=L40S BENCH_SUITE=ami BENCH_HOURS=1
make model-benchmark BENCH_PROFILE=prod                  # single-stream → real per-user latency
make model-benchmark-cpu BENCH_SUITE=synthetic           # CPU worst-case baseline

# perf + price across GPUs, single-stream vs parallel (the "test once" comparison):
make model-sweep BENCH_QUICK=1 BENCH_HOURS=0.5           # prints a table; one run per cell

# or call the runner directly (flags mirror the make vars):
.venv-modal/bin/python scripts/modal_benchmark.py run --suite ami --gpu L40S --hours 1
.venv-modal/bin/python scripts/modal_benchmark.py run --suite ami --profile batch  # tuned $/audio-hr shape
.venv-modal/bin/python scripts/modal_benchmark.py sweep --gpus L4,L40S,A100-40GB --profiles prod,fast
```

- **Suites** (`BENCH_SUITE` / `--suite`): `synthetic` (built fresh each run — the
  quick smoke), `ami` (real AMI diarization + identity), `all`.
- **Profile** (`BENCH_PROFILE` / `--profile`): `prod` = single stream; `batch` =
  pooled throughput; `fast` = legacy parallel. **Concurrency** is the knob: each
  bench runs `-jobs` meetings at once (`prod`→1, `batch`→10, `fast`→8, capped to
  the meeting count; override with `BENCH_JOBS` / `--jobs`). The runner divides
  `NOTO_SHERPA_THREADS` by `jobs` so parallel decoders don't oversubscribe the
  container's CPUs. The profile also picks the **container shape** (prod =
  4 vCPU/8 GiB; batch scales with the pooled engine count — ~1.2 GiB/engine,
  0.4 cores/job, measured; fast = 6/16) and `fast` sets `BENCH_STT_BATCH=1` so
  the atomic STT bench feeds the whole corpus through one warm CLI pass. Every
  tuning knob travels through **one forwarding table** (`KNOB_FORWARDS` in
  `scripts/modal_benchmark.py`) into the container env and is echoed into
  `summary.json` (`"knobs"`), so a result is interpretable without the shell
  history that launched it.
- **GPU** (`BENCH_GPU` / `--gpu`): default **L40S** — best fp32 FLOPs/$ on Modal's
  menu; the `batch` profile saturates it (92–94% mean util, ~130× aggregate
  realtime, **$0.0170/audio-hr** measured — the card's fp32 ceiling). **L4** for
  the cheapest single stream. **H100 re-measured in the fp32+TF32 era**
  (`BENCH_TF32=1` → `NVIDIA_TF32_OVERRIDE=1`, accuracy-identical): tops out at
  ~185× — 1.42× the L40S at 1.87× the price, still Pareto-dominated for
  $/audio-hr; it's the single-stream latency choice only. The measured per-GPU
  perf+cost table and recommendations live in
  [`.docs/benchmarks.md` → Measured: parallelism, per-GPU perf & cost](../.docs/benchmarks.md#measured-parallelism-per-gpu-perf--cost-2026-06-10).
- **Quick** (`BENCH_QUICK=1` / `--quick`): run only the chained e2e test — it emits
  WER + DER + cpWER in one pass, the fastest "is it working / did it improve" signal
  (skips the atomic STT/diarize passes the e2e already performs internally).
- **Subsampling** is identical to local: `BENCH_HOURS=n` (`--hours`) caps to whole
  meetings, `BENCH_SEED=n` (`--seed`, default **1**) fixes the draw. The runner
  appends them as `... ./benchmark/<pkg> -args -hours=n -seed=n -jobs=j`, so the cap
  lands on the test binary the same way the local `make bench HOURS=…` does.
  `--hours 0` (the default) is the full corpus. (Identity always runs in full.)
- **Results** land in `.modal-results/<run_id>/` (`summary.json` with the parsed
  KPIs, `profile`/`jobs`/`gpu`, GPU peak util+memory, `duration_ms`, and
  `estimated_cost_usd` — a rough GPU+CPU+mem $ estimate, not billing — plus
  `raw.jsonl` and `setup.log`); `.modal-results/latest` symlinks the newest run.
  `sweep` additionally prints a perf+price table across the GPU×profile matrix.
- **Engines** default to CUDA aliases (`cuda-parakeet`/`cuda-pyannote`); the runner
  refuses to launch a GPU run with CPU-only engines (use `model-benchmark-cpu` for
  the intentional CPU baseline). Infra knobs (Volumes, scaledown, cost) are in
  [`.docs/benchmarks.md`](../.docs/benchmarks.md#modal-gpu-benchmark-runtime).

### Environment flags

| Var / flag | Effect |
|------------|--------|
| `-hours=n` (test flag) | cap a run to ~`n` hours of audio (whole meetings); unset = all. After the package path. |
| `-seed=n` (test flag) | seed for the `-hours` draw (default 1; same seed → same meetings) |
| `-jobs=n` (test flag) | meetings to run concurrently (1 = single stream; >1 = parallel). Never changes scores, only wall time. |
| `BENCH_SUITE` / `BENCH_HOURS` / `BENCH_SEED` | remote (Modal) suite + the same hours/seed sampling, via `make model-benchmark` |
| `BENCH_PROFILE` / `BENCH_GPU` / `BENCH_JOBS` / `BENCH_QUICK` | remote profile (fast\|prod), GPU type, concurrency override, e2e-only quick mode |
| `BENCH_DEEP=1` | run the deep sweeps + the e2e regression gate (`Fatalf` on regression) |
| `PERSONA_DEEP=1` | unlock the identity deep benches (52-speaker sweep, conditions, SOTA) |
| `BENCH_REPIN=1` | with `BENCH_DEEP=1`, rewrite `benchmark/e2e/baseline.json` from the run |
| `BENCH_STT_ENGINE` + `BENCH_STT_MODELS` | pick the local STT runtime (registered engine name) + its model dir for the stt bench |
| `BENCH_DIAR_ENGINE` + `BENCH_DIAR_MODELS` | pick the local diarizer runtime + its model dir for the diarize bench |
| `BENCH_STT_BATCH=1` | atomic STT bench transcribes the whole corpus in ONE warm engine pass (cross-meeting batching; the Modal `fast` profile sets it) |
| `BENCH_STT_PRECISION` | `int8` (default) or `fp32`: model precision for the GPU STT path. fp32 measured **3× faster** on L4 (RTF 0.04 vs 0.12) — int8 is a CPU optimization — but its VRAM footprint OOMs the `fast` profile's concurrency on a 24 GB L4; use with `prod` or reduced `BENCH_JOBS` |
| `BENCH_NEMO_PRECISION` / `BENCH_NEMO_BATCH` | warm STT server (`scripts/parakeet_stt_server.py`, NeMo-backed — a multi-chunk request decodes as ONE padded batch, encoder + TDT decoder; measured accuracy-neutral on AMI, WER 23.5 vs 23.9, with warm per-stream STT 355–543× vs the retired sherpa server's ~120× ceiling): `bf16` autocast (default — WER-identical to fp32 on the 30-meeting corpus twice, ~5 GB less VRAM) or `fp32`, and the server's internal transcribe batch size (default 8). The runner auto-derives `BENCH_STT_POOL=1` + chunk batch 32 |
| `BENCH_DIAR_WORKERS` / `BENCH_DIAR_STREAMS` | warm diar server (`scripts/pyannote_diar_server.py`: ONE warm process / ONE CUDA context shared by the whole diar pool via the concurrent Go client; measured DER-identical and throughput-neutral vs the retired process-per-engine pool — the diar ceiling is pyannote's GPU compute, not topology; BOTTLENECK.md 19th pass — at **−8 GB peak VRAM**): worker-thread/replica count (= max concurrent meetings on the GPU, default = jobs), and per-worker CUDA streams (`1` default; `0` = all workers share the default stream, a serialization diagnostic) |
| `BENCH_PYANNOTE_EMB_COMPILE` | `torch.compile` the diar embedding ResNet's forward (83–86% of diar infer, compute-bound — the fused-static-engine probe). `1` = default compile mode; other values pass through as `torch.compile(mode=...)` (`reduce-overhead` is rejected — its CUDA graphs don't compose with per-worker streams). Off by default until DER-gated |
| `BENCH_VAD` / `BENCH_VAD_PAD` / `BENCH_VAD_MIN_GAP` / `BENCH_VAD_THRESHOLD` | Silero-VAD silence trimming in the DIAR server (TODO-7, `scripts/vad_trim.py`): each meeting is condensed to its padded speech regions before the pipeline sees it, and every turn is interval-clipped back to the original timeline server-side — the wire protocol and Go side are untouched. Only silence gaps ≥ `MIN_GAP` (default 3.0 s) are collapsed, each speech region keeps `PAD` (default 0.5 s) per side, `THRESHOLD` (default 0.35, more speech-sensitive than Silero's 0.5) guards against false negatives, and any VAD failure degrades to the untrimmed audio. The STT server deliberately does NOT trim (its single-threaded decode loop would serialize ~1.25 s CPU VAD per 120 s chunk into the critical path — the chunker-level VAD is the right STT pairing). GPU cost is linear in audio-seconds, so the win tracks the silence fraction: small on dense AMI, ×1.3–3 on real meetings |
| `BENCH_DIAR_ENGINE=cuda-pyannote-server` | warm pyannote.audio server diarizer (GPU-batched windows, model loads once per run). Default pipeline is the **ungated** community mirror of community-1 (`pyannote-community/speaker-diarization-community-1`, CC-BY-4.0, self-contained) — **no HF account or token for anyone**; weights cache into the model volume on first use. `NOTO_PYANNOTE_PIPELINE` overrides (local dir or other repo); `BENCH_PYANNOTE_OFFLINE=1` forbids hub contact (serve from cache) |

See [`.docs/benchmarks.md`](../.docs/benchmarks.md) for headline numbers and the
methodology behind each scorer, and [`e2e/README.md`](e2e/README.md) for the
chained pipeline and the regression gate.

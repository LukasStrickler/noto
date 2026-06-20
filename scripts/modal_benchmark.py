#!/usr/bin/env python3
"""Run noto benchmarks on Modal and copy result artifacts back locally.

This is a dev/CI runner, not a user runtime dependency. It uses the Modal Python
SDK directly with MODAL_TOKEN_ID / MODAL_TOKEN_SECRET from the environment. The
repo code is copied into the image; benchmark media and model/runtime assets are
kept in Modal Volumes so repeated runs do not re-upload or re-download them.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import modal


REPO_ROOT = Path(__file__).resolve().parents[1]
REMOTE_REPO = Path("/root/noto")
MODEL_MOUNT = Path("/cache/model")
BENCH_MOUNT = Path("/cache/benchmark")
RESULT_MOUNT = Path("/cache/results")

APP_NAME = os.getenv("NOTO_MODAL_APP_NAME", "noto-compute")
ENVIRONMENT = os.getenv("NOTO_MODAL_ENVIRONMENT") or None
GPU = os.getenv("NOTO_MODAL_GPU", "L40S")
ACCELERATOR = os.getenv("BENCH_ACCELERATOR", os.getenv("NOTO_COMPUTE", "cuda")).lower()
FUNCTION_GPU = None if ACCELERATOR == "cpu" else GPU
STT_ENGINE = os.getenv("BENCH_STT_ENGINE", "cuda-parakeet-server")
# Precision per accelerator: GPU defaults to fp32, CPU to int8. int8 is a CPU
# optimization whose dequantize overhead makes it SLOWER on CUDA — sixth-pass
# measurement (BOTTLENECK.md) confirmed fp32 is **3.6× faster AND 3.6× cheaper**
# on L40S (e2e 121 s→33 s, $0.074→$0.021) with NO accuracy loss (synthetic e2e
# WER 51.7→50.3, *better*; LibriSpeech 2.07→1.69; the old "mtg00 82 % collapse"
# did NOT reproduce). fp32 swaps the model dir (fetch-bench-deps.sh downloads the
# export on demand); engines are unchanged. VRAM is safe under `prod` (peak
# ~8 GB); fp32 only OOM'd the retired `fast` full-concurrency path.
#
# fp16 (opt-in: BENCH_STT_PRECISION=fp16) converts the fp32 ONNX export
# (scripts/convert_parakeet_fp16.py, cached on the volume) — these precision/
# model-dir knobs now matter ONLY for the sherpa CLI baseline engine
# (BENCH_STT_ENGINE=cuda-parakeet); the default warm server is NeMo-backed and
# loads its .nemo from the HF cache, no ONNX dir involved.
_DEFAULT_PRECISION = "fp32" if ACCELERATOR in {"cuda", "gpu", "modal"} else "int8"
STT_PRECISION = os.getenv("BENCH_STT_PRECISION", _DEFAULT_PRECISION).lower()
# Serving architecture (consolidated 2026-06-12, 20th pass — ONE architecture,
# no v1/v2 split):
#   STT  = scripts/parakeet_stt_server.py — NeMo-backed, truly batched: a
#          {"wavs": [...]} request decodes as ONE padded batch (encoder AND TDT
#          decoder). The sherpa/ONNX-Runtime warm server it replaced ceilinged
#          at ~132× aggregate on L40S (17th pass); NeMo's batched label-looping
#          decode of the SAME weights is the ~1000×-class leaderboard path.
#   diar = scripts/pyannote_diar_server.py — ONE warm process, ONE CUDA
#          context, NOTO_PYANNOTE_WORKERS threads each owning a stock pipeline
#          replica on its own CUDA stream; the whole BENCH_DIAR_POOL of Go
#          handles shares that process, responses demultiplexed by request id.
#          MEASURED (19th pass, 30-meeting AMI): throughput-neutral vs the
#          retired process-per-engine pool (the diar ceiling is pyannote's GPU
#          COMPUTE, not serving topology) at −8 GB peak VRAM (30.2 → 22.3) and
#          quality identical to the digit (DER 11.3 / cpWER 31.1).
# Synthetic-corpus size (meeting count). Default 3 ≈ 18.6 min of audio — the
# canonical small corpus. Override (with --rebuild-synth) to build a longer
# corpus for sustained-throughput / per-audio-hour measurements, e.g.
# BENCH_SYNTH_MEETINGS=10 ≈ 1 h. The corpus lives in a persistent Modal volume,
# so a non-default size must pass --rebuild-synth and be restored to 3 after.
SYNTH_MEETINGS = os.getenv("BENCH_SYNTH_MEETINGS", "3")
# Restrict the AMI capture universe to a known set of meeting IDs (comma list).
# Empty = every meeting identity/fetch.py provides. Used to keep a run to a subset
# whose (slow, server-side) reference fetch is already done locally.
AMI_MEETINGS = os.getenv("BENCH_AMI_MEETINGS", "")
# Auto-fill depth: with no explicit AMI_MEETINGS, fetch & score jobs×QUEUE_DEPTH
# meetings so the smart scheduler (LPT work-stealing pool, see bench.RunMeetings)
# always has a BACKLOG deeper than the worker count. A backlog is what lets a freed
# worker immediately start the next meeting instead of all streams hitting their
# CPU clustering tail together — the measured fix for the 63.6% GPU idle at jobs=4
# where meetings==jobs ran lock-step. depth=1 reproduces the old one-per-worker.
# Read CONTAINER-side (auto_ami_meetings runs remotely) — reaches the box via
# KNOB_FORWARDS like every other knob.
QUEUE_DEPTH = max(1, int(os.getenv("BENCH_QUEUE_DEPTH", "2") or 2))

# ── Tuning knobs: ONE forwarding table ──────────────────────────────────────
# Every tuning knob is a BENCH_* variable in the LOCAL environment; this table
# is the single place that carries it into the CONTAINER environment (where the
# Go benches and the warm python servers read it). Three rules keep this robust:
#   1. a knob not listed here never reaches the box — add it HERE, not as an
#      ad-hoc `if` in launch() (BENCH_QUEUE_DEPTH was silently ignored remotely
#      for a while because it lived outside the forwarding path);
#   2. empty value = "not set": the container/engine default applies;
#   3. run_remote_benchmark echoes every knob that reached the box into
#      summary.json ("knobs"), so a result is interpretable without the shell
#      history that launched it.
KNOB_FORWARDS: list[tuple[str, str, str]] = [
    # (local env, container env, default)
    # Stage-pool sizes: resident STT vs diar engines for the AMI capture (see
    # ami_test.go). Empty = the runner derives the VALIDATED split in launch():
    # STT≈0.4×jobs, diar=jobs (BOTTLENECK.md 16th pass: util 90%, −23% $/audio-hr
    # vs paired). A model is held only while its stage runs, so VRAM goes to the
    # bottleneck instead of 50/50.
    ("BENCH_STT_POOL", "BENCH_STT_POOL", ""),
    ("BENCH_DIAR_POOL", "BENCH_DIAR_POOL", ""),
    # Scheduler backlog depth (see QUEUE_DEPTH above).
    ("BENCH_QUEUE_DEPTH", "BENCH_QUEUE_DEPTH", ""),
    # Within-meeting STT chunk batching: decode N of one meeting's 120 s chunks
    # per warm-server request (sherpa decode_streams = ONE padded encoder pass).
    # Measured: accuracy-neutral but NO speedup (compute-bound) and 3× VRAM —
    # default off, kept as a knob for launch-bound future stacks.
    ("BENCH_STT_CHUNK_BATCH", "NOTO_PARAKEET_CHUNK_BATCH", ""),
    # Which transducer components the fp16 export converts (fetch-bench-deps
    # defaults to "encoder"). "encoder,decoder,joiner" = full-fp16 diagnostic.
    # Changing it rebuilds the volume-cached export (conversion.marker).
    ("BENCH_FP16_ROLES", "PARAKEET_FP16_ROLES", ""),
    # Force-enable TF32 in cuBLAS/cuDNN for EVERY process on the box (sherpa's
    # ONNX Runtime AND pyannote's torch). TF32 keeps fp32's 8-bit exponent — the
    # range fp16 lacked when it derailed the encoder — at tensor-core rates. A
    # no-op on Ada (L40S fp32 ≈ its TF32 rate) but the throughput unlock to test
    # on Ampere/Hopper, where TF32 is 5–8× the fp32 CUDA-core rate. "1" = on.
    ("BENCH_TF32", "NVIDIA_TF32_OVERRIDE", ""),
    # pyannote warm-server knobs (BOTTLENECK.md tenth pass): window batching gave
    # NO speedup at 2× VRAM (compute-bound) → default 0; embedding bf16 is the
    # validated win (DER unchanged, −26% wall) → default emb-bf16; segmentation
    # AMP measured +1.2 DER for nothing → opt-in diagnostic only.
    ("BENCH_PYANNOTE_SEG_BATCH", "NOTO_PYANNOTE_SEG_BATCH", "0"),
    ("BENCH_PYANNOTE_EMB_BATCH", "NOTO_PYANNOTE_EMB_BATCH", "0"),
    ("BENCH_PYANNOTE_FP16", "NOTO_PYANNOTE_FP16", "emb-bf16"),
    ("BENCH_PYANNOTE_SEG_AMP", "NOTO_PYANNOTE_SEG_AMP", ""),
    # Per-stage diar timing (load/seg/emb/cluster) on the server's stderr.
    ("BENCH_PYANNOTE_SERVER_STDERR", "NOTO_PYANNOTE_SERVER_STDERR", ""),
    # torch.compile the diar embedding ResNet's forward (83–86% of diar infer,
    # compute-bound — the fused-static-engine probe). "1" = default mode; other
    # values pass through as torch.compile(mode=...). Off until DER-gated.
    ("BENCH_PYANNOTE_EMB_COMPILE", "NOTO_PYANNOTE_EMB_COMPILE", ""),
    # Diar server shape: worker threads (= pipeline replicas = max concurrent
    # meetings inside the ONE process; empty = derived as eff_jobs in launch())
    # and per-worker CUDA streams (1 = overlap-capable default, 0 = shared-
    # default-stream diagnostic).
    ("BENCH_DIAR_WORKERS", "NOTO_PYANNOTE_WORKERS", ""),
    ("BENCH_DIAR_STREAMS", "NOTO_PYANNOTE_STREAMS", ""),
    # CUDA MPS for the multi-process engine pools: without it, N processes hold N
    # CUDA contexts that TIME-SLICE the GPU (kernels from different processes
    # never overlap), which is why per-engine diar rates collapse as the pool
    # grows. The MPS daemon funnels all clients through one context. "1" = start
    # the daemon in setup (no-op with a warning if the binary is unavailable).
    ("BENCH_CUDA_MPS", "BENCH_CUDA_MPS", ""),
    # NeMo STT server knobs: autocast precision (fp32|bf16) and internal
    # transcribe batch size. bf16 = fp32 weights + fp32 exponent range (the
    # technique that shipped DER-neutral on the pyannote embedding), NOT the
    # static fp16 ONNX export that derailed the encoder.
    ("BENCH_NEMO_PRECISION", "NOTO_PARAKEET_PRECISION", ""),
    ("BENCH_NEMO_BATCH", "NOTO_PARAKEET_BATCH", ""),
    # NeMo word confidence (opt-in) → hyps carry per-word P(correct) for B6
    # calibration + B7 repair candidate selection.
    ("BENCH_PARAKEET_CONFIDENCE", "NOTO_PARAKEET_CONFIDENCE", ""),
    # NeMo decode SEARCH (greedy default | beam | maes | …) + beam width: the §10.5
    # same-model alternate decode the B7 repair loop re-decodes low-confidence spans
    # with (greedy is deterministic on content across precision, so an alternate
    # STRATEGY is the lever that can actually fix errors, not fp32).
    ("BENCH_PARAKEET_DECODE", "NOTO_PARAKEET_DECODE", ""),
    ("BENCH_PARAKEET_BEAM_SIZE", "NOTO_PARAKEET_BEAM_SIZE", ""),
    # Audio perturbation (speed:R | noise:A) → re-decode the SAME model on altered
    # audio: the test-time-augmentation repair lever (a different hypothesis with no
    # weaker model). speed warps time; the server rescales timestamps back.
    ("BENCH_PARAKEET_PERTURB", "NOTO_PARAKEET_PERTURB", ""),
    ("BENCH_PARAKEET_PREPROC", "NOTO_PARAKEET_PREPROC", ""),
    # Swap the NeMo STT weights (HF repo or .nemo path) → §10.5 alternate-ASR
    # repair method: re-decode low-confidence spans with a different/larger parakeet.
    # The model resolves from the HF cache the same way the default does (downloads
    # once into the model volume), so the only marginal cost is the alt decode itself.
    ("BENCH_PARAKEET_MODEL", "NOTO_PARAKEET_MODEL", ""),
    # Stream the parakeet server's full stderr to the box stdout (diagnostic) so a
    # NeMo fault surfaces in the run output instead of a truncated tail.
    ("BENCH_PARAKEET_SERVER_STDERR", "NOTO_PARAKEET_SERVER_STDERR", ""),
    # Silero-VAD silence trimming in BOTH warm servers (TODO-7): GPU cost is
    # linear in audio-seconds, so feed only speech — the servers condense each
    # input to its (padded) speech regions and remap timestamps back to the
    # original timeline, so the wire/Go side never changes. ~1.2× expected on
    # dense AMI, ×1.3–3 on real silence-heavy meetings. Changes what the
    # models SEE → DER/WER-gated on the pinned corpus before any default flip.
    # "" = off; "1" = on; PAD/MIN_GAP in seconds (defaults 0.4 / 1.0 — only
    # silence gaps ≥ MIN_GAP are collapsed; see scripts/vad_trim.py).
    ("BENCH_VAD", "NOTO_VAD", ""),
    ("BENCH_VAD_PAD", "NOTO_VAD_PAD", ""),
    ("BENCH_VAD_MIN_GAP", "NOTO_VAD_MIN_GAP", ""),
    ("BENCH_VAD_THRESHOLD", "NOTO_VAD_THRESHOLD", ""),
    # diar_speakers=auto → the AMI bench passes NumSpeakers:0 (product-realistic
    # auto-detect) instead of the oracle reference count. Forwarded as-is into the
    # container; benchmark/e2e/ami_test.go (go test, inherits container env) reads
    # BENCH_DIAR_SPEAKERS. Empty/unset = oracle (the ledger-baseline behaviour).
    ("BENCH_DIAR_SPEAKERS", "BENCH_DIAR_SPEAKERS", ""),
]
BATCH_SIZE = int(os.getenv("BENCH_BATCH_SIZE", "0") or 0)
BATCH_WAIT_MS = int(os.getenv("BENCH_BATCH_WAIT_MS", "0") or 0)
E2E_BATCH = os.getenv("BENCH_E2E_BATCH", "0") == "1"
DIAR_ENGINE = os.getenv("BENCH_DIAR_ENGINE", "cuda-pyannote-server")
# The warm pyannote.audio server engine (cuda-pyannote-server) needs python
# deps in the image; its default pipeline is the UNGATED community mirror of
# community-1, so no HF account/token is required by anyone. This is now the
# DEFAULT GPU diarizer (measured DER 9.6 / RTF 0.04 — ~9× faster than the cold
# sherpa CLI it replaced); override BENCH_DIAR_ENGINE=cuda-pyannote for the
# dependency-free CLI baseline.
NEEDS_PYANNOTE = "pyannote-server" in DIAR_ENGINE
# pyannote warm-server tuning now lives in KNOB_FORWARDS (one table for every
# knob). MEASURED history (BOTTLENECK.md tenth pass): embedding ResNet ~93% of
# diar time; window batching no-win at 2× VRAM; emb-bf16 the validated default
# (DER unchanged, −26% wall) because bf16 keeps fp32's exponent range.
# The warm STT server engine (cuda-parakeet-server) is NeMo-backed: it needs
# the NeMo ASR stack in the image and loads its .nemo from the HF cache on the
# model volume — no sherpa wheel, no ONNX dir. The model loads ONCE, so the
# cold model-load + CUDA init is paid per run, not per meeting (atomic STT RTF
# 0.08 → 0.06). Now the DEFAULT GPU STT engine; override
# BENCH_STT_ENGINE=cuda-parakeet for the dependency-free CLI baseline. NOTE:
# transcripts are not byte-identical to the CLI (different decoder
# implementation, same weights) — the AMI WER/DER/cpWER gate is the judge.
NEEDS_NEMO = "parakeet-server" in STT_ENGINE
CUDA_IMAGE = os.getenv("NOTO_MODAL_CUDA_IMAGE", "nvidia/cuda:12.4.1-cudnn-devel-ubuntu22.04")
MODEL_VOLUME = os.getenv("NOTO_MODAL_MODEL_VOLUME", "noto-model-cache")
BENCH_VOLUME = os.getenv("NOTO_MODAL_BENCHMARK_VOLUME", "noto-benchmark-cache")
RESULT_VOLUME = os.getenv("NOTO_MODAL_RESULT_VOLUME", f"{BENCH_VOLUME}-results")
SCALEDOWN_WINDOW = int(os.getenv("NOTO_MODAL_SCALEDOWN_WINDOW_SECONDS", "300"))
MIN_CONTAINERS = int(os.getenv("NOTO_MODAL_MIN_CONTAINERS", "0"))

# Three run shapes, the same benches:
#   prod  — one meeting at a time, packages sequential: the honest single-stream
#           latency/RTF a real user gets. THE DEFAULT (and the production answer:
#           a 38-min meeting in ~26 s on L40S).
#   batch — the value-for-money throughput shape (BOTTLENECK.md 16th pass): jobs
#           in-flight meetings over stage-decoupled engine pools (STT≈0.4×jobs,
#           diar=jobs — derived in launch() when unset), container right-sized to
#           the engine count. The validated config measured 90 % mean GPU util /
#           $0.0197 per audio-hr on L40S at jobs=10. The easy robust invocation:
#           `run --suite ami --profile batch`.
#   fast  — legacy "many meetings on paired engines" profile. Superseded by batch
#           (paired engines pin VRAM 50/50 and idle half of it); kept for the CPU
#           baseline and A/B history.
PROFILE = os.getenv("BENCH_PROFILE", "prod").lower()
# Default L40S: best fp32 FLOPs per dollar on Modal's menu, and the batch profile
# saturates it (measured 17th pass: 92–94% mean util, 130–134× aggregate realtime,
# $0.0170/audio-hr — the card's fp32 ceiling). L4 RE-MEASURED with the consolidated
# stack (20th pass): fits comfortably now (14.5 GB peak with NeMo bf16 + one-process
# diar, 94% util, quality digit-identical) but $0.0208/audio-hr vs L40S's $0.0194 at
# ~3× the wall — the workload is compute-bound, so the cheaper card buys proportionally
# less compute. L4 = capacity fallback (NOTO_MODAL_GPU=L4, cap BENCH_DIAR_WORKERS≈6),
# not the default.
# Hopper RE-MEASURED in the fp32+TF32 era (17th pass): H100 with
# NVIDIA_TF32_OVERRIDE=1 (BENCH_TF32=1) is accuracy-IDENTICAL and tops out at
# ~185× — only 1.42× the L40S at 1.87× the price, and pool scaling 4→6 STT
# engines did NOT raise it (card-saturated; per-engine speed just split). So
# H100 stays dominated for $/audio-hr; it's the latency choice only. TF32 never
# reaches tensor-core paper rates here because ORT's kernels for this stack are
# not TF32-gemm-dominated.

# Per-profile container shape. CPU+memory are ~44% of an L4 container's bill at
# the old fixed 8 vCPU / 32 GiB, and a single stream (prod) never uses that —
# right-sizing it is a pure cost cut with no perf claim attached. fast was
# right-sized DOWN once the default pipeline became GPU-resident: with the warm
# pyannote server clustering on the GPU and the warm STT server running as ONE
# python process (not N parallel CLI decoders), the old 8 vCPU / 32 GiB is
# over-provisioned. 6 vCPU still feeds the per-meeting -jobs parallelism; the
# corpus is small so 16 GiB system RAM is ample (the models live in VRAM, so this
# does NOT affect the 24 GB L4 VRAM ceiling). A CPU baseline run
# (BENCH_ACCELERATOR=cpu) always uses the big shape: there the CPU IS the compute.
# Sizes are visible in summary.json (cpu_cores / memory_gib) so the cost estimate
# is always computed from what was actually provisioned.
PROFILE_SHAPES = {
    "prod": {"cpu": 4, "memory": 8192},
    # batch scales up from this floor with the pooled engine count (see launch()).
    "batch": {"cpu": 4, "memory": 8192},
    "fast": {"cpu": 6, "memory": 16384},
}
DEFAULT_SHAPE = {"cpu": 8, "memory": 32768}


def container_shape(profile: str, accelerator: str = ACCELERATOR) -> dict[str, int]:
    if accelerator == "cpu":
        return DEFAULT_SHAPE
    return dict(PROFILE_SHAPES.get(profile, DEFAULT_SHAPE))


def safe_int(raw: str, default: int) -> int:
    try:
        return int(raw)
    except (TypeError, ValueError):
        return default

# Approximate Modal on-demand GPU price ($/hour), for a rough cost estimate only
# (GPU dominates; CPU/memory are added in run_remote_benchmark). Not billing.
GPU_HOURLY_USD = {
    "T4": 0.59,
    "L4": 0.80,
    "A10G": 1.10,
    "L40S": 1.95,
    "A100": 2.10,
    "A100-40GB": 2.10,
    "A100-80GB": 2.50,
    "H100": 3.95,
    "H200": 4.54,
    "B200": 6.25,
}
CPU_CORE_HOURLY_USD = 0.048  # ~Modal physical-core rate
MEM_GIB_HOURLY_USD = 0.008


def gpu_hourly(gpu: str | None) -> float:
    if not gpu:
        return 0.0
    return GPU_HOURLY_USD.get(gpu, GPU_HOURLY_USD.get(gpu.split("-")[0].split(":")[0], 0.0))


def estimate_cost_usd(gpu: str | None, duration_ms: int, cpu_cores: int, mem_gib: float) -> float:
    hours = duration_ms / 3_600_000
    gpu_cost = gpu_hourly(gpu) * hours if gpu else 0.0
    return round(gpu_cost + cpu_cores * CPU_CORE_HOURLY_USD * hours + mem_gib * MEM_GIB_HOURLY_USD * hours, 4)


def audio_hours_from_hyps(hyp_dir: Path) -> float:
    """Sum the wall-clock audio length actually transcribed, from the cached AMI
    hyps (word/turn end times). This is the denominator for $/audio-hr — the
    headline metric — which the runner otherwise never computes (it only knows the
    REQUESTED sampling `hours`, which is 0='whole corpus' for the AMI suite)."""
    if not hyp_dir.is_dir():
        return 0.0
    total_s = 0.0
    for hp in hyp_dir.glob("*.json"):
        try:
            j = json.loads(hp.read_text())
        except Exception:
            continue
        end = 0.0
        for w in j.get("words", []) or []:
            end = max(end, w.get("end", 0) or 0)
        for t in (j.get("turns", []) or j.get("diar", []) or []):
            end = max(end, t.get("end", 0) or 0)
        total_s += end
    return total_s / 3600.0


def audio_hours_from_synth_meta(synth_dir: Path, tests: dict[str, Any]) -> float:
    meta_path = synth_dir / "meetings.json"
    try:
        meta = json.loads(meta_path.read_text())
    except Exception:
        return 0.0
    selected: set[str] = set()
    for name, result in tests.items():
        if result.get("action") != "pass":
            continue
        match = re.search(r"/meetings/(mtg[0-9A-Za-z_-]+)$", name)
        if match:
            selected.add(match.group(1))
    if not selected:
        selected = set(meta.keys())
    total_s = 0.0
    for meeting_id in selected:
        try:
            total_s += float(meta[meeting_id].get("duration") or 0)
        except Exception:
            continue
    return total_s / 3600.0


def auto_ami_meetings(jobs: int, depth: int) -> list[str]:
    """The first jobs×depth meeting IDs from the AMI dataset manifest — a backlog
    sized to keep `jobs` workers continuously fed (see QUEUE_DEPTH). Reading the
    same manifest fetch.py uses keeps the auto set fetchable AND scorable (these
    IDs have word references). Falls back to the ES2002 quartet if unreadable."""
    n = max(jobs * depth, jobs, 1)
    ids: list[str] = []
    try:
        ds = json.loads((REMOTE_REPO / "benchmark" / "identity" / "dataset.json").read_text())
        for m in ds.get("meetings", []):
            mid = m if isinstance(m, str) else m.get("id", "")
            if mid:
                ids.append(mid)
    except Exception as exc:  # manifest missing/corrupt → safe fallback
        log(f"auto_ami_meetings: manifest unreadable ({exc}); using ES2002 fallback")
    if not ids:
        ids = ["ES2002a", "ES2002b", "ES2002c", "ES2002d"]
    return ids[:n]


def utilization_stats(util_samples: list[int], busy_threshold: int = 50) -> dict[str, Any]:
    """Mean utilization and busy-fraction over GPU samples. PEAK util (already
    recorded) hides idle gaps — a run can peak at 100% yet average 60% because the
    GPU stalls between the STT and diarization phases or while the host decodes
    audio. Mean + busy% is what tells us whether we are actually *keeping* the card
    saturated, which is the real throughput lever."""
    vals = [v for v in util_samples if v is not None]
    if not vals:
        return {"gpu_mean_utilization_pct": 0, "gpu_busy_pct": 0, "gpu_util_samples": 0}
    busy = sum(1 for v in vals if v >= busy_threshold)
    return {
        "gpu_mean_utilization_pct": round(sum(vals) / len(vals), 1),
        "gpu_busy_pct": round(100.0 * busy / len(vals), 1),
        "gpu_util_samples": len(vals),
    }


def resolve_jobs(profile: str, override: int | None) -> int:
    """How many meetings to run concurrently. prod = 1 (single stream); batch =
    the validated pooled concurrency (10 on L40S — the 46 GB card is full at the
    matching pools); fast = legacy parallel. An explicit --jobs always wins."""
    if override is not None and override > 0:
        return override
    if profile == "prod":
        return 1
    if profile == "batch":
        return int(os.getenv("BENCH_JOBS", "10"))
    return int(os.getenv("BENCH_JOBS", "8"))


def ignore_repo(path: Path) -> bool:
    rel = path.relative_to(REPO_ROOT) if path.is_absolute() else path
    parts = set(rel.parts)
    if parts & {
        ".git",
        ".tools",
        ".venv",
        ".venv-modal",
        "bin",
        "tmp",
        ".modal-results",
        ".code-review-graph",
    }:
        return True
    if rel.name.startswith(".env"):
        return True
    ignored_dirs = {
        Path("benchmark/identity/ami"),
        Path("benchmark/dataset/librispeech"),
        Path("benchmark/dataset/librispeech_wer"),
        Path("benchmark/dataset/synthetic"),
        Path("benchmark/dataset/synthetic_meetings"),
        Path("benchmark/dataset/words"),
    }
    return any(rel == d or d in rel.parents for d in ignored_dirs)


base_image = (
    modal.Image.debian_slim(python_version="3.12")
    if FUNCTION_GPU is None
    else modal.Image.from_registry(CUDA_IMAGE, add_python="3.12")
)

image = base_image.apt_install("bash", "ca-certificates", "curl", "bzip2", "xz-utils", "git", "ffmpeg")
if NEEDS_PYANNOTE:
    # Heavy (torch CUDA wheels, ~5 GB layer) — built once, cached by Modal; only
    # paid for when the pyannote-server engine is actually selected.
    image = image.pip_install("pyannote.audio>=4.0,<5")
if STT_PRECISION == "fp16":
    # fp16 export tooling (scripts/convert_parakeet_fp16.py) for the sherpa CLI
    # baseline's ONNX model. Small pure-Python deps; only when requested.
    image = image.pip_install("onnx", "onnxconverter_common")
if NEEDS_NEMO:
    # NeMo ASR for the batched STT server. Heavy layer (reuses the torch CUDA
    # stack the pyannote layer installs), built once and cached by Modal; only
    # paid for when the warm server engine is actually selected. cuda-python
    # enables NeMo's CUDA-graph TDT greedy decoder (it falls back without,
    # just slower).
    image = image.pip_install("nemo_toolkit[asr]>=2.4", "cuda-python>=12.3")
if NEEDS_PYANNOTE or NEEDS_NEMO:
    # Silero VAD for the diar server's silence trimming (scripts/vad_trim.py,
    # BENCH_VAD). Tiny model (~2 MB); its torch dep rides the heavy layers
    # above — a separate layer AFTER them so adding it never busts their cache.
    # onnxruntime (CPU) is REQUIRED for throughput, not optional: the torch-JIT
    # fallback holds the GIL through its Python chunk loop, and 10 concurrent
    # diar workers ground to 1048 s of VAD wall on the 30-meeting gate (2.4×
    # the sequential cost) — the ONNX session releases the GIL and pins itself
    # to one intra-op thread, so worker VADs actually run in parallel.
    image = image.pip_install("silero-vad>=5.1", "onnxruntime>=1.16")
image = image.add_local_dir(REPO_ROOT, str(REMOTE_REPO), copy=True, ignore=ignore_repo)

app = modal.App(APP_NAME, image=image)
model_volume = modal.Volume.from_name(MODEL_VOLUME, create_if_missing=True, environment_name=ENVIRONMENT)
bench_volume = modal.Volume.from_name(BENCH_VOLUME, create_if_missing=True, environment_name=ENVIRONMENT)
result_volume = modal.Volume.from_name(RESULT_VOLUME, create_if_missing=True, environment_name=ENVIRONMENT)


def log(msg: str) -> None:
    print(f"[modal-benchmark] {msg}", flush=True)


def run_cmd(
    cmd: list[str],
    *,
    cwd: Path = REMOTE_REPO,
    env: dict[str, str] | None = None,
    timeout: int | None = None,
    probe_gpu: bool = False,
) -> dict[str, Any]:
    started = time.monotonic()
    log("running: " + " ".join(cmd))
    merged_env = os.environ.copy()
    if env:
        merged_env.update(env)
    proc = subprocess.Popen(
        cmd,
        cwd=str(cwd),
        env=merged_env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    samples: list[dict[str, Any]] = []
    output = ""
    exit_code = 124
    try:
        while True:
            if probe_gpu:
                samples.append(gpu_sample())
            elapsed = time.monotonic() - started
            if timeout is not None and elapsed > timeout:
                proc.kill()
                output, _ = proc.communicate()
                exit_code = 124
                break
            wait_timeout = 0.5 if probe_gpu else None
            if timeout is not None and not probe_gpu:
                wait_timeout = max(0.1, timeout - elapsed)
            try:
                output, _ = proc.communicate(timeout=wait_timeout)
                exit_code = proc.returncode
                break
            except subprocess.TimeoutExpired:
                continue
    finally:
        if proc.poll() is None:
            proc.kill()
            output, _ = proc.communicate()
    return {
        "cmd": cmd,
        "cwd": str(cwd),
        "exit_code": exit_code,
        "duration_ms": int((time.monotonic() - started) * 1000),
        "output": output,
        "gpu_samples": samples,
        "gpu_peak_memory_mb": max((s.get("memory_used_mb") or 0 for s in samples), default=0),
        "gpu_peak_utilization_pct": max((s.get("utilization_pct") or 0 for s in samples), default=0),
    }


def gpu_sample() -> dict[str, Any]:
    ts_ms = int(time.monotonic() * 1000)
    try:
        proc = subprocess.run(
            [
                "nvidia-smi",
                "--query-gpu=name,utilization.gpu,memory.used,memory.total",
                "--format=csv,noheader,nounits",
            ],
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            timeout=5,
        )
    except Exception as exc:
        return {"ts_ms": ts_ms, "error": str(exc)}
    if proc.returncode != 0:
        return {"ts_ms": ts_ms, "error": proc.stdout.strip()}
    line = proc.stdout.strip().splitlines()[0] if proc.stdout.strip() else ""
    parts = [p.strip() for p in line.split(",")]
    sample: dict[str, Any] = {"ts_ms": ts_ms, "raw": line}
    if len(parts) >= 4:
        sample["name"] = parts[0]
        sample["utilization_pct"] = parse_int(parts[1])
        sample["memory_used_mb"] = parse_int(parts[2])
        sample["memory_total_mb"] = parse_int(parts[3])
    return sample


def parse_int(s: str) -> int:
    try:
        return int(float(s))
    except ValueError:
        return 0


def ensure_link(src: Path, dst: Path) -> None:
    src.mkdir(parents=True, exist_ok=True)
    if dst.is_symlink() and Path(os.readlink(dst)) == src:
        return
    if dst.exists() or dst.is_symlink():
        if dst.is_dir() and not dst.is_symlink():
            shutil.rmtree(dst)
        else:
            dst.unlink()
    dst.parent.mkdir(parents=True, exist_ok=True)
    os.symlink(src, dst)


def prepare_layout() -> None:
    ensure_link(BENCH_MOUNT / "identity" / "ami", REMOTE_REPO / "benchmark" / "identity" / "ami")
    ensure_link(BENCH_MOUNT / "dataset" / "synthetic_meetings", REMOTE_REPO / "benchmark" / "dataset" / "synthetic_meetings")
    ensure_link(BENCH_MOUNT / "dataset" / "librispeech", REMOTE_REPO / "benchmark" / "dataset" / "librispeech")
    ensure_link(BENCH_MOUNT / "dataset" / "librispeech_wer", REMOTE_REPO / "benchmark" / "dataset" / "librispeech_wer")
    ensure_link(BENCH_MOUNT / "dataset" / "words", REMOTE_REPO / "benchmark" / "dataset" / "words")
    for p in [MODEL_MOUNT / "deps", MODEL_MOUNT / "go-build", MODEL_MOUNT / "gomod", RESULT_MOUNT]:
        p.mkdir(parents=True, exist_ok=True)


def benchmark_env(accelerator: str, stt_engine: str, diar_engine: str, stt_precision: str = "int8") -> dict[str, str]:
    deps = MODEL_MOUNT / "deps"
    provider = "cuda" if accelerator in {"cuda", "gpu", "modal"} else "cpu"
    sherpa_dir = (
        deps / "sherpa-onnx-v1.13.2-cuda-12.x-cudnn-9.x-linux-x64-gpu"
        if provider == "cuda"
        else deps / "sherpa-onnx-v1.13.2-linux-x64-shared-no-tts"
    )
    stt_models = {
        # fp16: our converted export (encoder.fp16.onnx …); pickModelFile resolves
        # the .fp16.onnx files since this dir has no plain .onnx / .int8.onnx.
        "fp16": deps / "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-fp16",
        "fp32": deps / "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3",
    }.get(stt_precision, deps / "sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8")
    return {
        # Seen by fetch-bench-deps.sh: fp32 triggers the one-time model download.
        "BENCH_STT_PRECISION": stt_precision,
        "NOTO_BENCH_DEPS": str(deps),
        "GOCACHE": str(MODEL_MOUNT / "go-build"),
        "GOMODCACHE": str(MODEL_MOUNT / "gomod"),
        "NOTO_COMPUTE": accelerator,
        "BENCH_ACCELERATOR": accelerator,
        "BENCH_STT_ENGINE": stt_engine,
        "BENCH_STT_MODELS": str(stt_models),
        "BENCH_DIAR_ENGINE": diar_engine,
        "BENCH_DIAR_MODELS": str(deps),
        "NOTO_SHERPA_BIN": str(sherpa_dir / "bin" / "sherpa-onnx-offline"),
        "NOTO_SHERPA_DIAR_BIN": str(sherpa_dir / "bin" / "sherpa-onnx-offline-speaker-diarization"),
        "NOTO_SHERPA_LIB": str(sherpa_dir / "lib"),
        "NOTO_SHERPA_PROVIDER": provider,
        "NOTO_SHERPA_SEG_PROVIDER": provider,
        "NOTO_SHERPA_EMB_PROVIDER": provider,
        "NOTO_FFMPEG": str(deps / "ffmpeg"),
        "FFMPEG": str(deps / "ffmpeg"),
        "NOTO_SHERPA_THREADS": str(os.cpu_count() or 4),
        "NOTO_SHERPA_SEG_MODEL": str(deps / "sherpa-onnx-pyannote-segmentation-3-0" / "model.onnx"),
        "NOTO_SHERPA_EMB_MODEL": str(deps / "diar-embedding.onnx"),
        # Warm pyannote server engine (used when BENCH_DIAR_ENGINE selects it).
        # HF models cache into the model volume so the gated pipeline downloads
        # once per volume, not per run; HF_TOKEN arrives via a Modal secret.
        "NOTO_PYANNOTE_SCRIPT": str(REMOTE_REPO / "scripts" / "pyannote_diar_server.py"),
        "NOTO_PYANNOTE_PYTHON": "python3",
        # Warm STT server engine (used when BENCH_STT_ENGINE selects it). The
        # NeMo server resolves its .nemo from the HF cache (NOTO_PARAKEET_MODEL);
        # the Go engine keeps owning chunking + word reconstruction.
        "NOTO_PARAKEET_SCRIPT": str(REMOTE_REPO / "scripts" / "parakeet_stt_server.py"),
        # NeMo scratch cache: container-LOCAL on purpose. The 2.4 GB .nemo itself
        # lives in the HF cache on the model volume; sharing a mutable extraction
        # cache across concurrent runs invites torn last-writer-wins commits.
        "NEMO_CACHE_DIR": "/tmp/nemo",
        # GPU runs must FAIL FAST if a server can't get CUDA, never silently run
        # on CPU: the broken-MPS run burned $1.32 / 31 min of GPU rental doing
        # CPU diarization at 0% util before this guard existed.
        "NOTO_PYANNOTE_DEVICE": provider,
        "NOTO_PARAKEET_PYTHON": "python3",
        "HF_HOME": str(MODEL_MOUNT / "hf"),
        "BENCH_SYNTH_DIR": str(REMOTE_REPO / "benchmark" / "dataset" / "synthetic_meetings"),
        # Sampling (-hours/-seed) is passed as go-test flags per command, not via
        # env — the Go benches read flags, not BENCH_HOURS. See go_test().
    }


def validate_runtime(accelerator: str = ACCELERATOR, stt_engine: str = STT_ENGINE, diar_engine: str = DIAR_ENGINE) -> None:
    cpu_only = {"sherpa-parakeet", "sherpa-pyannote"}
    cuda_requested = accelerator in {"cuda", "gpu", "modal"}
    if not cuda_requested:
        return
    bad = sorted(cpu_only & {stt_engine, diar_engine})
    if bad:
        raise RuntimeError(
            "refusing to run Modal GPU benchmark with CPU-only engine(s): "
            + ", ".join(bad)
            + ". Set BENCH_STT_ENGINE/BENCH_DIAR_ENGINE to CUDA-capable engines "
            + "or set BENCH_ACCELERATOR=cpu to run the CPU baseline intentionally."
        )
    if not stt_engine.startswith("cuda-") or not diar_engine.startswith("cuda-"):
        raise RuntimeError(
            "Modal GPU benchmark requires explicit CUDA engine aliases "
            f"(got STT={stt_engine!r}, diarizer={diar_engine!r}). "
            "Use BENCH_STT_ENGINE=cuda-parakeet BENCH_DIAR_ENGINE=cuda-pyannote."
        )


def parse_go_test_json(raw: str) -> dict[str, Any]:
    events = []
    packages: dict[str, dict[str, Any]] = {}
    tests: dict[str, dict[str, Any]] = {}
    kpis: dict[str, Any] = {}
    issues: list[dict[str, str]] = []

    patterns = [
        ("stt.synthetic.wer", re.compile(r"OVERALL SYNTH WER ([0-9.]+)% over ([0-9]+) ref words; RTF ([0-9.]+)")),
        ("stt.ami.wer", re.compile(r"OVERALL WER ([0-9.]+)% over ([0-9]+) ref words")),
        ("diar.synthetic.der", re.compile(r"OVERALL SYNTH DER ([0-9.]+)% over ([0-9.]+)s ref speech; mean attribution ([0-9.]+)%; RTF ([0-9.]+)")),
        ("diar.ami.der", re.compile(r"OVERALL DER ([0-9.]+)% over ([0-9.]+)s ref speech; mean attribution ([0-9.]+)%")),
        ("e2e.synthetic", re.compile(r"OVERALL\s+WER ([0-9.]+)%\s+DER ([0-9.]+)%\s+cpWER ([0-9.]+)%\s+\(attribution tax ([+-]?[0-9.]+) pts\)")),
    ]

    for line in raw.splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        events.append(event)
        pkg = event.get("Package", "")
        action = event.get("Action", "")
        test = event.get("Test", "")
        if pkg and action in {"pass", "fail", "skip"} and not test:
            packages[pkg] = {"action": action, "elapsed": event.get("Elapsed", 0)}
        if test and action in {"pass", "fail", "skip"}:
            tests[f"{pkg}.{test}"] = {"action": action, "elapsed": event.get("Elapsed", 0)}
            if action == "fail":
                issues.append({"kind": "test_failure", "test": f"{pkg}.{test}"})
        out = event.get("Output", "")
        if "FAIL" in out or "REGRESSED" in out or "exceeds ceiling" in out:
            issues.append({"kind": "output", "package": pkg, "test": test, "message": out.strip()})
        for name, pattern in patterns:
            m = pattern.search(out)
            if not m:
                continue
            vals = [float(x) for x in m.groups()]
            if name == "stt.synthetic.wer":
                kpis[name] = {"wer_pct": vals[0], "ref_words": int(vals[1]), "rtf": vals[2]}
            elif name == "stt.ami.wer":
                kpis[name] = {"wer_pct": vals[0], "ref_words": int(vals[1])}
            elif name == "diar.synthetic.der":
                kpis[name] = {"der_pct": vals[0], "ref_speech_sec": vals[1], "mean_attribution_pct": vals[2], "rtf": vals[3]}
            elif name == "diar.ami.der":
                kpis[name] = {"der_pct": vals[0], "ref_speech_sec": vals[1], "mean_attribution_pct": vals[2]}
            elif name == "e2e.synthetic":
                kpis[name] = {"wer_pct": vals[0], "der_pct": vals[1], "cpwer_pct": vals[2], "attribution_tax_pts": vals[3]}

    return {"packages": packages, "tests": tests, "kpis": kpis, "issues": issues, "events": events}


def go_test(run: str, pkg: str, hours: float, seed: int, jobs: int = 1) -> list[str]:
    """One `go test` invocation with the suite's flags attached the way the test
    binary expects them: AFTER the package path, behind `-args`. go test only
    forwards a flag it doesn't itself recognise (`-hours`/`-seed`/`-jobs`) once it
    has seen the package, so flags before the path make it look for a package
    named "." instead. `-parallel` IS a go-test flag, so it goes before the path.
    `-hours=0` is the whole corpus (omitted); `-seed`/`-jobs` ride along only when
    they differ from the default, keeping commands minimal and reproducible."""
    # -timeout: go's default is 10m, which a cold-ish or experimental run can
    # exceed mid-meeting; run_cmd's own 120m subprocess timeout stays the backstop.
    cmd = ["./scripts/go", "test", "-json", "-v", "-count=1", "-timeout", "110m"]
    if jobs > 1:
        cmd += ["-parallel", str(jobs)]  # let Go release enough parallel subtests
    if run:
        cmd += ["-run", run]
    cmd.append(pkg)
    args = []
    if hours > 0:
        args += [f"-hours={hours}", f"-seed={seed}"]
    if jobs != 1:
        args.append(f"-jobs={jobs}")
    if args:
        cmd += ["-args"] + args
    return cmd


def commands_for_suite(suite: str, hours: float, seed: int, jobs: int = 1, quick: bool = False) -> list[list[str]]:
    """The go-test invocations for a suite. `quick` keeps only the chained e2e
    test — it yields WER, DER and cpWER in a single pass, so it's the fastest
    'is it working / did it improve' signal without re-running the atomic STT and
    diarize passes the e2e already performs internally."""
    if suite == "synthetic":
        cmds = [
            go_test("TestSTTSynthetic", "./benchmark/stt", hours, seed, jobs),
            go_test("TestDiarizeSynthetic", "./benchmark/diarize", hours, seed, jobs),
            go_test("TestSynthAttributedPipeline", "./benchmark/e2e", hours, seed, jobs),
        ]
        return cmds[-1:] if quick else cmds
    if suite == "ami":
        # AMI is GPU-inference-only: TestAMICapture runs STT ∥ diarization → merge
        # and writes one hyp JSON per meeting to BENCH_HYP_DIR (on the result
        # volume). WER/DER/cpWER are scored LOCALLY off the streamed-back hyps
        # (`go test ./benchmark/e2e -run TestAMIScore`), so no transcript
        # references and no scoring ride on the rented GPU. `quick` is moot — the
        # single capture pass already yields everything the local scorer needs.
        return [go_test("TestAMICapture", "./benchmark/e2e", hours, seed, jobs)]
    if suite == "all":
        return commands_for_suite("synthetic", hours, seed, jobs, quick) + commands_for_suite("ami", hours, seed, jobs, quick)
    raise ValueError(f"unknown suite {suite!r}; expected synthetic|ami|all")


def run_commands(cmds, *, env, timeout, probe_gpu, concurrent):
    """Run the suite's go-test commands. prod runs them sequentially (clean
    per-package GPU attribution, honest single-stream timing). fast runs them at
    once so the packages overlap on one GPU; each still probes the (whole-GPU)
    nvidia-smi independently, and results are returned in submission order so the
    KPI parser is unaffected."""
    if not concurrent or len(cmds) <= 1:
        return [run_cmd(c, env=env, timeout=timeout, probe_gpu=probe_gpu) for c in cmds]
    from concurrent.futures import ThreadPoolExecutor

    with ThreadPoolExecutor(max_workers=len(cmds)) as ex:
        futures = [ex.submit(run_cmd, c, env=env, timeout=timeout, probe_gpu=probe_gpu) for c in cmds]
        return [f.result() for f in futures]


@app.function(
    gpu=FUNCTION_GPU,
    volumes={str(MODEL_MOUNT): model_volume, str(BENCH_MOUNT): bench_volume, str(RESULT_MOUNT): result_volume},
    timeout=60 * 60 * 6,
    startup_timeout=60 * 30,
    scaledown_window=SCALEDOWN_WINDOW,
    min_containers=MIN_CONTAINERS,
    cpu=8,
    memory=32768,
)
def run_remote_benchmark(
    suite: str,
    hours: float,
    seed: int,
    profile: str,
    jobs: int,
    quick: bool,
    fetch_ami: bool,
    rebuild_synth: bool,
    accelerator: str,
    stt_engine: str,
    diar_engine: str,
    gpu_name: str | None,
    cpu_cores: int,
    mem_gib: float,
    stt_precision: str = "int8",
    synth_meetings: str = "3",
    batch_size: int = 0,
    batch_wait_ms: int = 0,
    e2e_batch: bool = False,
    ami_meetings: str = "",
) -> dict[str, Any]:
    validate_runtime(accelerator, stt_engine, diar_engine)
    started = time.monotonic()
    # Second-resolution timestamps COLLIDE when two runs launch concurrently (two
    # A/B arms started in the same second overwrote each other's result-volume
    # dir); the random suffix keeps every run's results dir unique.
    run_id = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:6]
    log(
        f"remote run starting run_id={run_id} suite={suite} profile={profile} jobs={jobs} quick={quick} "
        f"hours={hours} seed={seed} gpu={gpu_name or 'cpu'} stt={stt_engine} diar={diar_engine}"
    )
    prepare_layout()
    env = benchmark_env(accelerator, stt_engine, diar_engine, stt_precision)
    # With several meetings in flight (fast/jobs>1), divide the per-process CPU
    # threads so the parallel sherpa decoders don't oversubscribe the container's
    # cores (oversubscription, not the GPU, is what makes parallel runs crawl).
    # A single stream (prod/jobs=1) keeps all the cores for the one decoder; the
    # e2e chain briefly overlaps its (GPU-bound) STT with its diarizer, which is
    # a mild 2× that the scheduler absorbs — not the N× thrash this guards.
    threads = str(cpu_cores if jobs <= 1 else max(2, cpu_cores // jobs))
    env["NOTO_SHERPA_THREADS"] = threads
    # fast = throughput shape: the atomic STT bench feeds the whole corpus
    # through ONE warm CLI pass (one model load / CUDA init per run). prod keeps
    # the per-meeting path so single-stream RTF stays honestly attributable.
    if profile == "fast":
        env["BENCH_STT_BATCH"] = "1"
    if e2e_batch:
        env["BENCH_E2E_BATCH"] = "1"
    if batch_size > 0:
        env["BENCH_BATCH_SIZE"] = str(batch_size)
    if batch_wait_ms > 0:
        env["BENCH_BATCH_WAIT_MS"] = str(batch_wait_ms)
    # AMI capture streams per-meeting hypotheses to the result volume; the GPU box
    # never scores. They are pulled back into .modal-results/<run_id>/hyps/ and
    # scored locally (see write_local_results + commands_for_suite("ami")).
    if suite in {"ami", "all"}:
        env["BENCH_HYP_DIR"] = str(RESULT_MOUNT / run_id / "hyps")
        if not ami_meetings:
            # Auto-fill a backlog sized to the concurrency so the scheduler always
            # has more meetings than workers to stagger (fills the GPU idle gaps).
            ami_meetings = ",".join(auto_ami_meetings(jobs, QUEUE_DEPTH))
            log(f"auto-fill AMI meetings: jobs={jobs} × depth={QUEUE_DEPTH} → {ami_meetings}")
        if ami_meetings:
            env["BENCH_AMI_MEETINGS"] = ami_meetings
    cuda_requested = accelerator in {"cuda", "gpu", "modal"}
    # Warm GPU servers (pyannote/parakeet) keep their model resident and are
    # GPU-bound; running STT-atomic ∥ diarize-atomic ∥ e2e at once then stacks
    # multiple resident models on one card and OOMs a 24 GB L4 (measured: batched
    # STT alone peaks ~22 GB). With a server engine, run packages SEQUENTIALLY
    # (meeting-level -jobs parallelism within each package is kept); the GPU is
    # the bottleneck anyway, so back-to-back packages still saturate it.
    uses_warm_server = "-server" in stt_engine or "-server" in diar_engine
    concurrent_pkgs = profile == "fast" and not uses_warm_server

    setup: list[dict[str, Any]] = []
    if cuda_requested:
        setup.append(run_cmd(["nvidia-smi"], env=env, timeout=30))
    if cuda_requested and os.getenv("BENCH_CUDA_MPS", "") == "1":
        # One shared CUDA context for the whole engine pool (see KNOB_FORWARDS).
        # The env vars ride `env` into every benchmark process, which is how the
        # spawned STT/diar servers become MPS clients.
        env["CUDA_MPS_PIPE_DIRECTORY"] = "/tmp/nvidia-mps"
        env["CUDA_MPS_LOG_DIRECTORY"] = "/tmp/nvidia-mps-log"
        setup.append(
            run_cmd(
                ["bash", "-c",
                 "mkdir -p /tmp/nvidia-mps /tmp/nvidia-mps-log && "
                 "(nvidia-cuda-mps-control -d && echo MPS daemon started) || echo MPS unavailable"],
                env=env,
                timeout=30,
            )
        )
    setup.append(run_cmd(["bash", "scripts/fetch-bench-deps.sh"], env=env, timeout=60 * 60))
    if NEEDS_NEMO:
        # Pre-stage the .nemo into the (persistent) model volume during SETUP so
        # the one-time ~2.4 GB download never pollutes the timed benchmark
        # commands — the same policy as every other model asset.
        setup.append(
            run_cmd(
                [
                    "python3",
                    "-c",
                    "import os; from huggingface_hub import snapshot_download; "
                    "print(snapshot_download(os.getenv('NOTO_PARAKEET_MODEL', 'nvidia/parakeet-tdt-0.6b-v3')))",
                ],
                env=env,
                timeout=60 * 30,
            )
        )
    if fetch_ami or suite in {"ami", "all"}:
        ami_cmd = ["python3", "benchmark/identity/fetch.py"]
        if ami_meetings:  # only fetch the meetings this run will actually score
            ami_cmd += [m.strip() for m in ami_meetings.split(",") if m.strip()]
        setup.append(run_cmd(ami_cmd, env=env, timeout=60 * 90))
    synth_dir = REMOTE_REPO / "benchmark" / "dataset" / "synthetic_meetings"
    if rebuild_synth or not (synth_dir / "meetings.json").exists():
        setup.append(
            run_cmd(
                [
                    "python3",
                    "benchmark/dataset/build_synthetic_meetings.py",
                    "--speakers",
                    "6",
                    "--per-meeting",
                    "4",
                    "--meetings",
                    str(synth_meetings),
                    "--clips-per-speaker",
                    "14",
                    "--overlap-frac",
                    "0.18",
                    "--seed",
                    "7",
                ],
                env=env,
                timeout=60 * 45,
            )
        )

    raw_parts = []
    commands = []
    exit_code = 0
    gpu_peak_memory_mb = 0
    gpu_peak_utilization_pct = 0
    util_samples: list[int] = []
    cmds = commands_for_suite(suite, hours, seed, jobs, quick)
    for res in run_commands(cmds, env=env, timeout=60 * 120, probe_gpu=cuda_requested, concurrent=concurrent_pkgs):
        command_summary = {k: v for k, v in res.items() if k not in {"output", "gpu_samples"}}
        samples = res.get("gpu_samples", [])
        command_summary["gpu_sample_count"] = len(samples)
        if samples:
            command_summary["gpu_first_sample"] = samples[0]
            command_summary["gpu_last_sample"] = samples[-1]
            command_summary.update(utilization_stats([s.get("utilization_pct") for s in samples]))
        commands.append(command_summary)
        gpu_peak_memory_mb = max(gpu_peak_memory_mb, res.get("gpu_peak_memory_mb") or 0)
        gpu_peak_utilization_pct = max(gpu_peak_utilization_pct, res.get("gpu_peak_utilization_pct") or 0)
        util_samples.extend(s.get("utilization_pct") for s in samples if s.get("utilization_pct") is not None)
        raw_parts.append(res["output"])
        if res["exit_code"] != 0:
            exit_code = res["exit_code"]

    raw = "\n".join(raw_parts)
    parsed = parse_go_test_json(raw)
    duration_ms = int((time.monotonic() - started) * 1000)
    gpu_for_cost = gpu_name if cuda_requested else None
    cost = estimate_cost_usd(gpu_for_cost, duration_ms, cpu_cores, mem_gib)
    util = utilization_stats(util_samples)
    # $/audio-hr: the headline number. Derived from the audio actually transcribed
    # (AMI hyps already on the result volume), not the requested sampling `hours`.
    if suite in {"ami", "all"}:
        audio_hours = audio_hours_from_hyps(RESULT_MOUNT / run_id / "hyps")
    elif suite == "synthetic":
        audio_hours = audio_hours_from_synth_meta(REMOTE_REPO / "benchmark" / "dataset" / "synthetic_meetings", parsed["tests"])
    else:
        audio_hours = 0.0
    cost_per_audio_hour = round(cost / audio_hours, 5) if audio_hours > 0 else None
    summary = {
        "run_id": run_id,
        "suite": suite,
        "profile": profile,
        "jobs": jobs,
        "quick": quick,
        "e2e_batch": e2e_batch,
        "batch_size_configured": batch_size,
        "batch_wait_ms": batch_wait_ms,
        "hours": hours,
        "requested_audio_hours": hours,
        "seed": seed,
        "exit_code": exit_code,
        "gpu": gpu_name or "cpu",
        "cuda_image": CUDA_IMAGE if cuda_requested else "",
        "accelerator": accelerator,
        "stt_engine": stt_engine,
        "stt_precision": stt_precision,
        "diar_engine": diar_engine,
        "gpu_peak_memory_mb": gpu_peak_memory_mb,
        "gpu_peak_utilization_pct": gpu_peak_utilization_pct,
        "gpu_mean_utilization_pct": util["gpu_mean_utilization_pct"],
        "gpu_busy_pct": util["gpu_busy_pct"],
        "gpu_hourly_usd": gpu_hourly(gpu_name) if cuda_requested else 0.0,
        "cpu_cores": cpu_cores,
        "memory_gib": mem_gib,
        # Every tuning knob that reached this box (container env), so a result is
        # interpretable without the shell history that launched it.
        "knobs": {ce: os.environ[ce] for _, ce, _ in KNOB_FORWARDS if os.getenv(ce, "")},
        "estimated_cost_usd": cost,
        "processed_audio_hours": round(audio_hours, 4),
        "cost_per_audio_hour_usd": cost_per_audio_hour,
        "model_volume": MODEL_VOLUME,
        "benchmark_volume": BENCH_VOLUME,
        "scaledown_window_seconds": SCALEDOWN_WINDOW,
        "min_containers": MIN_CONTAINERS,
        "duration_ms": duration_ms,
        "setup": [{k: v for k, v in s.items() if k != "output"} for s in setup],
        "commands": commands,
        "kpis": parsed["kpis"],
        "issues": parsed["issues"],
        "packages": parsed["packages"],
        "tests": parsed["tests"],
    }
    if cuda_requested and exit_code == 0 and gpu_peak_memory_mb <= 0:
        summary["exit_code"] = 86
        summary["issues"].append(
            {
                "kind": "gpu_not_observed",
                "message": "CUDA benchmark completed but nvidia-smi never observed GPU memory usage during benchmark commands.",
            }
        )
        exit_code = 86

    out_dir = RESULT_MOUNT / run_id
    out_dir.mkdir(parents=True, exist_ok=True)
    (out_dir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    (out_dir / "raw.jsonl").write_text(raw)
    (out_dir / "setup.log").write_text("\n\n".join(s["output"] for s in setup))
    model_volume.commit()
    bench_volume.commit()
    result_volume.commit()
    # Collect the AMI capture hyps (tiny JSON) for the return so the local side can
    # score them off-GPU. They were also written incrementally to the (committed)
    # result volume, so a crash mid-run keeps the meetings already finished.
    hyps: dict[str, str] = {}
    hyp_dir = out_dir / "hyps"
    if hyp_dir.is_dir():
        for hp in sorted(hyp_dir.glob("*.json")):
            hyps[hp.name] = hp.read_text()
    log(f"remote run finished run_id={run_id} exit_code={exit_code} hyps={len(hyps)}")
    return {
        "summary": summary,
        "raw_jsonl": raw,
        "setup_log": (out_dir / "setup.log").read_text(),
        "hyps": hyps,
    }


def write_local_results(result: dict[str, Any], out: Path) -> None:
    summary = result["summary"]
    run_dir = out / summary["run_id"]
    run_dir.mkdir(parents=True, exist_ok=True)
    (run_dir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    (run_dir / "raw.jsonl").write_text(result["raw_jsonl"])
    (run_dir / "setup.log").write_text(result.get("setup_log", ""))
    hyps = result.get("hyps") or {}
    if hyps:
        hyp_dir = run_dir / "hyps"
        hyp_dir.mkdir(parents=True, exist_ok=True)
        for name, content in hyps.items():
            (hyp_dir / name).write_text(content)
        print(f"fetched {len(hyps)} AMI hyps -> {hyp_dir}  "
              f"(score: BENCH_HYP_DIR={hyp_dir} go test ./benchmark/e2e -run TestAMIScore -v)")
    latest = out / "latest"
    if latest.exists() or latest.is_symlink():
        if latest.is_dir() and not latest.is_symlink():
            shutil.rmtree(latest)
        else:
            latest.unlink()
    latest.symlink_to(run_dir.name)


def launch(
    *,
    suite: str,
    hours: float,
    seed: int,
    profile: str,
    jobs: int | None,
    quick: bool,
    fetch_ami: bool,
    rebuild_synth: bool,
    gpu: str,
    batch_size: int = BATCH_SIZE,
    batch_wait_ms: int = BATCH_WAIT_MS,
    e2e_batch: bool = E2E_BATCH,
) -> dict[str, Any]:
    """Invoke the remote benchmark once, selecting the GPU AND container shape
    per call via with_options so one process can run the same suite across
    several cards, and a prod run is billed for the small container it needs."""
    gpu_name = None if ACCELERATOR == "cpu" else gpu
    shape = container_shape(profile)
    # The AMI capture runs meetings concurrently up to -jobs, each on its OWN warm
    # (Parakeet + pyannote) process pair so the GPU stays fed across the CPU
    # clustering tail (a single shared server would serialize at its mutex). That
    # holds eff_jobs process pairs at once, which the prod single-stream shape
    # (4 vCPU / 8 GiB) can't host — scale cores+RAM with the concurrency so CPU/RAM
    # starvation never becomes the bottleneck instead of the GPU.
    #
    # RIGHT-SIZED (2026-06-11): the first cut used 2*jobs cores / 6 GiB*jobs, which
    # raised the jobs=4 container ~14.5 % and ate ~half the concurrency win (it netted
    # only −10 % $/audio-hr vs +28 % wall). The heavy compute (STT encoder, pyannote
    # embedding) is ON THE GPU; host CPU is touched only by the BURSTY clustering tail
    # + IPC/audio-decode, and the big models live in VRAM, not host RAM. So cores need
    # only cover a few simultaneous tails (jobs+2) and RAM a torch+sherpa process pair
    # each (~4 GiB/job). GPU still dominates the bill, but this stops over-paying for
    # idle cores/RAM. Conservative trim (not jobs+0 / 2 GiB) since the summary records
    # only PROVISIONED cpu/mem — an OOM/starvation would waste the run, so we leave
    # headroom. Other suites share one warm server per engine, so keep the profile shape.
    eff_jobs = resolve_jobs(profile, jobs)
    # All tuning knobs travel through ONE table (KNOB_FORWARDS). The validated
    # stage-pool split (BOTTLENECK.md 16th pass) is derived here when unset, so
    # the easy invocation — `run --suite ami --profile batch` — IS the tuned one:
    # STT≈0.4×jobs engines saturate the card's STT FLOP ceiling while diar=jobs
    # keeps every in-flight meeting holding only the engine its stage needs.
    knob_env: dict[str, str] = {}
    for local, container, default in KNOB_FORWARDS:
        val = os.getenv(local, "") or default
        if val:
            knob_env[container] = val
    if gpu_name and suite in {"ami", "all"} and eff_jobs > 1:
        if NEEDS_NEMO:
            # One NeMo process serves every meeting: a {"wavs": [...]} request is
            # ONE padded batch, so throughput comes from batching inside the
            # engine, not from process-level replication (which would only
            # multiply the 2.4 GB resident weights). chunk_batch=32 sends a whole
            # meeting's ~18 chunks per request.
            knob_env.setdefault("BENCH_STT_POOL", "1")
            knob_env.setdefault("NOTO_PARAKEET_CHUNK_BATCH", "32")
        else:
            # CLI-baseline engines replicate per process; the validated split is
            # STT≈0.4×jobs (BOTTLENECK.md 16th pass).
            knob_env.setdefault("BENCH_STT_POOL", str(max(1, round(eff_jobs * 2 / 5))))
        knob_env.setdefault("BENCH_DIAR_POOL", str(eff_jobs))
        if NEEDS_PYANNOTE:
            # One pyannote process serves the whole diar pool (the pool's N Go
            # handles share it), so GPU-side diar concurrency is the WORKER
            # count, not the pool size. Default workers to the in-flight
            # meeting count so every meeting in its diar stage gets a worker.
            knob_env.setdefault("NOTO_PYANNOTE_WORKERS", str(eff_jobs))
    if gpu_name and NEEDS_PYANNOTE:
        # Synthetic smoke uses the same warm pyannote server but has no AMI pool
        # branch above. Keep its worker replicas aligned to effective concurrency
        # too; otherwise a one-meeting smoke boots four replicas and can crash
        # while extra workers are still loading during the first request.
        knob_env.setdefault("NOTO_PYANNOTE_WORKERS", str(eff_jobs))
    # Container shape scales with the RESIDENT ENGINE COUNT, not jobs: the heavy
    # compute lives on the GPU; the host only runs the bursty clustering tails,
    # IPC and audio decode, plus one python process per pooled engine. MEASURED
    # (17th pass, arm A1): jobs=10 with 14 engines runs at full speed on
    # 4 vCPU / 16 GiB (same wall as 8/28, $/audio-hr −14%) — so ~0.4 cores/job
    # and ~1.2 GiB/engine, floors 4 cores / 8 GiB. The summary records what was
    # actually provisioned, so the cost estimate stays honest.
    if gpu_name and suite in {"ami", "all"} and eff_jobs > 1:
        engines = safe_int(knob_env.get("BENCH_STT_POOL", ""), eff_jobs) + safe_int(
            knob_env.get("BENCH_DIAR_POOL", ""), eff_jobs
        )
        shape = {
            "cpu": max(shape["cpu"], (2 * eff_jobs + 4) // 5),
            "memory": max(shape["memory"], 6 * 1024 * engines // 5),
        }
        if NEEDS_NEMO:
            # The single NeMo process is host-heavier than a sherpa one (torch +
            # NeMo imports + .nemo extraction) — give it headroom beyond the
            # 1.2 GiB/engine heuristic so an OOM never wastes a paid run.
            shape["memory"] += 6 * 1024
    # Explicit shape overrides for right-sizing experiments.
    if os.getenv("BENCH_CPU", ""):
        shape["cpu"] = int(os.environ["BENCH_CPU"])
    if os.getenv("BENCH_MEM_GIB", ""):
        shape["memory"] = int(float(os.environ["BENCH_MEM_GIB"]) * 1024)
    opts: dict[str, Any] = {}
    if gpu_name:
        opts = {"gpu": gpu_name, "cpu": shape["cpu"], "memory": shape["memory"]}
    if knob_env:
        # Knobs ride the CONTAINER env so they reach the Go test (os.environ
        # inherit) and the python servers it spawns, without signature changes.
        opts["env"] = {**opts.get("env", {}), **knob_env}
    if NEEDS_PYANNOTE:
        # No token needed: the server's default pipeline is the UNGATED
        # community mirror (pyannote-community/speaker-diarization-community-1,
        # CC-BY-4.0, self-contained), cached into the model volume via HF_HOME
        # on first use. HF_TOKEN is honored only when someone explicitly points
        # NOTO_PYANNOTE_PIPELINE at a gated repo; BENCH_PYANNOTE_OFFLINE=1
        # forbids hub contact entirely (serve from the volume cache).
        token = os.environ.get("HF_TOKEN", "")
        if token:
            opts["secrets"] = [modal.Secret.from_dict({"HF_TOKEN": token})]
        if os.environ.get("BENCH_PYANNOTE_OFFLINE", "") == "1":
            opts["env"] = {**opts.get("env", {}), "HF_HUB_OFFLINE": "1"}
    fn = run_remote_benchmark.with_options(**opts) if opts else run_remote_benchmark
    return fn.remote(
        suite,
        hours,
        seed,
        profile,
        resolve_jobs(profile, jobs),
        quick,
        fetch_ami,
        rebuild_synth,
        ACCELERATOR,
        STT_ENGINE,
        DIAR_ENGINE,
        gpu_name,
        shape["cpu"],
        shape["memory"] / 1024,
        STT_PRECISION,
        SYNTH_MEETINGS,
        batch_size,
        batch_wait_ms,
        e2e_batch,
        AMI_MEETINGS,
    )


@app.local_entrypoint()
def main(
    suite: str = "synthetic",
    hours: float = 0.0,
    seed: int = 1,
    profile: str = PROFILE,
    jobs: int = 0,
    quick: bool = False,
    gpu: str = GPU,
    batch_size: int = BATCH_SIZE,
    batch_wait_ms: int = BATCH_WAIT_MS,
    e2e_batch: bool = E2E_BATCH,
    fetch_ami: bool = False,
    rebuild_synth: bool = False,
    out: str = ".modal-results",
) -> None:
    result = launch(
        suite=suite, hours=hours, seed=seed, profile=profile,
        jobs=(jobs or None), quick=quick, fetch_ami=fetch_ami, rebuild_synth=rebuild_synth, gpu=gpu,
        batch_size=batch_size, batch_wait_ms=batch_wait_ms, e2e_batch=e2e_batch,
    )
    write_local_results(result, Path(out))
    s = result["summary"]
    cph = s.get("cost_per_audio_hour_usd")
    knobs = s.get("knobs", {})
    pools = (f"pools stt={knobs.get('BENCH_STT_POOL', '-')}/diar={knobs.get('BENCH_DIAR_POOL', '-')} "
             if knobs.get("BENCH_STT_POOL") or knobs.get("BENCH_DIAR_POOL") else "")
    print(
        "\n=== headline ===\n"
        f"  gpu={s.get('gpu')} precision={s.get('stt_precision')} jobs={s.get('jobs')} {pools}"
        f"cpu={s.get('cpu_cores')} mem={s.get('memory_gib')}GiB\n"
        f"  GPU util: peak {s.get('gpu_peak_utilization_pct')}%  mean {s.get('gpu_mean_utilization_pct')}%  "
        f"busy {s.get('gpu_busy_pct')}%  (mean≪peak ⇒ idle gaps to reclaim)\n"
        f"  audio processed: {s.get('processed_audio_hours')} hr   run cost: ${s.get('estimated_cost_usd')}\n"
        f"  ==> $/audio-hr: {('$%.5f' % cph) if cph is not None else 'n/a'}   (target <$0.01)\n"
    )
    print(json.dumps(s, indent=2))


def _kpi(summary: dict[str, Any]) -> str:
    k = summary.get("kpis", {})
    bits = []
    if "stt.synthetic.wer" in k:
        bits.append(f"WER {k['stt.synthetic.wer']['wer_pct']}%")
    if "diar.synthetic.der" in k:
        bits.append(f"DER {k['diar.synthetic.der']['der_pct']}%")
    if "e2e.synthetic" in k:
        bits.append(f"cpWER {k['e2e.synthetic']['cpwer_pct']}%")
    if "diar.ami.der" in k:
        bits.append(f"amiDER {k['diar.ami.der']['der_pct']}%")
    return "  ".join(bits) or "—"


def _print_sweep_table(rows: list[dict[str, Any]]) -> None:
    print("\n=== sweep: perf & price per GPU (single-stream vs parallel) ===")
    print(f"{'gpu':<12} {'profile':<6} {'jobs':>4} {'wall_s':>8} {'pk%':>4} {'mean%':>6} "
          f"{'mem_mb':>7} {'$/hr':>6} {'est_$':>8} {'$/aud-hr':>9}  kpis")
    for s in rows:
        cph = s.get("cost_per_audio_hour_usd")
        print(
            f"{str(s.get('gpu')):<12} {str(s.get('profile')):<6} {s.get('jobs', 0):>4} "
            f"{s.get('duration_ms', 0) / 1000:>8.1f} {s.get('gpu_peak_utilization_pct', 0):>4} "
            f"{s.get('gpu_mean_utilization_pct', 0):>6} "
            f"{s.get('gpu_peak_memory_mb', 0):>7} {s.get('gpu_hourly_usd', 0):>6.2f} "
            f"{s.get('estimated_cost_usd', 0):>8.4f} {(('%.5f' % cph) if cph is not None else '—'):>9}  {_kpi(s)}"
        )


def cli() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="cmd", required=True)

    def common(p: argparse.ArgumentParser) -> None:
        p.add_argument("--suite", choices=["synthetic", "ami", "all"], default=os.getenv("BENCH_SUITE", "synthetic"))
        p.add_argument("--hours", type=float, default=float(os.getenv("BENCH_HOURS", "0") or 0),
                       help="cap the run to ~N hours of audio (whole meetings; 0 = the whole corpus)")
        p.add_argument("--seed", type=int, default=int(os.getenv("BENCH_SEED", "1") or 1),
                       help="seed for the reproducible -hours sample (default 1, matching the local default)")
        p.add_argument("--quick", action="store_true", default=os.getenv("BENCH_QUICK", "1") != "0",
                       help="run only the chained e2e test (default; set BENCH_QUICK=0 for atomic diagnostics)")
        p.add_argument("--batch-size", type=int, default=BATCH_SIZE,
                       help="max meetings per batched e2e group (0 = all selected meetings)")
        p.add_argument("--batch-wait-ms", type=int, default=BATCH_WAIT_MS,
                       help="reserved for deployed dynamic batching; benchmark corpus runs normally use 0")
        p.add_argument("--e2e-batch", action="store_true", default=E2E_BATCH,
                       help="experimental: batch selected meetings inside the chained e2e test")
        p.add_argument("--fetch-ami", action="store_true")
        p.add_argument("--rebuild-synth", action="store_true")
        p.add_argument("--out", default=".modal-results")

    run = sub.add_parser("run", help="one benchmark run on one GPU")
    common(run)
    run.add_argument("--profile", choices=["prod", "batch", "fast"], default=PROFILE,
                     help="prod = single-stream (one user's latency); batch = pooled throughput "
                          "(value-for-money, validated $/audio-hr shape); fast = legacy paired-engine parallel")
    run.add_argument("--jobs", type=int, default=None,
                     help="override concurrent meetings (default: prod=1, fast=BENCH_JOBS or 8)")
    run.add_argument("--gpu", default=GPU, help=f"GPU type (default {GPU}); e.g. L4, L40S, A100-40GB, H100")

    sweep = sub.add_parser("sweep", help="run the suite across GPUs × profiles and print perf+price")
    common(sweep)
    sweep.add_argument("--gpus", default=os.getenv("BENCH_SWEEP_GPUS", "L4,L40S,A100-40GB"),
                       help="comma-separated GPU types to compare")
    sweep.add_argument("--profiles", default=os.getenv("BENCH_SWEEP_PROFILES", "prod,fast"),
                       help="comma-separated profiles to compare (prod = 1 stream, fast = max parallel)")
    sweep.add_argument("--jobs", type=int, default=None, help="override fast-profile concurrency")

    args = parser.parse_args()

    log(
        f"starting Modal app={APP_NAME} env={ENVIRONMENT or 'default'} accelerator={ACCELERATOR} "
        f"cuda_image={CUDA_IMAGE if ACCELERATOR != 'cpu' else ''}"
    )
    with modal.enable_output():
        with app.run(environment_name=ENVIRONMENT):
            if args.cmd == "run":
                validate_runtime()
                log(f"invoking remote benchmark profile={args.profile} gpu={args.gpu}")
                result = launch(
                    suite=args.suite, hours=args.hours, seed=args.seed, profile=args.profile,
                    jobs=args.jobs, quick=args.quick, fetch_ami=args.fetch_ami,
                    rebuild_synth=args.rebuild_synth, gpu=args.gpu,
                    batch_size=args.batch_size, batch_wait_ms=args.batch_wait_ms,
                    e2e_batch=args.e2e_batch,
                )
                write_local_results(result, Path(args.out))
                print(json.dumps(result["summary"], indent=2))
                return int(result["summary"].get("exit_code") or 0)

            if args.cmd == "sweep":
                validate_runtime()
                gpus = [g.strip() for g in args.gpus.split(",") if g.strip()]
                profiles = [p.strip() for p in args.profiles.split(",") if p.strip()]
                rows: list[dict[str, Any]] = []
                worst_exit = 0
                for g in gpus:
                    for p in profiles:
                        log(f"sweep: gpu={g} profile={p}")
                        try:
                            result = launch(
                                suite=args.suite, hours=args.hours, seed=args.seed, profile=p,
                                jobs=args.jobs, quick=args.quick, fetch_ami=args.fetch_ami,
                                rebuild_synth=args.rebuild_synth, gpu=g,
                                batch_size=args.batch_size, batch_wait_ms=args.batch_wait_ms,
                                e2e_batch=args.e2e_batch,
                            )
                        except Exception as exc:  # one unavailable GPU shouldn't sink the rest
                            log(f"sweep cell gpu={g} profile={p} FAILED: {exc}")
                            rows.append({"gpu": g, "profile": p, "jobs": 0, "estimated_cost_usd": 0.0,
                                         "kpis": {}, "error": str(exc)})
                            worst_exit = worst_exit or 1
                            continue
                        write_local_results(result, Path(args.out))
                        rows.append(result["summary"])
                        worst_exit = worst_exit or int(result["summary"].get("exit_code") or 0)
                _print_sweep_table(rows)
                return worst_exit
    return 64


if __name__ == "__main__":
    raise SystemExit(cli())

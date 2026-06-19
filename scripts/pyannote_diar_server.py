#!/usr/bin/env python3
"""Warm pyannote diarization server — ONE process, ONE CUDA context, N worker
threads — spoken to over stdin/stdout JSONL.

This is noto's single GPU diarization server (the v1 process-per-engine server
and the v1/v2 split were retired after the 19th benchmark pass; one
architecture, one code path). Each worker thread owns its OWN stock pyannote
pipeline replica (the pipeline object is not thread-safe; weights are tiny,
~100 MB, so K replicas cost little VRAM) on its own CUDA stream, and requests
are answered OUT OF ORDER as they finish — every response carries its request
id, and the concurrent Go client
(internal/platform/providers/diarize/pyannote_server.go) demultiplexes by id,
so one warm process serves many meetings at once.

MEASURED (BOTTLENECK.md, 19th pass, 30-meeting AMI A/B): one process with 10
worker threads delivers the same ~130–140× aggregate realtime as 10 separate
processes (uniform ~14×/stream at 10-way either way) — the diar ceiling is the
pyannote pipeline's GPU COMPUTE saturating the card, not serving topology.
What the one-process shape buys, measured: peak VRAM 30.2 → 22.3 GB, one host
python/torch stack instead of N, and per-meeting output identical to the digit
(DER 11.3 / cpWER 31.1). The remaining cost levers are compute ones (the
embedding engine, VAD), which is what NOTO_PYANNOTE_EMB_COMPILE probes.

Why the model loads matter: NEVER load pipeline replicas concurrently in one
process. 9 simultaneous Pipeline.from_pretrained calls killed the server
NATIVELY (no traceback, death mid-stderr-line ~1 s after ready, every restart
— measured on a 30-meeting run; concurrent torch.load/mmap off the FUSE-backed
model volume is the prime suspect). Replicas load STRICTLY one at a time, each
joining the serve loop as it completes, while requests already flow to the
ready subset.

Protocol (one JSON object per line):
  ← {"ready": true, "device": "cuda", "pipeline": "...", "workers": 4}
  → {"id": 1, "wav": "/abs/path.wav", "num_speakers": 4}      (num_speakers optional/0 = auto)
  ← {"id": 1, "turns": [{"speaker": "SPEAKER_00", "start": 0.5, "end": 3.2}, ...]}
  → {"id": 2, "wavs": [...], "num_speakers": [4, 3]}
  ← {"id": 2, "results": [{"turns": [...]}, {"turns": [...]}]}
  ← {"id": 1, "error": "..."}                                  (request-level failure)
  Responses are NOT ordered: a short meeting submitted after a long one
  finishes first. A lock-step client must keep at most ONE request
  outstanding; the Go client demultiplexes by id and needs no such care.

stdout carries ONLY protocol lines: fd 1 is re-pointed at stderr before any
heavy import and protocol lines go to a private dup of the original stdout
under a lock (multiple workers emit concurrently). The default pipeline is the
UNGATED community mirror of pyannote's community-1 (same weights, CC-BY-4.0,
self-contained) — no Hugging Face account or token is needed by anyone;
HF_TOKEN is honored only for explicitly-configured gated pipelines.

Env:
  NOTO_PYANNOTE_PIPELINE    HF pipeline id or local pipeline dir
                            (default pyannote-community/speaker-diarization-community-1)
  NOTO_PYANNOTE_DEVICE      cuda | cpu | auto (default auto; cuda FAILS FAST
                            without CUDA — a "GPU" run must never silently
                            diarize on CPU at 0% util)
  NOTO_PYANNOTE_FP16        reduced-precision mode (default emb-bf16 — the
                            validated embedding-module bf16 wrap; see
                            wrap_embedding_amp for the full mode list)
  NOTO_PYANNOTE_EMB_COMPILE torch.compile the embedding ResNet's forward
                            (83–86% of diar infer). "" / "0" = off (default
                            until DER-gated); "1"/"default" = default mode;
                            any other value is passed as the compile mode.
                            Do NOT use "reduce-overhead" here: its CUDA graphs
                            don't compose with the per-worker streams.
  NOTO_PYANNOTE_SEG_AMP     segmentation autocast: bf16 | fp16 (default off —
                            measured +1.2 DER for nothing)
  NOTO_PYANNOTE_SEG_BATCH / NOTO_PYANNOTE_EMB_BATCH
                            window batch sizes, 0 = pyannote's default
                            (measured: compute-bound, bigger batches only buy VRAM)
  NOTO_PYANNOTE_TIMING      per-request stage timing on stderr (default on)
  NOTO_PYANNOTE_WORKERS     worker threads = pipeline replicas = max concurrent
                            meetings on the GPU (default 4)
  NOTO_PYANNOTE_STREAMS     1 (default) = one CUDA stream per worker so forwards
                            can overlap; 0 = all workers share the default stream
  NOTO_PYANNOTE_FAKE        1 = no-torch protocol selftest (deterministic turns
                            from the WAV header, staggered completion to exercise
                            out-of-order delivery; for $0 local testing)
  NOTO_VAD / NOTO_VAD_PAD / NOTO_VAD_MIN_GAP
                            Silero-VAD silence trimming (TODO-7): diarize only
                            the speech regions, remap turn times back to the
                            original timeline. Shared with the STT server —
                            see vad_trim.py for semantics and safety posture.
  HF_TOKEN                  only for explicitly-configured gated pipelines
"""

from __future__ import annotations

import contextlib
import json
import os
import sys
import threading
import time

import vad_trim


def claim_protocol_stdout():
    """Reserve real stdout for protocol lines; everything else → stderr."""
    proto_fd = os.dup(1)
    os.dup2(2, 1)  # fd 1 (incl. C-level writes) now lands on stderr
    return os.fdopen(proto_fd, "w", buffering=1)


PROTO = claim_protocol_stdout()
EMIT_LOCK = threading.Lock()


def log(msg: str) -> None:
    print(f"[pyannote-server] {msg}", file=sys.stderr, flush=True)


def _int_env(name: str, default: int) -> int:
    try:
        return int(os.getenv(name, "") or default)
    except ValueError:
        return default


def emit(obj: dict) -> None:
    line = json.dumps(obj) + "\n"
    with EMIT_LOCK:
        PROTO.write(line)
        PROTO.flush()


# ── quality-critical helpers ─────────────────────────────────────────────────
# These decide WHAT the pipeline computes (waveform decode, precision wraps,
# batching) — every worker replica goes through the same functions, so
# per-request output cannot differ across workers.


def load_waveform(path: str):
    """Decode a WAV file to a (channel, time) float32 tensor + sample rate
    WITHOUT torchcodec.

    pyannote.audio 4.x loads files through torchcodec, whose AudioDecoder needs
    FFmpeg's shared libraries present in the environment; without them the call
    fails with "name 'AudioDecoder' is not defined". Handing the pipeline a
    waveform dict skips file decoding entirely, so the diarizer doesn't depend on
    a system FFmpeg/torchcodec ABI match. stdlib `wave` reads PCM WAV (the format
    noto produces and the synthetic corpus uses); a non-PCM/float WAV raises and
    falls back to the ffmpeg BINARY noto already ships (NOTO_FFMPEG/FFMPEG)."""
    import numpy as np
    import torch

    try:
        import wave

        with wave.open(path, "rb") as w:
            sr = w.getframerate()
            nch = w.getnchannels()
            width = w.getsampwidth()
            frames = w.readframes(w.getnframes())
        if width == 2:
            data = np.frombuffer(frames, dtype="<i2").astype(np.float32) / 32768.0
        elif width == 1:  # WAV 8-bit is unsigned
            data = (np.frombuffer(frames, dtype=np.uint8).astype(np.float32) - 128.0) / 128.0
        elif width == 4:
            data = np.frombuffer(frames, dtype="<i4").astype(np.float32) / 2147483648.0
        elif width == 3:  # 24-bit packed little-endian, sign-extended
            raw = np.frombuffer(frames, dtype=np.uint8).reshape(-1, 3).astype(np.int32)
            v = raw[:, 0] | (raw[:, 1] << 8) | (raw[:, 2] << 16)
            data = np.where(v & 0x800000, v - 0x1000000, v).astype(np.float32) / 8388608.0
        else:
            raise ValueError(f"unsupported WAV sample width {width}")
        if nch > 1:
            data = data.reshape(-1, nch).mean(axis=1)
        return torch.from_numpy(np.ascontiguousarray(data)).unsqueeze(0), sr
    except Exception as wav_exc:
        log(f"stdlib wave decode failed ({wav_exc}); falling back to ffmpeg binary")
        return _load_waveform_ffmpeg(path)


def _load_waveform_ffmpeg(path: str):
    import subprocess

    import numpy as np
    import torch

    ff = os.getenv("NOTO_FFMPEG") or os.getenv("FFMPEG") or "ffmpeg"
    proc = subprocess.run(
        [ff, "-nostdin", "-hide_banner", "-loglevel", "error",
         "-i", path, "-ar", "16000", "-ac", "1", "-f", "f32le", "-"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True,
    )
    data = np.frombuffer(proc.stdout, dtype="<f4").copy()
    return torch.from_numpy(data).unsqueeze(0), 16000


def configure_batching(pipeline, seg_batch: int, emb_batch: int) -> dict:
    """Raise pyannote's segmentation + embedding batch sizes so the GPU processes
    many sliding windows per forward pass instead of the small library default
    (~32).

    It is a pure THROUGHPUT knob and does NOT change the diarization OUTPUT:
    batching only groups otherwise-independent windows through the same model, so
    the segmentation scores, embeddings, and clustering are bit-identical — only
    the GPU is fed differently. MEASURED (BOTTLENECK.md tenth pass): the embedding
    ResNet is compute-bound here, so bigger batches bought no speed, only VRAM —
    default 0 = leave pyannote's own default; kept as an experiment knob.

    pyannote's attribute spelling has moved across 3.x/4.x (and the segmentation
    Inference captures its batch size at construction, so the pipeline-level
    attribute alone doesn't reach it), so set every known surface, guarded, and
    report what actually took."""
    applied: dict = {}
    if seg_batch <= 0 and emb_batch <= 0:
        return applied
    for attr, val in (("segmentation_batch_size", seg_batch), ("embedding_batch_size", emb_batch)):
        if val > 0 and hasattr(pipeline, attr):
            try:
                setattr(pipeline, attr, val)
                applied[attr] = val
            except Exception as exc:
                log(f"could not set {attr}={val}: {exc}")
    for obj_name, val in (("_segmentation", seg_batch), ("_embedding", emb_batch)):
        obj = getattr(pipeline, obj_name, None)
        if val > 0 and obj is not None and hasattr(obj, "batch_size"):
            try:
                obj.batch_size = val
                applied[f"{obj_name}.batch_size"] = val
            except Exception as exc:
                log(f"could not set {obj_name}.batch_size={val}: {exc}")
    return applied


def _embedding_module(embedding):
    """Find the torch nn.Module that runs the speaker-embedding forward, plus a
    label for logging.

    pyannote's PretrainedSpeakerEmbedding backends (WeSpeaker / SpeechBrain /
    pyannote-native) keep the ResNet as a torch Module at `.model_`, and their
    `__call__` runs that forward and then returns a NUMPY array. So autocast must
    wrap the inner Module — not `__call__` — or the numpy conversion happens on a
    reduced-precision tensor (and numpy has no bfloat16: "unsupported ScalarType
    BFloat16", the error that made the prior pass abandon bf16). Wrapping the
    Module lets us run the heavy compute in bf16 AND hand pyannote an fp32 result."""
    import torch

    if embedding is None:
        return None, "none"
    for attr in ("model_", "model", "classifier_"):
        m = getattr(embedding, attr, None)
        if isinstance(m, torch.nn.Module):
            return m, f"_embedding.{attr}"
    if isinstance(embedding, torch.nn.Module):
        return embedding, "_embedding"
    return None, type(embedding).__name__


def compile_embedding(pipeline, mode: str) -> bool:
    """torch.compile the speaker-embedding ResNet's forward — the fused-static-
    engine probe for the dominant diar compute (the embedding is 83–86% of diar
    infer and COMPUTE-bound, so kernel fusion is the one lever that reduces its
    FLOPs-seconds; serving topology was measured neutral in the 19th pass).

    Composes with the precision wraps: compile FIRST, so wrap_embedding_amp's
    autocast closure calls the compiled callable. Worker replicas share compiled
    artifacts via dynamo/inductor caching, but the first call per worker may
    still pay a compile/guard cost — the AMI DER gate (which also times the run)
    is the judge of whether fusion pays for itself.

    mode: "1"/"default" = torch.compile defaults; anything else is passed as
    torch.compile(mode=...). "reduce-overhead" is rejected: its CUDA graphs
    capture on a single stream and do not compose with the per-worker streams.
    Returns False (stay eager) when no module is found or compile setup fails —
    degrading to the proven baseline, never erroring the server."""
    import torch

    if mode == "reduce-overhead":
        log("emb-compile mode reduce-overhead is incompatible with per-worker CUDA streams; staying eager")
        return False
    module, where = _embedding_module(getattr(pipeline, "_embedding", None))
    if module is None:
        log(f"emb-compile requested but found no embedding nn.Module (got {where}); staying eager")
        return False
    kwargs = {} if mode in {"1", "default"} else {"mode": mode}
    try:
        module.forward = torch.compile(module.forward, **kwargs)
    except Exception as exc:
        log(f"torch.compile failed ({type(exc).__name__}: {exc}); staying eager")
        return False
    log(f"embedding torch.compile mode={mode} on {where} ({type(module).__name__})")
    return True


def wrap_segmentation_amp(pipeline, dtype) -> bool:
    """Run the segmentation model forward under reduced-precision autocast but
    return fp32 — the same module-level wrap as the embedding (see
    wrap_embedding_amp); AMI stage timing puts segmentation at ~11% of diar infer,
    so this is a minor lever, opt-in via NOTO_PYANNOTE_SEG_AMP=bf16 until a DER
    check on real audio clears it for default."""
    import torch

    seg = getattr(pipeline, "_segmentation", None)
    module, where = _embedding_module(seg)
    if module is None:
        log(f"seg-amp requested but found no segmentation nn.Module (got {where}); leaving fp32")
        return False

    orig_forward = module.forward

    def amp_forward(*args, **kwargs):
        with torch.autocast("cuda", dtype=dtype):
            out = orig_forward(*args, **kwargs)
        if isinstance(out, torch.Tensor):
            return out.float()
        if isinstance(out, (tuple, list)):
            return type(out)(o.float() if isinstance(o, torch.Tensor) else o for o in out)
        return out

    module.forward = amp_forward
    log(f"segmentation autocast {dtype} on {where} ({type(module).__name__}); output cast back to fp32")
    return True


def wrap_embedding_amp(pipeline, dtype) -> bool:
    """Run the speaker-embedding ResNet forward under reduced-precision autocast
    but return its result in fp32.

    The embedding model dominates diar time, so tensor-core throughput here is
    the dominant single-stream diar lever. Why bf16 and not fp16: fp16's narrow
    exponent range degenerates the embedding geometry → mis-clusters (MEASURED, DER
    9 → 66); bf16 keeps fp32's exponent range at the same tensor-core throughput.
    Why cast the output to fp32: pyannote converts the embedding to numpy for
    clustering, numpy has no bfloat16, and clustering geometry should stay fp32 —
    so only the conv/matmul run reduced, the result the pipeline sees is full fp32.

    Patches the inner Module's `forward` in place (instance attribute), leaving
    every other attribute pyannote touches — `.dimension`, `.metric`, `.to(...)` —
    untouched. Returns False (stay fp32) if no inner Module is found, so a backend
    we don't recognize degrades to the proven-clean baseline rather than erroring."""
    import torch

    inner = getattr(pipeline, "_embedding", None)
    module, where = _embedding_module(inner)
    if module is None:
        log(f"fp16=emb requested but found no embedding nn.Module (got {where}); leaving fp32")
        return False

    orig_forward = module.forward

    def amp_forward(*args, **kwargs):
        with torch.autocast("cuda", dtype=dtype):
            out = orig_forward(*args, **kwargs)
        if isinstance(out, torch.Tensor):
            return out.float()
        if isinstance(out, (tuple, list)):
            return type(out)(o.float() if isinstance(o, torch.Tensor) else o for o in out)
        return out

    module.forward = amp_forward
    log(f"embedding autocast {dtype} on {where} ({type(module).__name__}); output cast back to fp32")
    return True


class StageTimer:
    """A pyannote pipeline `hook`. pyannote calls it as
    hook(step_name, step_artifact, file=…, total=…, completed=…) at each stage
    (segmentation, embeddings, clustering, …) and repeatedly within a stage for
    progress. We keep the first timestamp of each stage and derive a stage's wall
    from when the NEXT stage began (the final stage runs until the pipeline
    returns). Best-effort: it never raises, so timing can't break diarization."""

    def __init__(self) -> None:
        self.first: dict[str, float] = {}
        self.order: list[str] = []

    def __call__(self, step_name, *args, **kwargs) -> None:
        try:
            if step_name not in self.first:
                self.first[step_name] = time.perf_counter()
                self.order.append(step_name)
        except Exception:
            pass

    def stage_ms(self, end: float) -> dict:
        out: dict[str, float] = {}
        for i, name in enumerate(self.order):
            stop = self.first[self.order[i + 1]] if i + 1 < len(self.order) else end
            out[name] = round((stop - self.first[name]) * 1000, 1)
        return out


def _fatal_cuda(exc: Exception) -> bool:
    """True for CUDA failures that poison the whole context (not just the
    request): every subsequent kernel in this process would also fail, so the
    only useful response is a clean process death + restart."""
    msg = str(exc).lower()
    return "cuda error" in msg and any(
        s in msg
        for s in (
            "illegal memory access",
            "unspecified launch failure",
            "context is destroyed",
            "uncorrectable ecc",
        )
    )


# ── backends ─────────────────────────────────────────────────────────────────


def fake_backend(worker_idx: int):
    """Protocol selftest: turns derived from the WAV header (one 1 s turn per
    second of audio, speakers cycling through num_speakers), no ML deps.
    num_speakers=7 forces an error response; num_speakers=9 kills the process
    (restart-path testing). Completion is staggered by id so concurrent
    requests deterministically finish OUT of submission order, which is the
    behavior the Go demultiplexer must handle."""
    import wave

    def diarize_one(wav: str, num_speakers: int = 0) -> dict:
        if num_speakers == 7:
            raise RuntimeError("boom")
        if num_speakers == 9:
            log("FAKE crash requested (num_speakers=9)")
            os._exit(3)
        with wave.open(wav, "rb") as w:
            dur = w.getnframes() / float(w.getframerate() or 16000)
        nspk = max(1, num_speakers)
        turns = [
            {"speaker": f"SPEAKER_{i % nspk:02d}", "start": float(i), "end": float(i) + 1.0}
            for i in range(int(dur))
        ]
        return {"turns": turns}

    def handle(rid, req: dict) -> dict:
        time.sleep(((rid or 0) % 3) * 0.05)  # force out-of-order completion
        return handle_request(req, diarize_one)

    return handle


def pyannote_backend(worker_idx: int, cfg: dict):
    """Build this worker's OWN pipeline replica (pyannote pipelines are not
    thread-safe) and return a request handler. Every replica is configured by
    the same helpers, so per-request output is identical across workers."""
    import torch
    from pyannote.audio import Pipeline

    pipeline = Pipeline.from_pretrained(cfg["pipeline_id"], token=cfg["token"])
    if pipeline is None:
        raise RuntimeError(
            f"Pipeline.from_pretrained returned None for {cfg['pipeline_id']} — for a gated "
            "pipeline id this means missing terms acceptance / HF_TOKEN; the ungated "
            "default needs neither"
        )
    want_device = cfg["want_device"]
    device = "cpu"
    if want_device in {"cuda", "auto"} and torch.cuda.is_available():
        device = "cuda"
    elif want_device == "cuda":
        # Fail fast instead of silently diarizing on CPU — a "GPU" run that
        # quietly runs CPU-side burns rented GPU-time at 0% utilization.
        raise RuntimeError("NOTO_PYANNOTE_DEVICE=cuda but torch reports no CUDA device")
    cfg["resolved_device"] = device
    pipeline.to(torch.device(device))
    applied = configure_batching(pipeline, cfg["seg_batch"], cfg["emb_batch"])
    is_cuda = device == "cuda"
    # Compile BEFORE the amp wrap so autocast surrounds the compiled callable.
    emb_compile = cfg["emb_compile"]
    emb_compiled = emb_compile not in {"", "0", "off"} and is_cuda and compile_embedding(pipeline, emb_compile)
    fp16_mode = cfg["fp16_mode"]
    amp_dtype = torch.float16 if fp16_mode.endswith("fp16") or fp16_mode in {"1", "true"} else torch.bfloat16
    use_amp = (fp16_mode.startswith("all") or fp16_mode in {"1", "true"}) and is_cuda
    emb_amp = fp16_mode.startswith("emb") and is_cuda and wrap_embedding_amp(pipeline, amp_dtype)
    seg_amp = cfg["seg_amp"]
    seg_dtype = torch.float16 if seg_amp == "fp16" else torch.bfloat16
    seg_wrapped = seg_amp in {"bf16", "fp16"} and is_cuda and wrap_segmentation_amp(pipeline, seg_dtype)
    # One CUDA stream per worker: kernels from different workers may overlap
    # when SM occupancy allows (the same co-residency overlap the STT∥diar
    # pairing exploits). Without it all workers share the default stream —
    # still ONE context, but strictly serialized kernels.
    stream = torch.cuda.Stream() if cfg["streams"] and is_cuda else None

    # Per-WORKER Silero VAD instance (it keeps LSTM state across chunks, so it
    # cannot be shared between concurrently-diarizing threads); runs on CPU —
    # the GPU is the saturated resource here, the CPU is idle.
    vad_cfg = cfg["vad"]
    vad = vad_trim.SileroVAD.load() if vad_cfg is not None else None
    if vad_cfg is not None and vad is None:
        log(f"worker {worker_idx}: NOTO_VAD set but Silero VAD failed to load; serving untrimmed")

    def amp_ctx():
        if use_amp:
            return torch.autocast("cuda", dtype=amp_dtype)
        return contextlib.nullcontext()

    def stream_ctx():
        if stream is not None:
            return torch.cuda.stream(stream)
        return contextlib.nullcontext()

    log(
        f"worker {worker_idx} ready on {device}; fp16={fp16_mode} (emb={emb_amp} seg={seg_wrapped}) "
        f"emb_compile={emb_compiled} batching applied={applied} "
        f"stream={'own' if stream is not None else 'default'} "
        f"vad={vad.backend if vad is not None else 'off'}"
    )

    timing_on = cfg["timing_on"]
    hook_ok = {"on": timing_on}

    def diarize_one(wav: str, num_speakers: int = 0) -> dict:
        kwargs = {}
        if num_speakers > 0:
            kwargs["num_speakers"] = num_speakers

        t0 = time.perf_counter()
        waveform, sample_rate = load_waveform(wav)
        t_load = time.perf_counter()

        # VAD trim (TODO-7): diarize only the speech regions; turn times are
        # remapped to the original timeline below, so the wire never sees the
        # condensed timeline. Any failure serves the proven untrimmed path.
        tmap = None
        kept_ratio = 1.0
        if vad is not None:
            n_samples = int(waveform.shape[-1])
            try:
                regions = vad.speech_regions(waveform.squeeze(0), sample_rate, vad_cfg["threshold"])
                p = vad_trim.plan(n_samples, sample_rate, regions, vad_cfg["pad"], vad_cfg["min_gap"])
                if p is not None:
                    slices, m = p
                    waveform = torch.cat([waveform[:, a:b] for a, b in slices], dim=1)
                    tmap = m
                    kept_ratio = waveform.shape[-1] / n_samples
            except Exception as exc:
                log(f"worker {worker_idx}: VAD trim failed ({type(exc).__name__}: {exc}); serving untrimmed")
                tmap = None
        t_vad = time.perf_counter()

        timer = StageTimer() if hook_ok["on"] else None
        file = {"waveform": waveform, "sample_rate": sample_rate}
        with stream_ctx(), amp_ctx():
            if timer is not None:
                try:
                    annotation = pipeline(file, hook=timer, **kwargs)
                except Exception as exc:  # hook unsupported — degrade, never fail the request
                    log(f"stage-timing hook unsupported ({type(exc).__name__}: {exc}); disabling")
                    hook_ok["on"] = False
                    timer = None
                    annotation = pipeline(file, **kwargs)
            else:
                annotation = pipeline(file, **kwargs)
        if stream is not None:
            stream.synchronize()
        t_infer = time.perf_counter()

        # pyannote 4 returns an object whose speaker_diarization attribute (or
        # the object itself in 3.x) is an Annotation supporting itertracks.
        ann = getattr(annotation, "speaker_diarization", annotation)
        if tmap is None:
            turns = [
                {"speaker": str(label), "start": float(seg.start), "end": float(seg.end)}
                for seg, _, label in ann.itertracks(yield_label=True)
            ]
        else:
            # Interval-clip, never endpoint-remap: a turn bridging a splice
            # must split into per-span pieces instead of claiming the dropped
            # silence (see TimeMap.intervals for the measured DER blowup).
            turns = [
                {"speaker": str(label), "start": a, "end": b}
                for seg, _, label in ann.itertracks(yield_label=True)
                for a, b in tmap.intervals(float(seg.start), float(seg.end))
            ]
        t_done = time.perf_counter()

        # Per-stage wall, so the perf work can see whether segmentation,
        # embedding, or the CPU clustering tail dominates the diar critical path.
        timing: dict = {
            "load_ms": round((t_load - t0) * 1000, 1),
            "infer_ms": round((t_infer - t_vad) * 1000, 1),
            "serialize_ms": round((t_done - t_infer) * 1000, 1),
        }
        if vad is not None:
            timing["vad_ms"] = round((t_vad - t_load) * 1000, 1)
            timing["vad_kept"] = round(kept_ratio, 3)
        if timer is not None:
            timing["stages_ms"] = timer.stage_ms(t_infer)
        if timing_on:
            stages = timing.get("stages_ms") or {}
            stage_str = " ".join(f"{k}={v}" for k, v in stages.items())
            log(
                f"timing worker={worker_idx} wav={os.path.basename(wav)} turns={len(turns)} "
                f"load={timing['load_ms']}ms infer={timing['infer_ms']}ms "
                f"serialize={timing['serialize_ms']}ms"
                + (f" vad={timing['vad_ms']}ms kept={timing['vad_kept']}" if vad is not None else "")
                + (f" [{stage_str}]" if stage_str else "")
            )
        return {"turns": turns, "timing": timing}

    def handle(rid, req: dict) -> dict:
        return handle_request(req, diarize_one)

    return handle


def handle_request(req: dict, diarize_one) -> dict:
    """Shape one request's response body (single wav or in-order wavs batch)."""
    if "wavs" in req:
        wavs = req["wavs"]
        nums = req.get("num_speakers") or [0] * len(wavs)
        if not isinstance(nums, list):
            nums = [int(nums or 0)] * len(wavs)
        return {"results": [diarize_one(wav, int(nums[i] or 0)) for i, wav in enumerate(wavs)]}
    return diarize_one(req["wav"], int(req.get("num_speakers") or 0))


def main() -> int:
    fake = os.getenv("NOTO_PYANNOTE_FAKE", "") == "1"
    workers_n = max(1, _int_env("NOTO_PYANNOTE_WORKERS", 4))
    cfg = {
        "pipeline_id": os.getenv("NOTO_PYANNOTE_PIPELINE", "pyannote-community/speaker-diarization-community-1"),
        "want_device": os.getenv("NOTO_PYANNOTE_DEVICE", "auto").lower(),
        "token": os.getenv("HF_TOKEN") or os.getenv("HUGGING_FACE_HUB_TOKEN") or None,
        "seg_batch": _int_env("NOTO_PYANNOTE_SEG_BATCH", 0),
        "emb_batch": _int_env("NOTO_PYANNOTE_EMB_BATCH", 0),
        "fp16_mode": os.getenv("NOTO_PYANNOTE_FP16", "emb-bf16").lower(),
        "emb_compile": os.getenv("NOTO_PYANNOTE_EMB_COMPILE", "").lower(),
        "seg_amp": os.getenv("NOTO_PYANNOTE_SEG_AMP", "").lower(),
        "timing_on": os.getenv("NOTO_PYANNOTE_TIMING", "1") != "0",
        "streams": os.getenv("NOTO_PYANNOTE_STREAMS", "1") != "0",
        "vad": vad_trim.env_cfg(),
    }

    import queue

    jobs: queue.Queue = queue.Queue()

    def build_backend(idx: int):
        if fake:
            return fake_backend(idx)
        return pyannote_backend(idx, cfg)

    def worker_loop(handle) -> None:
        while True:
            item = jobs.get()
            if item is None:
                return
            rid, req = item
            try:
                emit({"id": rid, **handle(rid, req)})
            except Exception as exc:
                emit({"id": rid, "error": str(exc)})
                if _fatal_cuda(exc):
                    # A poisoned CUDA context never recovers: serving on after an
                    # illegal memory access turns ONE bad kernel into a guaranteed
                    # failure for every later request (measured on L4: 24 straight
                    # errors, $0.14 of dead GPU time). Die instead — the Go client
                    # treats process exit as a transport failure and restarts a
                    # fresh process with a fresh context; callers retry once.
                    log(f"fatal CUDA error — exiting so the client restarts a clean context: {exc}")
                    os._exit(4)

    # Worker 0 loads FIRST and alone — it warms the HF cache (first-ever run
    # downloads the pipeline) and gates the ready handshake.
    if fake:
        log(f"FAKE backend (protocol selftest), {workers_n} workers")
    else:
        log(f"loading {cfg['pipeline_id']} ({workers_n} workers) …")
    try:
        handle0 = build_backend(0)
    except Exception as exc:
        emit({"ready": False, "error": f"pipeline load failed: {exc}"})
        return 1
    threading.Thread(target=worker_loop, args=(handle0,), daemon=True).start()

    def spawn_extras() -> None:
        # Replicas load STRICTLY ONE AT A TIME, each starting its serve loop as
        # it completes, while requests already flow to the ready subset. NEVER
        # load replicas concurrently — see the module docstring for the native
        # crash this prevents. The serial ramp costs ~10 s per replica on a warm
        # cache and nothing was ever measured for parallel loads to win.
        for i in range(1, workers_n):
            try:
                handle = build_backend(i)
            except Exception as exc:
                # A failed replica degrades capacity, never the server: worker 0
                # is proven up, so requests still complete (less overlap).
                log(f"worker {i} failed to load ({exc}); continuing with fewer workers")
                continue
            threading.Thread(target=worker_loop, args=(handle,), daemon=True).start()

    threading.Thread(target=spawn_extras, daemon=True).start()

    device = cfg.get("resolved_device", "cpu")
    emit({"ready": True, "device": device, "pipeline": cfg["pipeline_id"], "workers": workers_n})
    log(f"ready; dispatching to up to {workers_n} workers")

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            req = json.loads(line)
        except Exception as exc:
            emit({"id": None, "error": f"bad request line: {exc}"})
            continue
        jobs.put((req.get("id"), req))

    for _ in range(workers_n):
        jobs.put(None)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

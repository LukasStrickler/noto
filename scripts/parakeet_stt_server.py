#!/usr/bin/env python3
"""Warm Parakeet STT server — NeMo-backed, truly batched — spoken to over
stdin/stdout JSONL.

This is noto's single GPU STT server (the sherpa/ONNX-Runtime warm server it
replaced was retired after the consolidation pass; one architecture, one code
path). The Go engine (internal/platform/providers/stt/parakeet_server.go)
keeps owning chunking (≤120 s windows) and word stitching; this server loads
the model ONCE via NeMo and decodes a {"wavs": [...]} request as ONE padded
batch (encoder AND decoder batched).

WHY NeMo (BOTTLENECK.md, 17th pass): the sherpa/ONNX-Runtime stack topped out
at ~132× aggregate realtime on an L40S (one warm engine alone is ~121×; a
4-engine pool only reached 132×) and that ceiling was the serving
implementation, not the silicon — published batched inference for this exact
model (parakeet-tdt-0.6b, NeMo batched label-looping TDT greedy decode) is
~1000×+. Same weights, batched decode. Gated on the AMI WER/DER/cpWER A/B
(transcripts are not byte-identical to the sherpa CLI — different decoder
implementation, same weights; the corpus gate is the judge, not byte equality).

Wire details the Go side relies on:
  - tokens are ONE-TOKEN-PER-WORD (each with a leading space), with word-level
    timestamps/durations. sherpaResult.words() reconstructs words from "a token
    starting with a space begins a new word", so emitting whole words yields
    exactly NeMo's word offsets — no subword mimicry, no token-table coupling.

Protocol (one JSON object per line):
  ← {"ready": true, "provider": "cuda"}                          (startup)
  → {"id": 1, "wav": "/abs/chunk.wav"}
  ← {"id": 1, "text": "...", "tokens": [...], "timestamps": [...], "durations": [...]}
  → {"id": 2, "wavs": ["/abs/c0.wav", "/abs/c1.wav"]}
  ← {"id": 2, "results": [{...}, {...}]}                          (input order)
  ← {"id": 1, "error": "..."}                                     (request failure)

stdout carries ONLY protocol lines. NeMo/torch are chatty, so fd 1 is
re-pointed at stderr before any heavy import and the protocol writes to a
private dup of the original stdout — even C-level prints can't corrupt it.

Env:
  NOTO_PARAKEET_MODEL       HF repo or local .nemo path
                            (default nvidia/parakeet-tdt-0.6b-v3 — the SAME
                            weights the sherpa CLI export was made from)
  NOTO_PARAKEET_PRECISION   bf16 | fp32   (default bf16 = torch.autocast, fp32
                            weights + fp32 exponent range — the validated
                            pyannote-embedding technique, NOT a static fp16
                            weight cast. Measured WER-identical to fp32 on the
                            30-meeting AMI corpus TWICE at −5 GB VRAM, so the
                            headroom is free; CPU runs are unaffected, autocast
                            is CUDA-gated)
  NOTO_PARAKEET_BATCH       internal transcribe batch size (default 8; a 120 s
                            chunk ≈ 4× a 30 s leaderboard utterance, so 8 ≈
                            an effective batch of 32)
  NOTO_PARAKEET_PROVIDER    cuda | cpu (default cpu; set by the Go engine;
                            cuda FAILS FAST without a CUDA device)
  NOTO_PARAKEET_DECODE      greedy (default) | beam | maes | alsd | tsd — the
                            decode SEARCH. Default greedy is the validated path;
                            a beam strategy is the §10.5 same-model alternate
                            decode the B7 repair loop re-runs low-confidence spans
                            with. Best-effort: an unsupported strategy degrades to
                            greedy, never crashes.
  NOTO_PARAKEET_BEAM_SIZE   beam width when NOTO_PARAKEET_DECODE is a beam
                            strategy (default 4)
  NOTO_PARAKEET_PERTURB     test-time AUDIO augmentation re-decoded by the SAME
                            model — the repair lever when same-model decode
                            config is byte-identical. speed:0.9 / speed:1.1
                            (resample time-warp, timestamps rescaled back) or
                            noise:0.005 (additive gaussian). Empty = unperturbed.
  NOTO_PARAKEET_FAKE        1 = no-NeMo protocol selftest (deterministic fake
                            words from the WAV header; for $0 local testing)

VAD note: the diar server trims silence via vad_trim.py (NOTO_VAD); this
server deliberately does NOT. Its serve loop is single-threaded, so per-chunk
CPU VAD (~1.25 s per 120 s chunk on Silero JIT) would serialize INTO the
decode path — ~10 minutes across a 30-meeting corpus, more than the whole
baseline run wall. STT's share of GPU time is small; the right STT
integration is at the Go chunker (feed VAD spans instead of fixed 120 s
windows — TODO-7's chunker pairing), not inside the warm decode loop.
"""

from __future__ import annotations

import json
import os
import sys


def claim_protocol_stdout():
    """Reserve real stdout for protocol lines; everything else → stderr."""
    proto_fd = os.dup(1)
    os.dup2(2, 1)  # fd 1 (incl. C-level writes) now lands on stderr
    return os.fdopen(proto_fd, "w", buffering=1)


PROTO = claim_protocol_stdout()


def log(msg: str) -> None:
    print(f"[parakeet-server] {msg}", file=sys.stderr, flush=True)


def emit(obj: dict) -> None:
    PROTO.write(json.dumps(obj) + "\n")
    PROTO.flush()


def result_from_words(
    text: str,
    words: list[tuple[str, float, float]],
    confidences: list[float] | None = None,
) -> dict:
    """Map word-level output onto the wire shape. One token per word, each
    with a leading space, so the Go side's words() reconstruction returns
    exactly these words with these times. `confidences` (parallel to `words`,
    each a P(correct) in [0,1]) is emitted only when present — the Go side reads
    it into HypWord.Confidence and feeds B6 calibration; absent, calibration is
    skipped rather than scoring fabricated values."""
    out = {
        "text": text,
        "tokens": [" " + w for w, _, _ in words],
        "timestamps": [round(s, 3) for _, s, _ in words],
        "durations": [round(max(0.0, e - s), 3) for _, s, e in words],
    }
    if confidences:
        out["confidences"] = [round(float(c), 4) for c in confidences]
    return out


def fake_transcribe_fn():
    """Protocol selftest backend: one word per second of audio, no ML deps."""
    import wave

    def transcribe(paths: list[str]) -> list[dict]:
        out = []
        for p in paths:
            with wave.open(p, "rb") as w:
                dur = w.getnframes() / float(w.getframerate() or 16000)
            words = [(f"w{i}", float(i), float(i) + 0.5) for i in range(int(dur))]
            out.append(result_from_words(" ".join(w for w, _, _ in words), words))
        return out

    return transcribe


def enable_word_confidence(model) -> bool:
    """Best-effort: reconfigure the model's decoding to preserve per-WORD
    confidence (§B2.6). Returns True on success. Guarded end-to-end because the
    confidence API and config shape drift across NeMo versions — a failure here
    must degrade to "no confidence emitted" (calibration skipped), never crash
    the validated decode path. Confidence is a normalized entropy measure in
    [0,1] aggregated per word with min() (a word is only as sure as its weakest
    token), so larger = more confident, matching the calibration P(correct)
    contract. Only ever called when NOTO_PARAKEET_CONFIDENCE=1, so the default
    (validated WER/throughput) path is byte-identical to before."""
    try:
        from omegaconf import open_dict
        from nemo.collections.asr.parts.utils.asr_confidence_utils import (
            ConfidenceConfig,
            ConfidenceMethodConfig,
        )

        # Preserve only WORD confidence — the single signal we read
        # (hyp.word_confidence). Per-FRAME preservation keeps a value for every
        # encoder frame of the whole meeting; on long audio that array is the
        # memory/compute hog with no payoff here (we never read it), so it is off.
        # Token confidence is kept as the input the per-word min() aggregation needs.
        conf_cfg = ConfidenceConfig(
            preserve_frame_confidence=False,
            preserve_token_confidence=True,
            preserve_word_confidence=True,
            aggregation="min",
            method_cfg=ConfidenceMethodConfig(name="entropy", entropy_type="tsallis", entropy_norm="exp"),
        )
        decoding_cfg = model.cfg.decoding
        with open_dict(decoding_cfg):
            decoding_cfg.confidence_cfg = conf_cfg
        model.change_decoding_strategy(decoding_cfg)
        log("word confidence enabled (entropy/tsallis, per-word min aggregation)")
        return True
    except Exception as exc:  # version drift, missing API, config rejection
        log(f"word confidence unavailable, emitting none: {exc}")
        return False


def enable_beam_decode(model, beam_size: int, strategy: str) -> bool:
    """Best-effort: switch TDT/RNNT decoding from greedy to a beam search so a
    RE-DECODE explores hypotheses the greedy pass committed away from — the §10.5
    same-model alternate decode the B7 repair loop re-runs low-confidence spans
    with. Guarded exactly like enable_word_confidence: any failure (TDT-beam
    unsupported in this NeMo build, config drift, rejected field) degrades to the
    validated greedy path and never crashes. Word timestamps are requested under
    beam where the version supports them; if they're dropped, words_of() emits
    text-only and the Go side falls back — the run still completes. Only ever
    called when NOTO_PARAKEET_DECODE selects a beam strategy, so the default
    (validated greedy) decode is byte-identical to before."""
    try:
        from omegaconf import open_dict

        decoding_cfg = model.cfg.decoding
        with open_dict(decoding_cfg):
            decoding_cfg.strategy = strategy
            if "beam" in decoding_cfg:
                decoding_cfg.beam.beam_size = beam_size
        # Word timestamps come from transcribe(timestamps=True) (the `opt` dict),
        # NOT a decoding-cfg field — setting an unknown `compute_timestamps` here
        # gets the structured config REJECTED, silently reverting the whole beam
        # switch to greedy (the first beam run decoded byte-identically to greedy
        # for exactly this reason). Touch only known fields (strategy, beam_size).
        model.change_decoding_strategy(decoding_cfg)
        log(f"beam decode enabled (strategy={strategy}, beam_size={beam_size})")
        return True
    except Exception as exc:  # unsupported strategy, config drift, rejection
        log(f"beam decode unavailable, staying greedy: {exc}")
        return False


def nemo_transcribe_fn(provider: str, precision: str, batch: int, model_name: str):
    import contextlib
    import inspect

    import torch

    # .nemo resolution: snapshot the HF repo into HF_HOME (the Modal model
    # volume) and restore from the local file — deterministic caching, and the
    # download is a one-time volume cost, not a per-run cost.
    local = model_name
    if not os.path.isfile(local):
        from huggingface_hub import snapshot_download

        log(f"resolving {model_name} via HF cache (HF_HOME={os.getenv('HF_HOME', '~')}) …")
        snap = snapshot_download(model_name)
        nemos = [os.path.join(snap, f) for f in os.listdir(snap) if f.endswith(".nemo")]
        if not nemos:
            raise RuntimeError(f"no .nemo file in snapshot of {model_name}")
        local = nemos[0]

    import nemo.collections.asr as nemo_asr

    if provider == "cuda" and not torch.cuda.is_available():
        # Fail fast instead of silently decoding on CPU: a "GPU" benchmark run
        # that quietly runs CPU-side burns rented GPU-time at 0% utilization
        # (and a broken CUDA env also poisons NeMo's class resolution with a
        # misleading "abstract class ASRModel" error).
        raise RuntimeError("provider=cuda but torch reports no CUDA device")
    device = "cuda" if provider == "cuda" else "cpu"
    log(f"restoring {os.path.basename(local)} on {device} (precision={precision}, batch={batch}) …")
    model = nemo_asr.models.ASRModel.restore_from(restore_path=local, map_location=device)
    model = model.to(device).eval()

    use_bf16 = precision == "bf16" and device == "cuda"
    if use_bf16:
        log("bf16 autocast enabled (fp32 weights, fp32 exponent range)")

    # Word confidence is opt-in (NOTO_PARAKEET_CONFIDENCE=1) so the validated
    # default decode path is unchanged; it powers B6 calibration when requested.
    # We snapshot the PLAIN decoding cfg first so that if the confidence decode
    # later raises — NeMo's TDT word-confidence aggregation fails on some inputs
    # ("Something went wrong with word-level confidence aggregation") — transcribe()
    # can revert to it and continue WITHOUT confidence, honoring the §B2.6 contract
    # that confidence never crashes the validated decode path. `conf` is a mutable
    # cell so that revert is visible to words_of() and later requests.
    import copy as _copy

    base_decoding_cfg = _copy.deepcopy(model.cfg.decoding)
    conf = {"on": os.getenv("NOTO_PARAKEET_CONFIDENCE", "") == "1" and enable_word_confidence(model)}

    # Alternate beam decode is opt-in (NOTO_PARAKEET_DECODE=beam|maes|alsd|tsd) so
    # the validated greedy default is untouched. Applied AFTER confidence on the
    # same decoding cfg, so the two compose when both are requested. `alt` is a
    # mutable cell so a beam decode-time failure can revert to base greedy and stay
    # reverted for the rest of the process, exactly like the confidence path.
    decode_mode = os.getenv("NOTO_PARAKEET_DECODE", "").strip().lower()
    alt = {"beam": False}
    if decode_mode in ("beam", "maes", "alsd", "tsd"):
        beam_size = max(1, int(os.getenv("NOTO_PARAKEET_BEAM_SIZE", "4") or 4))
        strat = "maes" if decode_mode == "beam" else decode_mode
        alt["beam"] = enable_beam_decode(model, beam_size, strat)

    # Audio perturbation is opt-in (NOTO_PARAKEET_PERTURB=speed:0.9 | speed:1.1 |
    # noise:0.005) — a TEST-TIME augmentation that re-decodes the SAME model on ALTERED
    # audio, the repair lever for when a same-model decode config is byte-identical
    # (greedy TDT is deterministic on identical input) and a different model is weaker.
    # `speed` warps the time axis by resampling (timestamps are rescaled back to the
    # original timeline); `noise` adds gaussian noise without warping time. The default
    # (empty) path is the validated unperturbed decode, byte-identical to before.
    perturb_spec = os.getenv("NOTO_PARAKEET_PERTURB", "").strip().lower()
    if perturb_spec:
        log(f"audio perturbation enabled: {perturb_spec}")

    # Pass optional kwargs only when this NeMo version's transcribe() has them,
    # so a signature drift degrades to defaults instead of crashing the server.
    sig = inspect.signature(model.transcribe).parameters
    opt_candidates: dict = {"timestamps": True, "return_hypotheses": True, "verbose": False}
    # Canary is a multitask AED model (a genuinely different/stronger ASR than parakeet
    # TDT — the B7 repair source for when same-family decodes are byte-identical). Its
    # transcribe() takes language + punctuation prompts parakeet's does not; these keys
    # are ABSENT from the parakeet signature, so the filter below leaves the validated
    # TDT path byte-identical and passes them only to a canary model, which needs them to
    # do plain (English, punctuated) ASR. Word timestamps come from the same
    # timestamps=True path (use canary-1b-flash, which supports them).
    is_canary = "canary" in model_name.lower()
    if is_canary:
        lang = os.getenv("NOTO_CANARY_LANG", "en")
        # pnc='no' → no punctuation/capitalization, so canary's output matches the plain
        # lowercase form parakeet and the AMI reference use (punctuated output would score
        # every comma/period as an error and pollute a repair splice). Overridable.
        pnc = os.getenv("NOTO_CANARY_PNC", "no")
        opt_candidates.update({"source_lang": lang, "target_lang": lang, "pnc": pnc})
    opt = {k: v for k, v in opt_candidates.items() if k in sig}

    # Canary is a 40 s-context model (max_duration=40); a full meeting fed whole to
    # transcribe() is truncated to a fraction (measured: 1178 of 4531 words, WER 0.83).
    # So canary decodes in fixed windows and stitches with per-window timestamp offsets.
    canary_window_sec = float(os.getenv("NOTO_CANARY_WINDOW_SEC", "30") or 30)

    # Encoder frame duration for offset→seconds fallback (8× subsampled 10 ms).
    try:
        frame_sec = float(model.cfg.preprocessor.window_stride) * float(model.cfg.encoder.subsampling_factor)
    except Exception:
        frame_sec = 0.08

    def words_of(hyp, time_scale: float = 1.0, time_offset: float = 0.0) -> dict:
        if isinstance(hyp, list):  # n-best shape → best hypothesis
            hyp = hyp[0] if hyp else ""
        if isinstance(hyp, str):  # no timestamp support → let Go fall back
            return {"text": hyp, "tokens": [], "timestamps": [], "durations": []}
        text = getattr(hyp, "text", "") or ""
        stamps = getattr(hyp, "timestamp", None) or {}
        # Per-word confidence, parallel to the raw word-timestamp list, when the
        # decoding was reconfigured to preserve it. Stays index-aligned to the
        # words below by reading from the SAME enumerate index before filtering.
        word_conf = list(getattr(hyp, "word_confidence", None) or []) if conf["on"] else []
        words: list[tuple[str, float, float]] = []
        confidences: list[float] = []
        for i, w in enumerate(stamps.get("word") or []):
            word = (w.get("word") or "").strip()
            if not word:
                continue
            start = w.get("start")
            end = w.get("end")
            if start is None:
                start = float(w.get("start_offset", 0)) * frame_sec
            if end is None:
                end = float(w.get("end_offset", 0)) * frame_sec
            # Map perturbed-timeline stamps back to the original audio (speed warp);
            # time_scale is 1.0 for unperturbed / non-time-warping perturbations.
            words.append((word, float(start) * time_scale + time_offset, float(end) * time_scale + time_offset))
            # Prefer a per-word confidence carried on the timestamp dict; else the
            # parallel hyp.word_confidence list; else this word drops out of the
            # confidence stream (so a partial list never misaligns the rest).
            c = w.get("confidence")
            if c is None and i < len(word_conf):
                c = word_conf[i]
            if c is not None and len(confidences) == len(words) - 1:
                confidences.append(float(c))
        # Only emit confidences when we have one for EVERY word — a partial list
        # can't be index-matched on the Go side, so drop it entirely.
        confs = confidences if len(confidences) == len(words) and words else None
        return result_from_words(text, words, confs)

    def mono_path(path: str, scratch: list[str]) -> str:
        """NeMo's loader does NOT downmix multi-channel audio (sherpa's read_wave
        does), so a stereo source WAV crashes the encoder with a shape error
        (measured: AMI ES2010d is stereo). Header-check every input (cheap) and
        rewrite multi-channel files to a mono temp WAV the dataloader accepts."""
        import soundfile as sf

        try:
            info = sf.info(path)
        except Exception:
            return path  # let NeMo's own loader raise the real error
        if info.channels <= 1:
            return path
        import tempfile

        data, sr = sf.read(path, dtype="float32", always_2d=True)
        tmp = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
        sf.write(tmp.name, data.mean(axis=1), sr)
        scratch.append(tmp.name)
        log(f"downmixed {info.channels}-channel input {os.path.basename(path)} → mono")
        return tmp.name

    def perturb_audio(path: str, scratch: list[str]) -> tuple[str, float]:
        """Apply NOTO_PARAKEET_PERTURB to one wav before decode. Returns the (possibly
        new) path and the factor word timestamps must be multiplied by to map back to
        the ORIGINAL timeline (1.0 when time isn't warped). Best-effort: any failure
        returns the original audio unperturbed — perturbation never breaks a decode."""
        if not perturb_spec:
            return path, 1.0
        try:
            import numpy as np
            import soundfile as sf

            kind, _, val = perturb_spec.partition(":")
            data, sr = sf.read(path, dtype="float32")
            if data.ndim > 1:
                data = data.mean(axis=1)
            scale = 1.0
            if kind == "speed":
                rate = float(val or "1.0")
                if rate <= 0 or rate == 1.0:
                    return path, 1.0
                # Resample to len/rate samples: at the same sr the content plays `rate`×
                # faster (rate>1) or slower (rate<1). A perturbed-time stamp × rate maps
                # back to the original timeline.
                n = max(1, int(round(len(data) / rate)))
                data = np.interp(np.linspace(0.0, len(data) - 1, n), np.arange(len(data)), data).astype("float32")
                scale = rate
            elif kind == "noise":
                amp_db = float(val or "0.005")
                data = (data + np.random.normal(0.0, amp_db, len(data)).astype("float32"))
            else:
                return path, 1.0
            import tempfile

            tmp = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
            sf.write(tmp.name, data, sr)
            scratch.append(tmp.name)
            return tmp.name, scale
        except Exception as exc:
            log(f"perturb failed ({exc}); using original audio")
            return path, 1.0

    def preproc_audio(path: str, scratch: list[str]) -> str:
        """Apply NOTO_PARAKEET_PREPROC (e.g. "highpass:80,dc") before decode — pure-numpy
        audio cleanup (scripts/audio_preproc.py) to cut sub-speech rumble/DC. No new deps
        (numpy only), so it can't conflict with the pyannote/nemo image. Timeline-preserving
        (same length/sr) so word timestamps are unaffected. Best-effort: any failure returns
        the original audio. Empty/unset = passthrough."""
        spec = os.getenv("NOTO_PARAKEET_PREPROC", "").strip().lower()
        if not spec:
            return path
        try:
            import audio_preproc
            import soundfile as sf

            data, sr = sf.read(path, dtype="float32")
            if data.ndim > 1:
                data = data.mean(axis=1)
            out = audio_preproc.preprocess(data, sr, spec)
            import tempfile

            tmp = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
            sf.write(tmp.name, out, sr)
            scratch.append(tmp.name)
            return tmp.name
        except Exception as exc:  # noqa: BLE001
            log(f"preproc failed ({exc}); using original audio")
            return path

    def transcribe(paths: list[str]) -> list[dict]:
        scratch: list[str] = []
        amp = torch.autocast("cuda", dtype=torch.bfloat16) if use_bf16 else contextlib.nullcontext()
        mono = [mono_path(p, scratch) for p in paths]
        preprocd = [preproc_audio(p, scratch) for p in mono]
        perturbed = [perturb_audio(p, scratch) for p in preprocd]
        wavs = [w for w, _ in perturbed]
        # All inputs share one NOTO_PARAKEET_PERTURB → one time-scale for the batch.
        time_scale = perturbed[0][1] if perturbed else 1.0

        def _decode_paths(decode_wavs):
            with torch.inference_mode(), amp:
                return model.transcribe(audio=decode_wavs, batch_size=min(batch, len(decode_wavs)), **opt)

        def _decode():
            return _decode_paths(wavs)

        def _canary_long(wav, decode, scratch_list, scale, window_sec):
            import soundfile as sf
            import tempfile

            data, sr = sf.read(wav, dtype="float32")
            if getattr(data, "ndim", 1) > 1:
                data = data.mean(axis=1)
            win = int(window_sec * sr)
            if win <= 0 or len(data) <= win:
                h = decode([wav])
                if isinstance(h, tuple):
                    h = h[0]
                return words_of(h[0] if h else "", scale)
            win_paths: list[str] = []
            offsets: list[float] = []
            for s in range(0, len(data), win):
                seg = data[s:s + win]
                if len(seg) < int(0.2 * sr):  # skip a sub-200 ms tail
                    continue
                tmp = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
                sf.write(tmp.name, seg, sr)
                scratch_list.append(tmp.name)
                win_paths.append(tmp.name)
                offsets.append(s / sr)
            hyps = decode(win_paths)
            if isinstance(hyps, tuple):
                hyps = hyps[0]
            merged: dict = {"text": "", "tokens": [], "timestamps": [], "durations": []}
            confs: list[float] = []
            have_conf = True
            for off, h in zip(offsets, hyps):
                wd = words_of(h, scale, off)
                merged["text"] = (merged["text"] + " " + wd["text"]).strip()
                merged["tokens"] += wd["tokens"]
                merged["timestamps"] += wd["timestamps"]
                merged["durations"] += wd["durations"]
                if "confidences" in wd:
                    confs += wd["confidences"]
                else:
                    have_conf = False
            if have_conf and confs:
                merged["confidences"] = confs
            return merged

        # Canary long-form: window each meeting into ≤canary_window_sec chunks, decode
        # them, and stitch each meeting's words back with per-window timestamp offsets.
        if is_canary:
            try:
                return [_canary_long(w, _decode_paths, scratch, time_scale, canary_window_sec) for w in wavs]
            finally:
                for p in scratch:
                    try:
                        os.unlink(p)
                    except OSError:
                        pass

        try:
            try:
                hyps = _decode()
            except Exception as exc:
                # Confidence and beam decode are both best-effort: NeMo's TDT
                # word-confidence aggregation raises on some inputs, and a beam
                # strategy may be unsupported/unstable in this build. Either way,
                # revert to the validated plain greedy decode and retry rather than
                # erroring the request (§B2.6). One-way for the rest of this process
                # — a later request never re-hits the same fault.
                if not conf["on"] and not alt["beam"]:
                    raise
                log(f"alternate decode failed ({exc}); reverting to plain greedy decode")
                try:
                    model.change_decoding_strategy(base_decoding_cfg)
                except Exception:
                    pass
                conf["on"] = False
                alt["beam"] = False
                hyps = _decode()
        finally:
            for p in scratch:
                try:
                    os.unlink(p)
                except OSError:
                    pass
        if isinstance(hyps, tuple):  # legacy RNNT shape: (best, all_n_best)
            hyps = hyps[0]
        if len(hyps) != len(paths):
            raise RuntimeError(f"transcribe returned {len(hyps)} results for {len(paths)} inputs")
        return [words_of(h, time_scale) for h in hyps]

    return transcribe


def main() -> int:
    provider = os.getenv("NOTO_PARAKEET_PROVIDER", "cpu").lower()
    precision = os.getenv("NOTO_PARAKEET_PRECISION", "bf16").lower()
    batch = max(1, int(os.getenv("NOTO_PARAKEET_BATCH", "8") or 8))
    model_name = os.getenv("NOTO_PARAKEET_MODEL", "nvidia/parakeet-tdt-0.6b-v3")

    try:
        if os.getenv("NOTO_PARAKEET_FAKE", "") == "1":
            log("FAKE backend (protocol selftest)")
            transcribe = fake_transcribe_fn()
        else:
            transcribe = nemo_transcribe_fn(provider, precision, batch, model_name)
    except Exception as exc:
        emit({"ready": False, "error": f"model load failed: {exc}"})
        return 1

    log(f"ready on {provider}")
    emit({"ready": True, "provider": provider})

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        rid = None
        try:
            req = json.loads(line)
            rid = req.get("id")
            if "wavs" in req:
                emit({"id": rid, "results": transcribe(list(req["wavs"]))})
            else:
                emit({"id": rid, **transcribe([req["wav"]])[0]})
        except Exception as exc:
            emit({"id": rid, "error": str(exc)})
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

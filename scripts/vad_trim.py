"""VAD-gating (TODO-7): silence trimming shared by the STT and diar servers —
pay GPU time for speech, not silence.

Both warm servers (parakeet_stt_server.py, pyannote_diar_server.py) sit in
front of models whose cost is linear in audio-seconds. A cheap CPU VAD
(Silero) finds the speech regions; the server condenses the waveform to just
those regions (with padding, and short gaps kept whole), runs the model on the
condensed audio, and remaps every output timestamp back to the ORIGINAL
timeline before answering. The wire protocol and the Go side never see any of
this — timestamps on the wire are always original-timeline.

Safety posture (this changes what the models see, so it is DER/WER-gated on
the pinned AMI corpus like every quality-touching knob):
  - speech regions are PADDED (NOTO_VAD_PAD per side, default 0.4 s) so word
    onsets/offsets and local segmentation context survive the cut;
  - only silence gaps ≥ NOTO_VAD_MIN_GAP (default 1.0 s) are collapsed, and a
    collapsed gap still keeps 2×pad of real audio (natural room tone, not
    zeros) at the splice — pyannote's segmentation sees actual non-speech
    there, so spliced-adjacent speakers still get separated;
  - every failure degrades to the untrimmed baseline: VAD import/model/load
    errors, unsupported sample rates, zero speech found (a Silero false
    negative must never silently empty a transcript), or a trim that would
    save <0.5 s (not worth the copy).

The timeline math (keep_spans / sample_slices / TimeMap / plan) is pure
stdlib — unit-tested for $0 by scripts/vad_trim_test.py; only SileroVAD needs
torch + the silero-vad package (lazy imports, container-only).

Env (read via env_cfg; the diar server is the consumer — see the STT server's
docstring for why the warm decode loop deliberately does NOT trim):
  NOTO_VAD          "" / "0" / "off" = disabled; anything else = Silero trim
  NOTO_VAD_PAD      seconds of audio kept on each side of a speech region
                    (default 0.5)
  NOTO_VAD_MIN_GAP  only silence gaps at least this long are collapsed
                    (default 3.0; shorter pauses are kept in full — measured
                    ladder on the 4-meeting AMI arm: min_gap 1.0 = +1.1 DER,
                    2.0 = +0.5, because AMI reference segments span same-
                    speaker pauses that untrimmed pyannote also bridges; 3.0
                    keeps those while still collapsing real dead air, which
                    in production runs 30-70% of the recording)
  NOTO_VAD_THRESHOLD
                    Silero speech probability threshold (default 0.35 — more
                    speech-sensitive than Silero's own 0.5: a false negative
                    here deletes audio the diarizer never gets to see, so the
                    trim errs toward keeping; the min-gap policy above already
                    keeps the cut points far from speech)
"""

from __future__ import annotations

import os


def keep_spans(
    speech: list[tuple[float, float]], total: float, pad: float, min_gap: float
) -> list[tuple[float, float]]:
    """Padded, merged keep-list from raw VAD speech regions.

    Pads each region by `pad` per side (clipped to [0, total]), then merges
    any two padded regions whose gap is < min_gap — so the silence between
    them is KEPT, and only gaps ≥ min_gap are ever dropped."""
    spans = sorted((max(0.0, s - pad), min(total, e + pad)) for s, e in speech if e > s)
    if not spans:
        return []
    out = [[spans[0][0], spans[0][1]]]
    for s, e in spans[1:]:
        if s - out[-1][1] < min_gap:
            out[-1][1] = max(out[-1][1], e)
        else:
            out.append([s, e])
    return [(s, e) for s, e in out]


def sample_slices(spans: list[tuple[float, float]], sr: int, n_samples: int) -> list[tuple[int, int]]:
    """Quantize second-domain spans to non-overlapping [a, b) sample ranges."""
    out: list[tuple[int, int]] = []
    prev_end = 0
    for s, e in spans:
        a = max(prev_end, min(n_samples, int(round(s * sr))))
        b = max(a, min(n_samples, int(round(e * sr))))
        if b > a:
            out.append((a, b))
            prev_end = b
    return out


class TimeMap:
    """Maps condensed-timeline seconds back to original-timeline seconds.

    Built from the kept spans (original-timeline seconds). Within a span the
    map is the identity shifted by the span's offset; a timestamp landing
    exactly on a splice resolves to the later span's start (starts of turns
    matter more than ends there, and the ambiguity is one sample wide)."""

    def __init__(self, spans: list[tuple[float, float]]) -> None:
        self.spans: list[tuple[float, float, float]] = []  # (cond_start, orig_start, dur)
        t = 0.0
        for s, e in spans:
            self.spans.append((t, s, e - s))
            t += e - s
        self.kept = t

    def to_orig(self, t: float) -> float:
        if not self.spans:
            return t
        lo, hi = 0, len(self.spans) - 1
        while lo < hi:  # last span with cond_start <= t
            mid = (lo + hi + 1) // 2
            if self.spans[mid][0] <= t:
                lo = mid
            else:
                hi = mid - 1
        c, o, d = self.spans[lo]
        return o + min(max(t - c, 0.0), d)

    def intervals(self, s: float, e: float) -> list[tuple[float, float]]:
        """Map a condensed-time INTERVAL to the original-time intervals it
        covers — clipped to the kept spans, so the dropped gaps between them
        are excluded.

        This, not endpoint remapping, is how a diarization turn must travel
        back to the original timeline: a turn that bridges a splice (pyannote
        happily emits one turn across the ~2×pad of room tone at a joint) has
        its start in one span and its end in another, and to_orig on the two
        endpoints would claim the ENTIRE dropped silence as speech — measured
        on the 4-meeting smoke as DER 13.9 → 26.5 (ES2005a 66.4) from false
        alarm alone, while cpWER sat still at 32.4 (no words live in the
        dropped gaps, so attribution never noticed). Clipping splits such a
        turn into per-span pieces; audio the model never saw is never claimed."""
        out: list[tuple[float, float]] = []
        for c, o, d in self.spans:
            a = max(s, c)
            b = min(e, c + d)
            if b > a:
                out.append((o + (a - c), o + (b - c)))
        return out


def plan(n_samples: int, sr: int, regions: list[tuple[float, float]], pad: float, min_gap: float):
    """→ (sample_slices, TimeMap) or None when trimming should be skipped.

    None means: process the ORIGINAL audio (no speech found — conservative
    against a VAD false negative — or the trim would save <0.5 s)."""
    if sr <= 0 or n_samples <= 0:
        return None
    total = n_samples / float(sr)
    spans = keep_spans(regions, total, pad, min_gap)
    if not spans:
        return None
    slices = sample_slices(spans, sr, n_samples)
    kept = sum(b - a for a, b in slices)
    if kept <= 0 or kept >= n_samples - int(0.5 * sr):
        return None
    return slices, TimeMap([(a / float(sr), b / float(sr)) for a, b in slices])


def env_cfg() -> dict | None:
    """Read the NOTO_VAD knobs; None = trimming disabled."""
    mode = os.getenv("NOTO_VAD", "").lower()
    if mode in {"", "0", "off"}:
        return None

    def _f(name: str, default: float) -> float:
        try:
            return float(os.getenv(name, "") or default)
        except ValueError:
            return default

    return {
        "pad": _f("NOTO_VAD_PAD", 0.5),
        "min_gap": _f("NOTO_VAD_MIN_GAP", 3.0),
        "threshold": _f("NOTO_VAD_THRESHOLD", 0.35),
    }


class SileroVAD:
    """Silero VAD wrapper. ONE instance per thread: the model keeps LSTM state
    across chunks, so it is not shareable across concurrent requests."""

    def __init__(self, model, get_speech_timestamps, backend: str) -> None:
        self._model = model
        self._gst = get_speech_timestamps
        self.backend = backend

    @classmethod
    def load(cls) -> "SileroVAD | None":
        """None on any failure — the caller logs and serves untrimmed."""
        try:
            from silero_vad import get_speech_timestamps, load_silero_vad
        except Exception:
            return None
        # ONNX backend is ~2-3× the JIT speed on CPU and onnxruntime ships as
        # a silero-vad dependency; fall back to JIT if the ONNX path breaks.
        for onnx in (True, False):
            try:
                return cls(load_silero_vad(onnx=onnx), get_speech_timestamps, "onnx" if onnx else "jit")
            except Exception:
                continue
        return None

    def speech_regions(self, samples, sr: int, threshold: float = 0.35) -> list[tuple[float, float]]:
        """Speech regions (seconds) for a 1-D float waveform tensor. Raises on
        failure; the caller catches and serves untrimmed."""
        wav, vsr = samples, sr
        if vsr not in (8000, 16000):  # Silero only speaks 8/16 kHz
            import torchaudio.functional as AF

            wav = AF.resample(wav, vsr, 16000)
            vsr = 16000
        ts = self._gst(wav, self._model, threshold=threshold, sampling_rate=vsr, return_seconds=False)
        return [(t["start"] / float(vsr), t["end"] / float(vsr)) for t in ts]

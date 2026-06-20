"""Cheap, dependency-free audio preprocessing for the STT server (cut low-freq
noise before decode).

The "smart preprocessing / cut out noise" step that does NOT need scipy/nara_wpe
(adding those to the heavy nemo+pyannote+torch bench image breaks pyannote — see
GPU_REDESIGN_PROGRESS.md). Everything here is pure numpy, so it rides the image's
existing numpy and can't conflict. Lazy-imported by parakeet_stt_server.py only
when NOTO_PARAKEET_PREPROC is set; the timeline is unchanged (same length, same
sample rate) so word timestamps map straight through.

Steps (comma-separated in the spec, applied in order):
  highpass[:HZ]   FFT high-pass with a smooth raised-cosine rolloff (default 80 Hz)
                  — removes sub-speech rumble / handling noise / mains hum tails;
                  speech (fundamental ~85 Hz+) is preserved.
  dc              remove the DC offset (a recording artifact), mean-subtract.

e.g. NOTO_PARAKEET_PREPROC="highpass:80,dc"

Safety: like VAD, this changes what the model sees, so it is WER-gated before any
production default. A high-pass at 80 Hz is provably speech-preserving (the unit
test asserts a 200 Hz tone passes and a 40 Hz tone is killed), so it is safe to
offer off-by-default; the gate is for tuning, not correctness.
"""
from __future__ import annotations


def highpass(data, sr, cutoff_hz: float = 80.0):
    """FFT high-pass with a raised-cosine transition from cutoff/2 to cutoff (0
    below, 1 above). Pure numpy; returns same length/dtype."""
    import numpy as np

    x = np.asarray(data, dtype="float64")
    n = x.shape[-1]
    if n < 4 or cutoff_hz <= 0:
        return data
    X = np.fft.rfft(x)
    freqs = np.fft.rfftfreq(n, 1.0 / sr)
    lo, hi = cutoff_hz * 0.5, cutoff_hz
    gain = np.ones_like(freqs)
    gain[freqs < lo] = 0.0
    band = (freqs >= lo) & (freqs < hi)
    gain[band] = 0.5 * (1.0 - np.cos(np.pi * (freqs[band] - lo) / (hi - lo)))
    out = np.fft.irfft(X * gain, n)
    return out.astype(getattr(data, "dtype", "float32"))


def remove_dc(data):
    import numpy as np

    x = np.asarray(data)
    return (x - x.mean()).astype(getattr(data, "dtype", "float32"))


def preprocess(data, sr, spec: str):
    """Apply a comma-separated preprocessing spec. Unknown steps are skipped."""
    out = data
    for step in (spec or "").split(","):
        name, _, arg = step.strip().partition(":")
        if name == "highpass":
            out = highpass(out, sr, float(arg) if arg else 80.0)
        elif name == "dc":
            out = remove_dc(out)
    return out

"""Unit tests for audio_preproc — pure numpy, $0 (no GPU/model). Run:
  python3 scripts/audio_preproc_test.py
"""
import os
import sys

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import audio_preproc as ap  # noqa: E402

SR = 16000


def _tone(hz, n=SR, amp=1.0):
    t = np.arange(n) / SR
    return (amp * np.sin(2 * np.pi * hz * t)).astype("float32")


def _rms(x):
    return float(np.sqrt(np.mean(np.square(x))))


def test_highpass_kills_subspeech_keeps_speech():
    # 40 Hz (below speech) must be strongly attenuated; 200 Hz / 1 kHz preserved.
    lo = ap.highpass(_tone(40), SR, 80.0)
    assert _rms(lo) < 0.1, f"40 Hz not attenuated: rms={_rms(lo)}"
    for hz in (200, 1000, 4000):
        sp = _tone(hz)
        out = ap.highpass(sp, SR, 80.0)
        ratio = _rms(out) / _rms(sp)
        assert ratio > 0.9, f"{hz} Hz speech tone attenuated: ratio={ratio:.3f}"


def test_highpass_preserves_length_and_finite():
    x = _tone(300)
    out = ap.highpass(x, SR, 80.0)
    assert len(out) == len(x)
    assert np.all(np.isfinite(out))


def test_remove_dc():
    x = _tone(300) + 0.5  # add a DC offset
    out = ap.remove_dc(x)
    assert abs(float(np.mean(out))) < 1e-4, f"DC not removed: mean={np.mean(out)}"


def test_preprocess_chain_and_unknown_skipped():
    x = _tone(300) + 0.3
    out = ap.preprocess(x, SR, "highpass:80,dc,bogus")
    assert len(out) == len(x)
    assert abs(float(np.mean(out))) < 1e-3  # dc removed
    # speech tone still mostly present
    assert _rms(out) / _rms(_tone(300)) > 0.85


if __name__ == "__main__":
    fns = [v for k, v in sorted(globals().items()) if k.startswith("test_") and callable(v)]
    for fn in fns:
        fn()
        print(f"ok  {fn.__name__}")
    print(f"\nPASS — {len(fns)} tests")

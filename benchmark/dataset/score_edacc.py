#!/usr/bin/env python3
"""Score parakeet WER on EdAcc from a Modal STT pass (.modal-edacc.json), overall
and sliced BY ACCENT — the de-risk check for the repair direction.

If parakeet's EdAcc WER is high (esp. on non-native accents), there's headroom for
an alternate ASR source to repair → the B7 gate is worth running. If it's near
LibriSpeech-clean levels, EdAcc is not parakeet-weak and the direction is a
dead-end (like AMI). Normalization matches the Go harness's metrics.Normalize
(lowercase, keep letters/digits, collapse whitespace).

Usage: python3 benchmark/dataset/score_edacc.py [.modal-edacc.json]
"""
import json
import re
import sys
from collections import defaultdict


def norm(s: str) -> list[str]:
    return re.sub(r"[^a-z0-9 ]+", " ", s.lower()).split()


def wer(ref: list[str], hyp: list[str]) -> tuple[int, int]:
    n, m = len(ref), len(hyp)
    if n == 0:
        return (m, 0)
    prev = list(range(m + 1))
    for i in range(1, n + 1):
        cur = [i] + [0] * m
        ri = ref[i - 1]
        for j in range(1, m + 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (0 if ri == hyp[j - 1] else 1))
        prev = cur
    return (prev[m], n)


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else ".modal-edacc.json"
    data = json.load(open(path))
    results = data.get("results", [])
    by_accent = defaultdict(lambda: [0, 0])  # accent -> [err, refwords]
    tot_err = tot_ref = 0
    for r in results:
        if r.get("error"):
            continue
        ref, hyp = norm(r.get("ref", "")), norm(r.get("hyp", ""))
        err, rlen = wer(ref, hyp)
        tot_err += err
        tot_ref += rlen
        a = r.get("accent") or "(unknown)"
        by_accent[a][0] += err
        by_accent[a][1] += rlen

    print(f"{'accent':28} {'WER':>7} {'ref_w':>7}")
    for a, (e, r) in sorted(by_accent.items(), key=lambda kv: -(kv[1][0] / kv[1][1] if kv[1][1] else 0)):
        rate = e / r if r else 0.0
        print(f"  {a:26} {rate:7.3f} {r:7d}")
    overall = tot_err / tot_ref if tot_ref else 0.0
    print("\n" + "=" * 50)
    print(f"parakeet EdAcc WER (micro-avg): {overall:.4f}  ({100*overall:.1f}%)  over {tot_ref} ref words")
    print(f"  vs LibriSpeech-clean (~2%) and AMI Mix-Headset (~20.7%)")
    print(f"  → parakeet-WEAK if this is high (esp. non-native accents); repair has headroom")


if __name__ == "__main__":
    main()

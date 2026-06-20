#!/usr/bin/env python3
"""Score the overlap-separation experiment (.modal-overlap-sep.json): does running
a 2-speaker separation model recover BOTH speakers in overlap better than the
current single mixed transcript?

Per region the reference has 2 speakers (refA, refB). We compute a region cpWER
(min over speaker assignment of edit-distance / ref words) for two systems:
  baseline  — the single MIXED transcript (the 2nd speaker is all-deletions)
  separated — the two SEPARATED transcripts (best of the 2 assignments)
Lower = better. If separated < baseline, separation recovered the 2nd speaker.

Usage: python3 benchmark/dataset/score_overlap_sep.py [.modal-overlap-sep.json]
"""
import json
import re
import sys
from itertools import permutations


def norm(s):
    return re.sub(r"[^a-z0-9 ]+", " ", s.lower()).split()


def edit(a, b):
    n, m = len(a), len(b)
    if n == 0:
        return m
    prev = list(range(m + 1))
    for i in range(1, n + 1):
        cur = [i] + [0] * m
        ai = a[i - 1]
        for j in range(1, m + 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (0 if ai == b[j - 1] else 1))
        prev = cur
    return prev[m]


def region_cpwer(refs, hyps):
    """min over assignment of sum(edit)/sum(ref words). refs/hyps are lists of token lists."""
    n = max(len(refs), len(hyps))
    R = refs + [[]] * (n - len(refs))
    H = hyps + [[]] * (n - len(hyps))
    total = sum(len(r) for r in refs) or 1
    best = None
    for perm in permutations(range(n)):
        e = sum(edit(R[i], H[perm[i]]) for i in range(n))
        best = e if best is None or e < best else best
    return (best or 0) / total


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else ".modal-overlap-sep.json"
    data = json.load(open(path))
    agg = {"base": [0.0, 0], "sep": [0.0, 0]}  # weighted err, ref words
    n_regions = n_better = 0
    for _meeting, regions in data.items():
        for reg in regions:
            refs = [norm(" ".join(ws)) for ws in reg["speakers"].values()]
            refs = [r for r in refs if r]
            if len(refs) < 2:
                continue
            rw = sum(len(r) for r in refs)
            base = region_cpwer(refs, [norm(reg.get("mixed_hyp", ""))])
            sep_hyps = [norm(h) for h in reg.get("sep_hyps", [])]
            sep = region_cpwer(refs, sep_hyps) if sep_hyps else base
            agg["base"][0] += base * rw
            agg["base"][1] += rw
            agg["sep"][0] += sep * rw
            agg["sep"][1] += rw
            n_regions += 1
            if sep < base - 1e-9:
                n_better += 1
    b = agg["base"][0] / agg["base"][1] if agg["base"][1] else 0
    s = agg["sep"][0] / agg["sep"][1] if agg["sep"][1] else 0
    print(f"regions scored: {n_regions}  ({n_better} where separation beat the mix)")
    print(f"  baseline (single mixed transcript) cpWER : {b:.4f}  ({100*b:.1f}%)")
    print(f"  separated (2 streams)              cpWER : {s:.4f}  ({100*s:.1f}%)")
    print(f"  recovery (baseline - separated)          : {(b-s)*100:+.1f} pts  "
          f"({'REAL — separation recovers the 2nd speaker' if s < b else 'none — separation did not help here'})")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Offline cpWER attribution experiment — iterate the word->speaker assignment with
ZERO GPU (reuses an existing run's hyps: STT words + diar turns + refs).

cpWER ("who said what") = 27% vs WER 21% on the anchor; that 6-point gap is
ATTRIBUTION error, which is post-processing — so we can tune it offline and re-score
in seconds, no Modal run. Each hyp word is (re)assigned a speaker from the diar
TURNS, the per-speaker word streams are concatenated, and cpWER is the lowest-WER
speaker permutation (Hungarian-equivalent; brute force for <=6 speakers).

Variants:
  baseline   - the speaker already on each hyp word (the production merge)
  midpoint   - assign by the diar turn covering the word midpoint
  span       - assign by the speaker active for the MAJORITY of the word span
  span+gap   - span, but a word in a diar gap snaps to the nearest turn
  +smooth    - span+gap, then flip lone single-word speaker islands to their neighbours

Usage: python3 benchmark/dataset/attribution_experiment.py <run_hyps_dir>
"""
import json
import os
import re
import sys
from itertools import permutations

HERE = os.path.dirname(os.path.abspath(__file__))
WORDS = os.path.join(HERE, "words")


def norm(s):
    return re.sub(r"[^a-z0-9]+", "", s.lower())


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


def cpwer(ref_by_spk, hyp_by_spk):
    """min over ref<->hyp speaker assignment of sum(edit)/sum(ref words).

    Precompute the n×n edit-cost matrix ONCE (the expensive part), then brute-force
    the permutation summing matrix entries — so edits aren't recomputed per perm.
    """
    refs = sorted(ref_by_spk)
    hyps = sorted(hyp_by_spk)
    total = sum(len(v) for v in ref_by_spk.values())
    if total == 0:
        return 0.0
    n = max(len(refs), len(hyps))
    R = refs + [None] * (n - len(refs))
    H = hyps + [None] * (n - len(hyps))
    cost = [
        [edit(ref_by_spk.get(r, []) if r else [], hyp_by_spk.get(h, []) if h else []) for h in H]
        for r in R
    ]
    best = None
    for perm in permutations(range(n)):
        e = 0
        for i in range(n):
            e += cost[i][perm[i]]
        if best is None or e < best:
            best = e
    return (best or 0) / total


def group(words, speaker_key):
    out = {}
    for w in words:
        s = speaker_key(w) or "?"
        t = norm(w.get("text", ""))
        if t:
            out.setdefault(s, []).append(t)
    return out


def turn_at_midpoint(turns, w):
    t = (w.get("start", 0.0) + w.get("end", 0.0)) / 2
    for tr in turns:
        if tr["start"] <= t < tr["end"]:
            return tr["speaker"]
    return None


def turn_by_span(turns, w):
    s, e = w.get("start", 0.0), w.get("end", 0.0)
    if e <= s:
        return turn_at_midpoint(turns, w)
    cover = {}
    for tr in turns:
        a, b = max(s, tr["start"]), min(e, tr["end"])
        if b > a:
            cover[tr["speaker"]] = cover.get(tr["speaker"], 0.0) + (b - a)
    return max(cover, key=cover.get) if cover else None


def nearest_turn(turns, w):
    t = (w.get("start", 0.0) + w.get("end", 0.0)) / 2
    best, bd = None, 1e18
    for tr in turns:
        d = 0 if tr["start"] <= t < tr["end"] else min(abs(t - tr["start"]), abs(t - tr["end"]))
        if d < bd:
            bd, best = d, tr["speaker"]
    return best


def smooth(labels):
    out = labels[:]
    for i in range(1, len(out) - 1):
        if out[i - 1] == out[i + 1] and out[i] != out[i - 1] and out[i - 1] is not None:
            out[i] = out[i - 1]
    return out


def main():
    hyps_dir = sys.argv[1]
    variants = ["baseline", "midpoint", "span", "span+gap", "+smooth"]
    agg = {v: [0.0, 0] for v in variants}  # name -> [weighted err sum, ref words]
    for hf in sorted(os.listdir(hyps_dir)):
        if not hf.endswith(".json"):
            continue
        mid = hf[:-5]
        rf = os.path.join(WORDS, f"{mid}.words.json")
        if not os.path.exists(rf):
            continue
        ref = json.load(open(rf))
        hyp = json.load(open(os.path.join(hyps_dir, hf)))
        words, turns = hyp.get("words", []), hyp.get("turns", [])
        # Small-sample window for fast/cheap iteration (SLICE_SEC=0 = whole meeting).
        sec = float(os.getenv("SLICE_SEC", "600"))
        if sec > 0:
            ref = [w for w in ref if w.get("start", 0.0) < sec]
            words = [w for w in words if w.get("start", 0.0) < sec]
            turns = [t for t in turns if t.get("start", 0.0) < sec]
        if not words or not turns:
            continue
        ref_by = group(ref, lambda w: w.get("speaker", "?"))
        nref = sum(len(v) for v in ref_by.values())
        # brute-force cpWER permutes speakers; guard the factorial on distinct count
        nspk = max(len(ref_by), len({t["speaker"] for t in turns}))
        if nspk > 7:
            continue

        def score(by):
            return cpwer(ref_by, by)

        results = {}
        results["baseline"] = score(group(words, lambda w: w.get("speaker", "?")))
        results["midpoint"] = score(group(words, lambda w: turn_at_midpoint(turns, w)))
        results["span"] = score(group(words, lambda w: turn_by_span(turns, w)))
        spans = [turn_by_span(turns, w) or nearest_turn(turns, w) for w in words]
        results["span+gap"] = score(_regroup(words, spans))
        results["+smooth"] = score(_regroup(words, smooth(spans)))

        for v in variants:
            agg[v][0] += results[v] * nref
            agg[v][1] += nref

    print(f"{'variant':10} {'cpWER':>8}")
    base = agg["baseline"][0] / agg["baseline"][1] if agg["baseline"][1] else 0
    for v in variants:
        e, r = agg[v]
        rate = e / r if r else 0
        delta = (rate - base) * 100
        print(f"{v:10} {rate:8.4f}   {'(baseline)' if v == 'baseline' else f'{delta:+.2f} pts'}")


def _regroup(words, labels):
    out = {}
    for w, s in zip(words, labels):
        s = s or "?"
        t = norm(w.get("text", ""))
        if t:
            out.setdefault(s, []).append(t)
    return out


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Partition AMI WER into single-speaker vs overlap regions (no GPU — reuses an
existing anchor run's hyps).

The Mix-Headset anchor mixes all speakers onto one channel, so its ~20.7% WER is
inflated by OVERLAP. The product captures each speaker on their own channel, so the
relevant number is the WER where exactly ONE speaker is active ("your mic, you
alone speaking"). This splits the existing parakeet transcript by the reference's
single-speaker vs overlap time regions and scores each — same model, same audio,
just partitioned by what the product would see per channel.

A word (ref or hyp) is assigned to a region by how many REFERENCE speakers are
active at its midpoint: 1 -> single-speaker, >=2 -> overlap. Overlap structure is a
precomputed event-sweep timeline (per-speaker merged intervals) so classification
is O(words log words). WER is time-ordered word-level Levenshtein within each region;
normalization matches metrics.Normalize.

Usage: python3 benchmark/dataset/single_speaker_wer.py <run_hyps_dir>
"""
import bisect
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
WORDS = os.path.join(HERE, "words")


def norm_tok(t: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", t.lower())


def wer(ref: list[str], hyp: list[str]) -> tuple[int, int]:
    n, m = len(ref), len(hyp)
    if n == 0:
        return (m, 0)
    prev = list(range(m + 1))
    for i in range(1, n + 1):
        cur = [i] + [0] * m
        ri = ref[i - 1]
        for j in range(1, m + 1):
            cost = 0 if ri == hyp[j - 1] else 1
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost)
        prev = cur
    return (prev[m], n)


def overlap_timeline(ref):
    """(times, counts): counts[i] = # distinct speakers active in [times[i], times[i+1])."""
    by_spk = {}
    for w in ref:
        by_spk.setdefault(w.get("speaker", "?"), []).append((w.get("start", 0.0), w.get("end", 0.0)))
    events = []
    for ivs in by_spk.values():
        ivs.sort()
        merged = []
        for s, e in ivs:
            if merged and s <= merged[-1][1]:
                merged[-1][1] = max(merged[-1][1], e)
            else:
                merged.append([s, e])
        for s, e in merged:
            events.append((s, 1))
            events.append((e, -1))
    events.sort()
    times, counts, cur, last = [], [], 0, None
    for t, d in events:
        if last is not None and t != last:
            times.append(last)
            counts.append(cur)
        cur += d
        last = t
    if last is not None:
        times.append(last)
        counts.append(cur)
    return times, counts


def region_of(times, counts, w):
    if not times:
        return "single"
    t = (w.get("start", 0.0) + w.get("end", 0.0)) / 2
    i = bisect.bisect_right(times, t) - 1
    return "overlap" if (0 <= i < len(counts) and counts[i] >= 2) else "single"


def main():
    if len(sys.argv) < 2:
        print("usage: single_speaker_wer.py <run_hyps_dir>")
        return
    hyps_dir = sys.argv[1]
    agg = {"single": [0, 0], "overlap": [0, 0]}
    print(f"{'meeting':10} {'single WER':>11} {'(ref_w)':>8}   {'overlap WER':>12} {'(ref_w)':>8}")
    for hf in sorted(os.listdir(hyps_dir)):
        if not hf.endswith(".json"):
            continue
        mid = hf[:-5]
        rf = os.path.join(WORDS, f"{mid}.words.json")
        if not os.path.exists(rf):
            continue
        ref = json.load(open(rf))
        hyp = json.load(open(os.path.join(hyps_dir, hf))).get("words", [])
        times, counts = overlap_timeline(ref)

        cells = {"single": ([], []), "overlap": ([], [])}  # region -> (ref_toks, hyp_toks)
        for w in ref:
            tok = norm_tok(w.get("text", ""))
            if tok:
                cells[region_of(times, counts, w)][0].append(tok)
        for w in hyp:
            tok = norm_tok(w.get("text", ""))
            if tok:
                cells[region_of(times, counts, w)][1].append(tok)

        line = [mid]
        for rgn in ("single", "overlap"):
            rt, ht = cells[rgn]
            err, rlen = wer(rt, ht)
            agg[rgn][0] += err
            agg[rgn][1] += rlen
            line.append(f"{(err/rlen if rlen else 0):.3f}")
            line.append(f"{rlen}")
        print(f"{line[0]:10} {line[1]:>11} {line[2]:>8}   {line[3]:>12} {line[4]:>8}")

    print("\n" + "=" * 60)
    for rgn in ("single", "overlap"):
        e, r = agg[rgn]
        rate = e / r if r else 0.0
        print(f"  {rgn:8} WER (micro-avg): {rate:.4f} ({100 * rate:.1f}%)  over {r} ref words")
    oe, orf = agg["single"][0] + agg["overlap"][0], agg["single"][1] + agg["overlap"][1]
    print(f"  overall  WER (both regions): {oe / orf:.4f}  (sanity vs ~0.207 anchor)")


if __name__ == "__main__":
    main()

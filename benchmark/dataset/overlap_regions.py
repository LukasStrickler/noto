#!/usr/bin/env python3
"""Extract OVERLAP regions + per-speaker reference words from AMI — the ground
truth for evaluating speaker SEPARATION (the "2 people talk → transcribe both
individually" goal).

The pipeline today emits one mixed transcript in overlap and pins it to one
speaker (merge.go picks the single best-overlap turn), so the 2nd speaker is
lost — ~60% overlap WER. A separation stage (H10) must recover BOTH speakers'
words. To score that, we need to know what each speaker ACTUALLY said in each
overlap region: this builds exactly that from the reference word timeline.

A region is a maximal time span where >=2 reference speakers are active (merged
from the per-speaker merged-interval event sweep, same as single_speaker_wer.py).
Each region carries the ref words per speaker whose midpoint falls inside it.

Output: overlap_regions/<meeting>.json
  [{ "start": s, "end": e, "speakers": { "<spk>": ["word", ...], ... } }, ...]

Usage: python3 benchmark/dataset/overlap_regions.py [meeting ...]   (default: all)
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
WORDS = os.path.join(HERE, "words")
OUT = os.path.join(HERE, "overlap_regions")
MIN_REGION_SEC = 0.3  # ignore micro-overlaps shorter than this (not separable)


def norm_tok(t: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", t.lower())


def overlap_intervals(ref):
    """Merged time intervals where >=2 distinct speakers are active."""
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
    regions, cur, start = [], 0, None
    for t, d in events:
        was = cur
        cur += d
        if was < 2 <= cur:
            start = t
        elif was >= 2 > cur and start is not None:
            if t - start >= MIN_REGION_SEC:
                regions.append((start, t))
            start = None
    return regions


def words_in(ref, s, e):
    """ref words (per speaker) whose midpoint falls in [s, e)."""
    out = {}
    for w in ref:
        mid = (w.get("start", 0.0) + w.get("end", 0.0)) / 2
        if s <= mid < e:
            tok = norm_tok(w.get("text", ""))
            if tok:
                out.setdefault(w.get("speaker", "?"), []).append(tok)
    return out


def main():
    os.makedirs(OUT, exist_ok=True)
    meetings = sys.argv[1:] or [f[:-len(".words.json")] for f in sorted(os.listdir(WORDS))
                                if f.endswith(".words.json")]
    tot_regions = tot_sec = tot_words = 0
    print(f"{'meeting':10} {'regions':>8} {'ovl_sec':>9} {'2nd_spk_words':>14}")
    for mid in meetings:
        rf = os.path.join(WORDS, f"{mid}.words.json")
        if not os.path.exists(rf):
            continue
        ref = json.load(open(rf))
        regions = []
        sec = words = 0.0
        second = 0
        for s, e in overlap_intervals(ref):
            spk = words_in(ref, s, e)
            spk = {k: v for k, v in spk.items() if v}
            if len(spk) < 2:  # need >=2 speakers WITH words to be a separation target
                continue
            regions.append({"start": round(s, 3), "end": round(e, 3), "speakers": spk})
            sec += e - s
            wc = sum(len(v) for v in spk.values())
            words += wc
            # words beyond the single most-talkative speaker = what's lost today
            second += wc - max(len(v) for v in spk.values())
        json.dump(regions, open(os.path.join(OUT, f"{mid}.json"), "w"))
        print(f"{mid:10} {len(regions):8d} {sec:9.1f} {second:14d}")
        tot_regions += len(regions)
        tot_sec += sec
        tot_words += second
    print("\n" + "=" * 50)
    print(f"  {tot_regions} overlap regions, {tot_sec/60:.1f} min of overlap")
    print(f"  {tot_words} ref words spoken by the NON-dominant speaker(s) in overlap")
    print(f"  = the words the current single-stream pipeline cannot recover; the separation prize")


if __name__ == "__main__":
    main()

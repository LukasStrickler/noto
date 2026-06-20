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
active at its midpoint: 1 -> single-speaker, >=2 -> overlap. WER is time-ordered
word-level Levenshtein within each region. Normalization matches metrics.Normalize.

Usage: python3 benchmark/dataset/single_speaker_wer.py <run_hyps_dir>
"""
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
        for j in range(1, m + 1):
            cost = 0 if ref[i - 1] == hyp[j - 1] else 1
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost)
        prev = cur
    return (prev[m], n)


def active_count(intervals_by_spk: dict, t: float) -> int:
    """How many distinct speakers are active at time t (their word covers t)."""
    return sum(1 for ivs in intervals_by_spk.values() if any(s <= t < e for s, e in ivs))


def main():
    if len(sys.argv) < 2:
        print("usage: single_speaker_wer.py <run_hyps_dir>")
        return
    hyps_dir = sys.argv[1]
    agg = {"single": [0, 0], "overlap": [0, 0]}  # region -> [errors, ref_words]
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

        # reference speaker activity intervals (ground-truth overlap structure)
        ivs = {}
        for w in ref:
            ivs.setdefault(w.get("speaker", "?"), []).append((w.get("start", 0.0), w.get("end", 0.0)))

        def region(w):
            t = (w.get("start", 0.0) + w.get("end", 0.0)) / 2
            return "overlap" if active_count(ivs, t) >= 2 else "single"

        for w in ref + hyp:
            w["_region"] = region(w)

        for rgn in ("single", "overlap"):
            rwords = [norm_tok(w["text"]) for w in ref if w["_region"] == rgn and norm_tok(w["text"])]
            hwords = [norm_tok(w["text"]) for w in hyp if w["_region"] == rgn and norm_tok(w["text"])]
            err, rlen = wer(rwords, hwords)
            agg[rgn][0] += err
            agg[rgn][1] += rlen
        s_err, s_ref = wer(
            [norm_tok(w["text"]) for w in ref if w["_region"] == "single" and norm_tok(w["text"])],
            [norm_tok(w["text"]) for w in hyp if w["_region"] == "single" and norm_tok(w["text"])],
        )
        o_err, o_ref = wer(
            [norm_tok(w["text"]) for w in ref if w["_region"] == "overlap" and norm_tok(w["text"])],
            [norm_tok(w["text"]) for w in hyp if w["_region"] == "overlap" and norm_tok(w["text"])],
        )
        sw = s_err / s_ref if s_ref else 0.0
        ow = o_err / o_ref if o_ref else 0.0
        print(f"{mid:10} {sw:11.3f} {s_ref:8d}   {ow:12.3f} {o_ref:8d}")

    print("\n" + "=" * 60)
    for rgn in ("single", "overlap"):
        e, r = agg[rgn]
        rate = e / r if r else 0.0
        print(f"  {rgn:8} WER (micro-avg): {rate:.4f} ({100*rate:.1f}%)  over {r} ref words")
    overall_e = agg["single"][0] + agg["overlap"][0]
    overall_r = agg["single"][1] + agg["overlap"][1]
    print(f"  overall  WER (both regions): {overall_e/overall_r:.4f}  (sanity vs ~0.207 anchor)")


if __name__ == "__main__":
    main()

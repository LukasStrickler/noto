#!/usr/bin/env python3
"""Score AMI-IHM single-speaker WER from a Modal STT pass (.modal-ihm.json).

Each headset is one speaker, but the Headset-N -> speaker mapping isn't given, so
each headset transcript is scored against EVERY speaker reference and assigned its
lowest-WER match (oracle assignment — each headset genuinely is one speaker, so the
min is that speaker). The headline is the micro-averaged WER over all assigned
headsets, directly comparable to the Mix-Headset anchor (~20.7%).

Normalization matches the Go harness's metrics.Normalize: lowercase, keep only
letters/digits, collapse whitespace — so model casing/punctuation don't pollute it.

Usage: python3 benchmark/dataset/score_ihm.py [.modal-ihm.json]
"""
import json
import os
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
IHM = os.path.join(HERE, "ami_ihm")


def norm(s: str) -> list[str]:
    s = s.lower()
    s = re.sub(r"[^a-z0-9 ]+", " ", s)
    return s.split()


def wer(ref: list[str], hyp: list[str]) -> tuple[int, int]:
    """(edit distance, ref length) — word-level Levenshtein."""
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


def load_refs(meeting: str) -> dict[str, list[str]]:
    mdir = os.path.join(IHM, meeting)
    refs = {}
    for f in os.listdir(mdir) if os.path.isdir(mdir) else []:
        if f.startswith("ref_") and f.endswith(".txt"):
            spk = f[len("ref_"):-len(".txt")]
            refs[spk] = norm(open(os.path.join(mdir, f)).read())
    return refs


def main():
    path = sys.argv[1] if len(sys.argv) > 1 else ".modal-ihm.json"
    data = json.load(open(path))
    tot_err = tot_ref = 0
    print(f"{'meeting/headset':22} {'->spk':6} {'ref_w':>6} {'WER':>7}")
    for meeting, heads in data.items():
        refs = load_refs(meeting)
        if not refs:
            print(f"  {meeting}: no refs, skipping")
            continue
        for i, text in enumerate(heads):
            hyp = norm(text)
            # assign to the best-matching (lowest-WER) speaker reference
            best = min(
                ((spk, *wer(ref, hyp)) for spk, ref in refs.items()),
                key=lambda t: (t[1] / t[2] if t[2] else 9.9),
            )
            spk, err, rlen = best
            rate = err / rlen if rlen else 0.0
            tot_err += err
            tot_ref += rlen
            print(f"  {meeting}.Headset-{i:<10} {spk:6} {rlen:6d} {rate:7.3f}")
    overall = tot_err / tot_ref if tot_ref else 0.0
    print("\n" + "=" * 48)
    print(f"AMI-IHM single-speaker WER (micro-avg): {overall:.4f}  ({100*overall:.1f}%)")
    print(f"  over {tot_ref} reference words")
    print(f"  vs AMI Mix-Headset anchor (overlapping mix): 0.207 (20.7%)")


if __name__ == "__main__":
    main()

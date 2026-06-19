#!/usr/bin/env python3
"""Fetch AMI word-level reference transcripts and write them as
benchmark/dataset/words/<id>.words.json — the reference half of the AMI benchmark.

Runs LOCALLY / off-GPU and FAST: it downloads AMI's manual annotations ONCE (one
~22 MB zip) and parses every meeting's words locally in seconds. The model
benchmark streams hypotheses back from the rented GPU and scores them against
these references here, so references never ride on GPU time (see memory note
"benchmark-cost-split"). Audio (the only thing the GPU needs) is fetched
separately by ../identity/fetch.py.

Source: AMI manual annotations, NXT format. `words/<meeting>.<X>.words.xml` holds
one <w starttime= endtime=>WORD</w> per token, where <X> (A/B/C/D...) is the
speaker. We keep real word-level timing and drop punctuation-only tokens. Speaker
labels are the agent letters — fine for cpWER, which is permutation-invariant.

Usage:
  python3 fetch_ami_words.py                 # all meetings with audio in ../identity/ami
  python3 fetch_ami_words.py ES2002a ES2002b # explicit subset
  python3 fetch_ami_words.py --force         # rewrite even if words.json exists
"""
import argparse
import glob
import json
import os
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
AMI_AUDIO = os.path.join(HERE, "..", "identity", "ami")
OUT = os.path.join(HERE, "words")
ZIP_URL = "https://groups.inf.ed.ac.uk/ami/AMICorpusAnnotations/ami_public_manual_1.6.2.zip"
ZIP_CACHE = os.path.join(HERE, ".cache", "ami_public_manual_1.6.2.zip")


def ensure_zip():
    if os.path.exists(ZIP_CACHE) and os.path.getsize(ZIP_CACHE) > 1 << 20:
        return ZIP_CACHE
    os.makedirs(os.path.dirname(ZIP_CACHE), exist_ok=True)
    print(f"downloading AMI annotations ({ZIP_URL}) ...")
    req = urllib.request.Request(ZIP_URL, headers={"User-Agent": "noto-ami-fetch"})
    with urllib.request.urlopen(req, timeout=180) as r, open(ZIP_CACHE + ".part", "wb") as f:
        f.write(r.read())
    os.replace(ZIP_CACHE + ".part", ZIP_CACHE)
    print(f"  cached -> {ZIP_CACHE} ({os.path.getsize(ZIP_CACHE)/1e6:.1f} MB)")
    return ZIP_CACHE


def parse_words(zf, member, speaker):
    """Extract spoken <w> tokens (text + timing) from one words.xml member."""
    words = []
    root = ET.fromstring(zf.read(member))
    for w in root.iter("w"):
        if w.get("punc") == "true":  # punctuation token — drop (scorer strips it anyway)
            continue
        text = (w.text or "").strip()
        if not text:
            continue
        st, en = w.get("starttime"), w.get("endtime")
        if st is None or en is None:
            continue
        try:
            start, end = float(st), float(en)
        except ValueError:
            continue
        words.append({"speaker": speaker, "start": round(start, 3), "end": round(end, 3), "text": text})
    return words


def meeting_words(zf, members_by_meeting, meeting):
    words = []
    for speaker, member in members_by_meeting.get(meeting, []):
        words.extend(parse_words(zf, member, speaker))
    words.sort(key=lambda w: w["start"])
    return words


def index_members(zf):
    """Map meeting_id -> [(speaker_letter, zip_member)] from words/<m>.<X>.words.xml."""
    by_meeting = {}
    for name in zf.namelist():
        base = os.path.basename(name)
        if not base.endswith(".words.xml"):
            continue
        stem = base[: -len(".words.xml")]  # e.g. ES2002a.A
        if "." not in stem:
            continue
        meeting, speaker = stem.rsplit(".", 1)
        by_meeting.setdefault(meeting, []).append((speaker, name))
    return by_meeting


def meetings_with_audio():
    wavs = sorted(glob.glob(os.path.join(AMI_AUDIO, "*.wav")))
    return [os.path.splitext(os.path.basename(w))[0] for w in wavs]


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("meetings", nargs="*", help="meeting IDs (default: all wavs in ../identity/ami)")
    ap.add_argument("--force", action="store_true", help="rewrite even if words.json exists")
    args = ap.parse_args()
    os.makedirs(OUT, exist_ok=True)

    ids = args.meetings or meetings_with_audio()
    if not ids:
        raise SystemExit(f"no meetings given and no wavs in {AMI_AUDIO}")

    with zipfile.ZipFile(ensure_zip()) as zf:
        by_meeting = index_members(zf)
        print(f"writing AMI words for {len(ids)} meeting(s) -> {OUT}")
        done = skipped = missing = 0
        for mid in ids:
            dst = os.path.join(OUT, f"{mid}.words.json")
            if os.path.exists(dst) and not args.force:
                skipped += 1
                print(f"  {mid}: present, skip")
                continue
            words = meeting_words(zf, by_meeting, mid)
            if not words:
                missing += 1
                print(f"  {mid}: NOT FOUND in annotations")
                continue
            with open(dst, "w") as f:
                json.dump(words, f)
            done += 1
            spk = sorted({w["speaker"] for w in words})
            print(f"  {mid}: {len(words)} words, {len(spk)} speakers {spk}, {words[-1]['end']/60:.1f} min")
        print(f"done: {done} written, {skipped} skipped, {missing} missing")


if __name__ == "__main__":
    main()

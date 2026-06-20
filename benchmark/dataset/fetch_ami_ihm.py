#!/usr/bin/env python3
"""Fetch AMI **IHM** (individual headset) audio + build single-speaker references.

The anchor benchmark uses AMI **Mix-Headset** — every participant's close-talk mic
mixed onto ONE channel, so the recognizer faces heavy speaker overlap (that's why
its WER reads ~20%). The product, by contrast, captures each speaker on their OWN
channel (your mic = one single-speaker stream). AMI-IHM is the exact analog: each
`Headset-N.wav` is one participant, close-talk, single-speaker — so it isolates how
well the model does WITHOUT the overlap problem.

This script, per meeting:
  - downloads `{meeting}.Headset-0..3.wav` from the AMI Edinburgh mirror,
  - splits the meeting's word reference (benchmark/dataset/words/<m>.words.json)
    by speaker into one single-speaker reference per participant.

We do NOT hard-map Headset-N -> speaker here: AMI's headset index isn't a reliable
global-speaker key. Scoring assigns each headset to its best-matching (lowest-WER)
speaker reference — each headset IS one speaker, so the min is that speaker (the
standard oracle assignment for IHM scoring).

Usage:
  python3 benchmark/dataset/fetch_ami_ihm.py                 # ES2002 a/b/c/d
  python3 benchmark/dataset/fetch_ami_ihm.py ES2002a         # one meeting
"""
import json
import os
import sys
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
WORDS = os.path.join(HERE, "words")
OUT = os.path.join(HERE, "ami_ihm")
AUDIO = "https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/{m}/audio/{m}.Headset-{n}.wav"
MAX_HEADSETS = 4  # AMI ES/IS/TS meetings have up to 4 headset channels (0..3)
DEFAULT = ["ES2002a", "ES2002b", "ES2002c", "ES2002d"]


def fetch(url, dest):
    if os.path.exists(dest) and os.path.getsize(dest) > 1024:
        return True
    req = urllib.request.Request(url, headers={"User-Agent": "noto-ihm-fetch"})
    try:
        with urllib.request.urlopen(req, timeout=180) as r, open(dest + ".part", "wb") as f:
            while True:
                chunk = r.read(1 << 20)
                if not chunk:
                    break
                f.write(chunk)
        os.replace(dest + ".part", dest)
        return True
    except Exception as e:  # noqa: BLE001 - best-effort fetch, report and skip
        print(f"  ! {url}: {e}")
        if os.path.exists(dest + ".part"):
            os.remove(dest + ".part")
        return False


def split_refs(meeting, mdir):
    """Write one single-speaker reference (ordered words) per participant."""
    wf = os.path.join(WORDS, f"{meeting}.words.json")
    if not os.path.exists(wf):
        print(f"  ! no word ref for {meeting}; skipping refs")
        return []
    words = json.load(open(wf))
    by_spk = {}
    for w in words:
        by_spk.setdefault(w.get("speaker", "?"), []).append(w)
    refs = []
    for spk, ws in sorted(by_spk.items()):
        ws.sort(key=lambda w: w.get("start", 0.0))
        text = " ".join(w.get("text", "") for w in ws).strip()
        if not text:
            continue
        rp = os.path.join(mdir, f"ref_{spk}.txt")
        with open(rp, "w") as f:
            f.write(text + "\n")
        refs.append({"speaker": spk, "words": len(ws), "ref": os.path.basename(rp)})
    return refs


def main():
    meetings = sys.argv[1:] or DEFAULT
    os.makedirs(OUT, exist_ok=True)
    manifest = {}
    for m in meetings:
        mdir = os.path.join(OUT, m)
        os.makedirs(mdir, exist_ok=True)
        print(f"{m}:")
        heads = []
        for n in range(MAX_HEADSETS):
            dest = os.path.join(mdir, f"{m}.Headset-{n}.wav")
            if fetch(AUDIO.format(m=m, n=n), dest):
                mb = os.path.getsize(dest) / 1e6
                print(f"  Headset-{n}.wav  {mb:.1f} MB")
                heads.append(os.path.basename(dest))
        refs = split_refs(m, mdir)
        manifest[m] = {"headsets": heads, "refs": refs}
        print(f"  -> {len(heads)} headsets, {len(refs)} speaker refs")
    with open(os.path.join(OUT, "manifest.json"), "w") as f:
        json.dump(manifest, f, indent=2)
    print(f"\nmanifest: {os.path.join(OUT, 'manifest.json')}")


if __name__ == "__main__":
    main()

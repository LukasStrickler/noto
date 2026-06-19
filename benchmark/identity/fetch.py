#!/usr/bin/env python3
"""Re-download AMI meeting audio + ground-truth diarization for the persona eval.

The AMI Meeting Corpus is real multi-party meetings. Crucially, the ES2002
*series* (a/b/c/d) records the **same four participants** across four separate
sessions, and the RTTM speaker labels are **global participant IDs**
(FEE005, MEE006, …) consistent across sessions — so it is genuine
cross-meeting ground truth for "is this the same person in another meeting?".

Sources (no auth):
  - audio:  AMI Edinburgh mirror, single-channel Mix-Headset wav per meeting
  - RTTM :  pyannote/AMI-diarization-setup (only_words), manual segments

Downloads into ./ami/ (next to this script), nothing committed:
  ami/<MEETING>.wav     16 kHz-ish Mix-Headset mix
  ami/<MEETING>.rttm    SPEAKER lines: start, dur, global speaker id

Usage:
  python3 fetch.py                 # ES2002 a/b/c/d (~275 MB)
  python3 fetch.py ES2002a ES2002b # a subset
Then: go test ./benchmark/identity/ -run Persona -v
"""
import json
import os
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "ami")
AUDIO = "https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/{m}/audio/{m}.Mix-Headset.wav"
RTTM = "https://raw.githubusercontent.com/pyannote/AMI-diarization-setup/main/only_words/rttms/{split}/{m}.rttm"
SPLITS = ("train", "dev", "test")
DEFAULT = ["ES2002a", "ES2002b", "ES2002c", "ES2002d"]


def fetch(url, dest):
    req = urllib.request.Request(url, headers={"User-Agent": "noto-persona-fetch"})
    with urllib.request.urlopen(req, timeout=120) as r, open(dest + ".part", "wb") as f:
        total = int(r.headers.get("Content-Length", 0))
        got = 0
        while True:
            chunk = r.read(1 << 20)
            if not chunk:
                break
            f.write(chunk)
            got += len(chunk)
            if total:
                pct = got * 100 // total
                print(f"\r    {os.path.basename(dest)}: {got/1e6:5.1f}/{total/1e6:5.1f} MB ({pct}%)",
                      end="", flush=True)
    os.replace(dest + ".part", dest)
    print()


def fetch_rttm(meeting, dest):
    for split in SPLITS:
        try:
            fetch(RTTM.format(split=split, m=meeting), dest)
            return split
        except urllib.error.HTTPError as e:
            if e.code == 404:
                continue
            raise
    raise SystemExit(f"no RTTM found for {meeting} in {SPLITS}")


def fetch_meeting(meeting, audio_url=None, rttm_url=None):
    """Download one meeting's wav + rttm into OUT (idempotent)."""
    print(f"{meeting}:")
    rttm = os.path.join(OUT, f"{meeting}.rttm")
    if os.path.exists(rttm):
        pass
    elif rttm_url:
        fetch(rttm_url, rttm)
        print("    rttm")
    else:
        print(f"    rttm <- {fetch_rttm(meeting, rttm)}")
    wav = os.path.join(OUT, f"{meeting}.wav")
    if os.path.exists(wav) and os.path.getsize(wav) > 1 << 20:
        print(f"    wav present ({os.path.getsize(wav)/1e6:.1f} MB)")
    else:
        fetch(audio_url or AUDIO.format(m=meeting), wav)


def main():
    import argparse

    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("meetings", nargs="*", help="explicit meeting IDs (default: all in dataset.json)")
    ap.add_argument("--dataset", default=os.path.join(HERE, "dataset.json"))
    args = ap.parse_args()
    os.makedirs(OUT, exist_ok=True)

    if args.meetings:
        for m in args.meetings:
            fetch_meeting(m)
    else:
        if not os.path.exists(args.dataset):
            raise SystemExit(f"no {args.dataset}; pass meeting IDs or run build_dataset.py")
        ds = json.load(open(args.dataset))
        print(f"dataset '{ds['name']}': {ds['num_speakers']} speakers, "
              f"{ds['num_meetings']} meetings, ~{ds['approx_total_mb']} MB")
        for i, m in enumerate(ds["meetings"], 1):
            print(f"[{i}/{len(ds['meetings'])}]", end=" ")
            fetch_meeting(m["id"], m.get("audio_url"), m.get("rttm_url"))
    print(f"\nready -> {OUT}")
    print("next: go test ./benchmark/identity/ -run Persona -v")


if __name__ == "__main__":
    main()

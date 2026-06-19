#!/usr/bin/env python3
"""Generate dataset.json: a curated ~50-speaker AMI subset for the persona bench.

Indexes the AMI ground-truth RTTMs (tiny), then greedily picks N sessions from
each scenario series (same participants across a/b/c/d) until ~`--target`
distinct speakers are covered — so every speaker appears in >=N meetings and
cross-meeting linking is testable. Audio is NOT downloaded here (that's
fetch.py); we only need the RTTMs to decide the roster, and we record the
download URLs + an approximate size per meeting.

Deterministic: series are ranked by (total size of the N shortest sessions,
name), so re-running yields the same dataset.json.

  python3 build_dataset.py                       # ~50 speakers, 3 sessions each
  python3 build_dataset.py --target 50 --sessions 3
  python3 build_dataset.py --rttm-cache /tmp/ami_rttms   # reuse downloaded RTTMs
"""
import argparse
import collections
import json
import os
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
API = "https://api.github.com/repos/pyannote/AMI-diarization-setup/contents/only_words/rttms/{split}"
RTTM_RAW = "https://raw.githubusercontent.com/pyannote/AMI-diarization-setup/main/only_words/rttms/{split}/{m}.rttm"
AUDIO = "https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/{m}/audio/{m}.Mix-Headset.wav"
SPLITS = ("train", "dev", "test")
BYTES_PER_SEC = 32000  # 16 kHz mono 16-bit Mix-Headset


def get(url):
    req = urllib.request.Request(url, headers={"User-Agent": "noto-bench"})
    with urllib.request.urlopen(req, timeout=60) as r:
        return r.read()


def list_meetings():
    out = []
    for split in SPLITS:
        data = json.loads(get(API.format(split=split)))
        for x in data:
            if isinstance(x, dict) and x.get("name", "").endswith(".rttm"):
                out.append((split, x["name"][:-5]))
    return out


def load_rttm(text):
    """returns (speaker->dur, end_time)"""
    spk = collections.defaultdict(float)
    end = 0.0
    for line in text.splitlines():
        p = line.split()
        if len(p) < 8 or p[0] != "SPEAKER":
            continue
        st, du = float(p[3]), float(p[4])
        spk[p[7]] += du
        end = max(end, st + du)
    return spk, end


def series_of(m):
    return m[:-1] if (m[-1].isalpha() and m[-2].isdigit()) else m


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--target", type=int, default=50, help="approx distinct speakers")
    ap.add_argument("--sessions", type=int, default=3, help="sessions per series (meetings per speaker)")
    ap.add_argument("--rttm-cache", default="", help="dir of pre-downloaded {split}/{m}.rttm")
    ap.add_argument("--out", default=os.path.join(HERE, "dataset.json"))
    args = ap.parse_args()

    meetings = list_meetings()
    print(f"indexing {len(meetings)} AMI meetings ...")
    meet_spk, meet_end, meet_split = {}, {}, {}
    for split, m in meetings:
        cache = os.path.join(args.rttm_cache, split, m + ".rttm") if args.rttm_cache else ""
        if cache and os.path.exists(cache):
            text = open(cache).read()
        else:
            try:
                text = get(RTTM_RAW.format(split=split, m=m)).decode()
            except Exception:
                continue
        spk, end = load_rttm(text)
        if not spk:
            continue
        meet_spk[m], meet_end[m], meet_split[m] = dict(spk), end, split

    mb = lambda m: meet_end[m] * BYTES_PER_SEC / 1e6
    ser = collections.defaultdict(list)
    for m in meet_spk:
        ser[series_of(m)].append(m)

    # series with >= N sessions sharing a consistent participant set
    good = []
    for s, ms in ser.items():
        if len(ms) < args.sessions:
            continue
        common = set.intersection(*[set(meet_spk[m]) for m in ms])
        if len(common) >= 3:
            good.append((s, ms, common))
    good.sort(key=lambda g: (sum(sorted(mb(m) for m in g[1])[: args.sessions]), g[0]))

    selected, covered = [], set()
    for s, ms, common in good:
        take = sorted(ms, key=lambda m: (mb(m), m))[: args.sessions]
        selected += take
        covered |= set(common)
        if len(covered) >= args.target:
            break
    selected = sorted(set(selected))

    roster = sorted({sp for m in selected for sp in meet_spk[m] if sp in covered})
    ds = {
        "name": "ami-personas",
        "description": (
            "Curated AMI Meeting Corpus subset: scenario series (same participants "
            "across sessions) chosen so each speaker appears in multiple meetings. "
            "Mix-Headset single-channel audio + pyannote AMI-diarization-setup RTTM "
            "(global speaker IDs = cross-meeting ground truth). Media is re-downloadable "
            "via fetch.py, never committed."
        ),
        "sessions_per_series": args.sessions,
        "num_speakers": len(roster),
        "num_meetings": len(selected),
        "approx_total_mb": round(sum(mb(m) for m in selected)),
        "speakers": roster,
        "meetings": [
            {
                "id": m,
                "split": meet_split[m],
                "approx_mb": round(mb(m), 1),
                "speakers": sorted(meet_spk[m]),
                "audio_url": AUDIO.format(m=m),
                "rttm_url": RTTM_RAW.format(split=meet_split[m], m=m),
            }
            for m in selected
        ],
    }
    with open(args.out, "w") as f:
        json.dump(ds, f, indent=1)
    print(f"speakers={len(roster)} meetings={len(selected)} approx={ds['approx_total_mb']} MB")
    print(f"wrote {args.out}")


if __name__ == "__main__":
    main()

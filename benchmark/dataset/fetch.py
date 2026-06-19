#!/usr/bin/env python3
"""Re-download the sample meeting media used to sanity-check speaker ID.

None of this audio/video is kept in git (see .gitignore) — it is fully
re-creatable from the public LibriSpeech `clean/validation` split via the
Hugging Face datasets-server, plus ffmpeg for the synthetic meeting clip.

What it produces under ./librispeech/ (next to this script):

  spk<ID>_<n>.flac   labelled single-speaker clips (default 24 speakers x 4)
  manifest.json      { "<speaker_id>": ["spk<ID>_0.flac", ...], ... }

and, if ffmpeg is on PATH, under ./synthetic/:

  meeting_2spk.wav   two speakers alternating turns on one 16 kHz mono track
  meeting_2spk.mp4   the same audio muxed over a color/text video (a real
                     "video file" to point `noto import-audio` at)
  meeting_2spk.json  ground-truth turn timeline (speaker_id + [start,end] s)

Usage:
  python3 fetch.py                  # 24 speakers x 4 clips + synthetic meeting
  python3 fetch.py --speakers 8     # fewer speakers (faster)
  python3 fetch.py --no-video       # skip the ffmpeg synthetic meeting
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import urllib.parse
import urllib.request

DATASET = "openslr/librispeech_asr"
CONFIG = "clean"
SPLIT = "validation"
ROWS_URL = "https://datasets-server.huggingface.co/rows"
PAGE = 100
HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.normpath(os.path.join(HERE, "..", ".."))


def find_ffmpeg():
    """Prefer $FFMPEG, then PATH, then the static binary the speaker-model
    download drops in the dev tools dir."""
    cand = os.environ.get("FFMPEG") or shutil.which("ffmpeg")
    if cand and os.path.exists(cand):
        return cand
    local = os.path.join(REPO, "tools", "voiceprint", "bin", "ffmpeg")
    return local if os.path.exists(local) else None


def _get(url, retries=3):
    last = None
    for _ in range(retries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "noto-bench-fetch"})
            with urllib.request.urlopen(req, timeout=60) as r:
                return r.read()
        except Exception as e:  # noqa: BLE001 - transient HF/network errors
            last = e
    raise last


def page(offset, length):
    q = urllib.parse.urlencode(
        {"dataset": DATASET, "config": CONFIG, "split": SPLIT, "offset": offset, "length": length}
    )
    return json.loads(_get(f"{ROWS_URL}?{q}"))


def fetch_clips(out_dir, n_speakers, clips_per):
    """Walk rows in order; keep the first `clips_per` clips of the first
    `n_speakers` speakers we encounter. Same ordering the manifest was built
    with, so filenames stay stable across re-fetches."""
    os.makedirs(out_dir, exist_ok=True)
    by_spk = {}  # speaker_id -> [src_url, ...]
    order = []  # speaker_ids in first-seen order
    offset = 0
    first = page(0, PAGE)
    total = first["num_rows_total"]
    rows_pages = [first["rows"]]
    while True:
        rows = rows_pages.pop(0) if rows_pages else page(offset, PAGE)["rows"]
        if not rows:
            break
        for item in rows:
            row = item["row"]
            spk = str(row["speaker_id"])
            if spk not in by_spk:
                if len(order) >= n_speakers:
                    continue
                by_spk[spk] = []
                order.append(spk)
            if len(by_spk[spk]) < clips_per:
                by_spk[spk].append(row["audio"][0]["src"])
        done = len(order) >= n_speakers and all(len(by_spk[s]) >= clips_per for s in order)
        offset += len(rows)
        if done or offset >= total:
            break

    manifest = {}
    for spk in order:
        names = []
        for i, src in enumerate(by_spk[spk][:clips_per]):
            name = f"spk{spk}_{i}.flac"
            path = os.path.join(out_dir, name)
            with open(path, "wb") as f:
                f.write(_get(src))
            names.append(name)
        manifest[spk] = names
        print(f"  {spk}: {len(names)} clips")
    with open(os.path.join(out_dir, "manifest.json"), "w") as f:
        json.dump(manifest, f)
    return manifest, out_dir


def build_meeting(clip_dir, manifest, out_dir):
    """Alternate two speakers' clips into one track and mux over video.
    Reproducible 'meeting' file for the import/injection path."""
    ffmpeg = find_ffmpeg()
    if ffmpeg is None:
        print("ffmpeg not found ($FFMPEG / PATH / tools/voiceprint/bin); "
              "run `noto speaker-model download` first or set FFMPEG. Skipping video.")
        return
    spks = list(manifest.keys())[:2]
    if len(spks) < 2:
        print("need >=2 speakers for a meeting; skipping")
        return
    os.makedirs(out_dir, exist_ok=True)
    a, b = spks
    # interleave a0, b0, a1, b1, ... so turns clearly alternate
    seq = []
    for i in range(max(len(manifest[a]), len(manifest[b]))):
        if i < len(manifest[a]):
            seq.append((a, manifest[a][i]))
        if i < len(manifest[b]):
            seq.append((b, manifest[b][i]))

    # decode each clip to a raw 16k mono wav and concat, tracking turn bounds.
    # durations come from the 16-bit/mono/16 kHz wav byte count (no ffprobe dep).
    timeline = []
    parts = []
    concat_list = os.path.join(out_dir, "_concat.txt")
    with open(concat_list, "w") as lf:
        cur = 0.0
        for spk, name in seq:
            src = os.path.join(clip_dir, name)
            wav = os.path.join(out_dir, f"_part_{len(parts)}.wav")
            subprocess.run(
                [ffmpeg, "-y", "-loglevel", "error", "-i", src,
                 "-ac", "1", "-ar", "16000", wav],
                check=True,
            )
            dur = wav_seconds(wav)
            timeline.append({"speaker_id": spk, "start": round(cur, 3), "end": round(cur + dur, 3)})
            cur += dur
            parts.append(wav)
            lf.write(f"file '{os.path.abspath(wav)}'\n")

    wav_out = os.path.join(out_dir, "meeting_2spk.wav")
    subprocess.run(
        [ffmpeg, "-y", "-loglevel", "error", "-f", "concat", "-safe", "0",
         "-i", concat_list, "-ac", "1", "-ar", "16000", wav_out],
        check=True,
    )
    total = wav_seconds(wav_out)
    mp4_out = os.path.join(out_dir, "meeting_2spk.mp4")
    # plain color video track muxed with the meeting audio — a real video
    # container to exercise the "point at any video file" import path.
    subprocess.run(
        [ffmpeg, "-y", "-loglevel", "error",
         "-f", "lavfi", "-i", f"color=c=0x1e1e2e:s=640x360:d={total:.2f}",
         "-i", wav_out,
         "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", mp4_out],
        check=True,
    )
    with open(os.path.join(out_dir, "meeting_2spk.json"), "w") as f:
        json.dump({"speakers": [a, b], "duration": round(total, 3), "turns": timeline}, f, indent=1)
    for p in parts:
        os.remove(p)
    os.remove(concat_list)
    print(f"  synthetic meeting: {mp4_out} ({total:.1f}s, speakers {a}+{b})")


def wav_seconds(path):
    """Duration of a 16 kHz mono 16-bit PCM wav from its size (44-byte header)."""
    return max(0.0, (os.path.getsize(path) - 44) / 2.0 / 16000.0)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--speakers", type=int, default=24, help="number of speakers (default 24)")
    ap.add_argument("--clips-per", type=int, default=4, help="clips per speaker (default 4)")
    ap.add_argument("--out", default=os.path.join(HERE, "librispeech"), help="output dir for clips")
    ap.add_argument("--no-video", action="store_true", help="skip the ffmpeg synthetic meeting")
    args = ap.parse_args()

    print(f"fetching {args.speakers} speakers x {args.clips_per} clips from "
          f"{DATASET} [{CONFIG}/{SPLIT}] ...")
    manifest, clip_dir = fetch_clips(args.out, args.speakers, args.clips_per)
    print(f"wrote {sum(len(v) for v in manifest.values())} clips -> {clip_dir}")

    if not args.no_video:
        build_meeting(clip_dir, manifest, os.path.join(HERE, "synthetic"))

    print("\nnext:")
    print("  # cross-meeting precision (Go; reads these clips from benchmark/dataset/librispeech).")
    print("  # model/lib/ffmpeg default to tools/voiceprint/... or override via ECAPA_MODEL/ORT_LIB/FFMPEG:")
    print("  go test ./internal/platform/providers/speaker/ -run CrossMeeting -v")
    if not args.no_video:
        print("  # full pipeline on a real video file:")
        print(f"  noto import-audio {os.path.join(HERE, 'synthetic', 'meeting_2spk.mp4')} --wait")


if __name__ == "__main__":
    sys.exit(main())

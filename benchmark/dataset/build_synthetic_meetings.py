#!/usr/bin/env python3
"""Build fully-labeled synthetic multi-speaker meetings from LibriSpeech clips.

This is the corpus that lets us measure the WHOLE attributed pipeline — not just
STT, but who-said-what — because every stage's ground truth is known by
construction:

  - speaker identity : each clip's LibriSpeech speaker_id (and the SAME speakers
                       recur across meetings → cross-meeting identity ground truth)
  - diarization      : the [start,end] we PLACE each clip at → exact turns (RTTM)
  - transcription    : the clip's exact text → reference words
  - attribution      : text + speaker + timing together → cpWER / SA-WER ground truth
  - overlap          : a fraction of turns are deliberately OVERLAPPED (mixed
                       audio) to test that overlapping speech is handled

Clips are decoded to 16 kHz mono PCM and summed onto a timeline (overlaps add),
then written as one WAV per meeting with sidecar ground truth.

Output (gitignored) under ./synthetic_meetings/:
  mtgNN.wav              the mixed meeting audio (16 kHz mono)
  mtgNN.words.json       [{speaker,start,end,text}] per word (WER/cpWER/SA-WER ref)
  mtgNN.rttm             SPEAKER turns (DER ref)
  meetings.json          { meeting -> {speakers:[...], duration, num_speakers} } (identity ref)

Usage:
  FFMPEG=/path/to/ffmpeg python3 build_synthetic_meetings.py \
      --speakers 6 --per-meeting 4 --meetings 3 --clips-per-speaker 14 \
      --overlap-frac 0.18 --seed 7
"""
import argparse
import array
import json
import os
import random
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request
import wave

DATASET = "openslr/librispeech_asr"
CONFIG = "clean"
SPLIT = "validation"
ROWS_URL = "https://datasets-server.huggingface.co/rows"
PAGE = 100
SR = 16000
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "synthetic_meetings")


def ffmpeg():
    cand = os.environ.get("FFMPEG") or shutil.which("ffmpeg")
    if not cand or not os.path.exists(cand):
        raise SystemExit("need ffmpeg: set FFMPEG=/path/to/ffmpeg")
    return cand


def _get(url, retries=3):
    last = RuntimeError("no attempt")
    for _ in range(retries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "noto-bench-fetch"})
            with urllib.request.urlopen(req, timeout=120) as r:
                return r.read()
        except Exception as e:  # noqa: BLE001
            last = e
    raise last


def page(offset, length):
    q = urllib.parse.urlencode(
        {"dataset": DATASET, "config": CONFIG, "split": SPLIT, "offset": offset, "length": length}
    )
    return json.loads(_get(f"{ROWS_URL}?{q}"))


def fetch_by_speaker(n_speakers, clips_per, tmp, ff):
    """Collect clips_per (audio_pcm, text) per speaker for the first n_speakers
    speakers seen — clips decoded to 16 kHz mono int16 samples."""
    by_spk = {}
    order = []
    offset = 0
    total = page(0, 1)["num_rows_total"]
    while True:
        rows = page(offset, PAGE).get("rows", [])
        if not rows:
            break
        for item in rows:
            row = item["row"]
            spk = str(row["speaker_id"])
            text = (row.get("text") or "").strip()
            if not text:
                continue
            if spk not in by_spk:
                if len(order) >= n_speakers:
                    continue
                by_spk[spk] = []
                order.append(spk)
            if len(by_spk[spk]) >= clips_per:
                continue
            # download + decode to 16k mono int16
            flac = os.path.join(tmp, "c.flac")
            wav = os.path.join(tmp, "c.wav")
            try:
                with open(flac, "wb") as f:
                    f.write(_get(row["audio"][0]["src"]))
                subprocess.run([ff, "-nostdin", "-hide_banner", "-loglevel", "error",
                                "-i", flac, "-ar", str(SR), "-ac", "1", "-c:a", "pcm_s16le",
                                "-f", "wav", "-y", wav], check=True)
                with wave.open(wav, "rb") as w:
                    pcm = array.array("h")
                    pcm.frombytes(w.readframes(w.getnframes()))
            except Exception as e:  # noqa: BLE001
                print(f"  skip clip for {spk}: {e}")
                continue
            by_spk[spk].append((pcm, text))
        done = len(order) >= n_speakers and all(len(by_spk[s]) >= clips_per for s in order)
        offset += len(rows)
        if done or offset >= total:
            break
    return order, by_spk


def build_meeting(name, spk_pool, clips_by_spk, cursor, n_spk, overlap_frac, rng):
    """Lay clips from n_spk speakers onto one timeline, sometimes overlapping,
    and mix to WAV with ground-truth words + RTTM turns."""
    speakers = rng.sample(spk_pool, n_spk)
    # round-robin turns drawing each speaker's next unused clip
    turns = []  # (speaker, pcm, text)
    active = list(speakers)
    while active:
        rng.shuffle(active)
        progressed = False
        for spk in list(active):
            i = cursor[spk]
            if i >= len(clips_by_spk[spk]):
                active.remove(spk)
                continue
            pcm, text = clips_by_spk[spk][i]
            cursor[spk] = i + 1
            turns.append((spk, pcm, text))
            progressed = True
        if not progressed:
            break

    gap = int(0.25 * SR)
    timeline = []  # (speaker, start_sample, pcm, text)
    pos = 0
    for idx, (spk, pcm, text) in enumerate(turns):
        start = pos
        # Overlap a fraction of turns onto the previous one (skip the first).
        if idx > 0 and rng.random() < overlap_frac:
            prev_start = timeline[-1][1]
            prev_len = len(timeline[-1][2])
            ov = int(rng.uniform(0.6, 1.8) * SR)
            start = max(prev_start + prev_len - ov, prev_start + int(0.2 * SR))
        timeline.append((spk, start, pcm, text))
        pos = start + len(pcm) + gap

    total = pos
    mix = array.array("i", [0]) * total  # int32 accumulator
    for spk, start, pcm, text in timeline:
        for j, s in enumerate(pcm):
            mix[start + j] += s
    # clamp to int16
    out = array.array("h", [0]) * total
    for i, v in enumerate(mix):
        out[i] = 32767 if v > 32767 else (-32768 if v < -32768 else v)

    wav_path = os.path.join(OUT, f"{name}.wav")
    with wave.open(wav_path, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(SR)
        w.writeframes(out.tobytes())

    # Ground truth: words (spread across each clip span) + RTTM turns.
    words = []
    rttm = []
    for spk, start, pcm, text in timeline:
        s0 = start / SR
        s1 = (start + len(pcm)) / SR
        rttm.append((spk, s0, s1 - s0))
        toks = text.split()
        for k, tok in enumerate(toks):
            ws = s0 + (s1 - s0) * k / max(len(toks), 1)
            we = s0 + (s1 - s0) * (k + 1) / max(len(toks), 1)
            words.append({"speaker": spk, "start": round(ws, 3), "end": round(we, 3), "text": tok})
    words.sort(key=lambda x: x["start"])
    with open(os.path.join(OUT, f"{name}.words.json"), "w") as f:
        json.dump(words, f)
    with open(os.path.join(OUT, f"{name}.rttm"), "w") as f:
        for spk, s0, dur in sorted(rttm, key=lambda x: x[1]):
            f.write(f"SPEAKER {name} 1 {s0:.3f} {dur:.3f} <NA> <NA> {spk} <NA> <NA>\n")
    return {"speakers": speakers, "duration": round(total / SR, 2), "num_speakers": n_spk}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--speakers", type=int, default=6, help="speaker pool size")
    ap.add_argument("--per-meeting", type=int, default=4, help="speakers per meeting")
    ap.add_argument("--meetings", type=int, default=3)
    ap.add_argument("--clips-per-speaker", type=int, default=14)
    ap.add_argument("--overlap-frac", type=float, default=0.18)
    ap.add_argument("--seed", type=int, default=7)
    args = ap.parse_args()
    ff = ffmpeg()
    os.makedirs(OUT, exist_ok=True)
    # Clear any meetings from a previous (possibly larger) build so a smaller
    # --meetings count can't leave ghost mtgNN files behind: the benches discover
    # meetings by globbing *.words.json (e2e/synth_test.go), so a stale file would
    # silently re-add a meeting that meetings.json no longer lists.
    for fn in os.listdir(OUT):
        if fn.startswith("mtg") or fn == "meetings.json":
            os.remove(os.path.join(OUT, fn))
    rng = random.Random(args.seed)

    with tempfile.TemporaryDirectory() as tmp:
        print(f"fetching {args.speakers} speakers × {args.clips_per_speaker} clips …")
        order, by_spk = fetch_by_speaker(args.speakers, args.clips_per_speaker, tmp, ff)
        order = [s for s in order if len(by_spk[s]) >= 2]
        if len(order) < args.per_meeting:
            raise SystemExit(f"only got {len(order)} usable speakers")
        print(f"  speakers: {order}")

        cursor = {s: 0 for s in order}
        meetings = {}
        for n in range(args.meetings):
            name = f"mtg{n:02d}"
            # reset cursors per meeting so speakers reuse their clips across meetings
            cursor = {s: 0 for s in order}
            info = build_meeting(name, order, by_spk, cursor, args.per_meeting,
                                 args.overlap_frac, rng)
            meetings[name] = info
            print(f"  {name}: {info['num_speakers']} spk, {info['duration']}s, speakers={info['speakers']}")

    with open(os.path.join(OUT, "meetings.json"), "w") as f:
        json.dump(meetings, f, indent=0)
    print(f"\nOK → {OUT}")
    print("  per meeting: <id>.wav, <id>.words.json, <id>.rttm; plus meetings.json")


if __name__ == "__main__":
    main()

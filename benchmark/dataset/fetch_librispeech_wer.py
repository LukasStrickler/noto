#!/usr/bin/env python3
"""Build a per-clip LibriSpeech STT (WER) corpus.

The diarization fetchers grab AMI audio + RTTM (turns, no text); the STT bench
needs reference *words*. LibriSpeech `clean` is read speech with exact
transcripts — the clean WER baseline the draft lists. Each clip is scored
INDEPENDENTLY (the standard LibriSpeech protocol): no concatenation, so there
are no artificial clip-junction errors inflating WER.

Output (gitignored, under ./librispeech_wer/):
  <id>.wav        16 kHz mono clips (sherpa reads WAV directly)
  refs.json       { "<id>.wav": "reference text", ... }

Usage:
  FFMPEG=/path/to/ffmpeg python3 fetch_librispeech_wer.py --clips 170
"""
import argparse
import json
import os
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request

DATASET = "openslr/librispeech_asr"
CONFIG = "clean"
SPLIT = "validation"
ROWS_URL = "https://datasets-server.huggingface.co/rows"
PAGE = 100
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "librispeech_wer")


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


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--clips", type=int, default=170)
    args = ap.parse_args()
    ff = ffmpeg()
    os.makedirs(OUT, exist_ok=True)

    print(f"fetching {args.clips} LibriSpeech {CONFIG}/{SPLIT} clips (audio+text) → per-clip WAV …")
    refs = {}
    total_sec = 0.0
    offset = 0
    with tempfile.TemporaryDirectory() as tmp:
        while len(refs) < args.clips:
            rows = page(offset, PAGE).get("rows", [])
            if not rows:
                break
            for item in rows:
                if len(refs) >= args.clips:
                    break
                row = item["row"]
                text = (row.get("text") or "").strip()
                if not text:
                    continue
                idx = len(refs)
                src = row["audio"][0]["src"]
                flac = os.path.join(tmp, f"c{idx}.flac")
                wav_name = f"clip{idx:04d}.wav"
                wav = os.path.join(OUT, wav_name)
                try:
                    with open(flac, "wb") as f:
                        f.write(_get(src))
                    subprocess.run(
                        [ff, "-nostdin", "-hide_banner", "-loglevel", "error",
                         "-i", flac, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
                         "-f", "wav", "-y", wav],
                        check=True,
                    )
                except Exception as e:  # noqa: BLE001
                    print(f"  skip clip {idx}: {e}")
                    continue
                import wave
                with wave.open(wav, "rb") as w:
                    total_sec += w.getnframes() / float(w.getframerate())
                refs[wav_name] = text
            offset += len(rows)
            print(f"  {len(refs)}/{args.clips}")

    with open(os.path.join(OUT, "refs.json"), "w") as f:
        json.dump(refs, f, indent=0)

    words = sum(len(t.split()) for t in refs.values())
    print(f"\nOK: {len(refs)} clips → {total_sec/60:.1f} min, {words} reference words")
    print(f"  dir : {OUT}")
    print(f"\nRun: BENCH_LIBRI_DIR={OUT} make bench-stt-libri   (or the go test directly)")


if __name__ == "__main__":
    main()

#!/usr/bin/env python3
"""Build a per-clip EdAcc (accented English) STT/WER corpus — a PARAKEET-WEAK set.

LibriSpeech is clean read speech where parakeet is already near-ceiling (~2% WER),
so transcription repair has no headroom there (confirmed on AMI too). EdAcc — the
Edinburgh International Accents of English Corpus — is spontaneous CONVERSATIONAL
English from dozens of L1 backgrounds (Spanish, Urdu, Kenyan, Chinese, …); it was
built specifically to expose ASR accent bias, and SOTA systems post 20-40%+ WER on
it. That's where an alternate ASR source (canary / a larger model) can actually
beat parakeet, so it's the dataset the B7 repair gate needs.

The split is ORDERED BY CONVERSATION (the first ~hundreds of rows are one speaker /
one accent), so a naive head() would be single-accent. We SPREAD the sample across
the whole 9289-row test split to span speakers and accents, and record each clip's
accent + L1 so WER can be sliced by accent (the hardest accents = the most repair
headroom).

Output (gitignored, under ./edacc_wer/):
  clip0000.wav    16 kHz mono clips
  refs.json       { "<clip>.wav": "REFERENCE TEXT", ... }
  meta.json       { "<clip>.wav": {"accent": "...", "l1": "..."}, ... }

Usage:
  FFMPEG=/path/to/ffmpeg python3 fetch_edacc.py --clips 120 --min-words 4
"""
import argparse
import json
import os
import re
import shutil
import subprocess
import tempfile
import urllib.parse
import urllib.request

# EdAcc references carry non-speech annotation tokens (<NO-SPEECH>, <OVERLAP>,
# <DTMF>, <LAUGH>, …). They are NOT words to transcribe — left in, the WER
# normalizer would coin fake words ("nospeech") and inflate WER. Standard EdAcc
# scoring strips all <...> tags; do it here so refs.json is scorable as-is.
_ANNOT_RE = re.compile(r"<[^>]*>")


def clean_text(t: str) -> str:
    return " ".join(_ANNOT_RE.sub(" ", t).split())

DATASET = "edinburghcstr/edacc"
CONFIG = "default"
SPLIT = "test"
ROWS_URL = "https://datasets-server.huggingface.co/rows"
SIZE_URL = "https://datasets-server.huggingface.co/size"
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "edacc_wer")


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


def total_rows() -> int:
    q = urllib.parse.urlencode({"dataset": DATASET, "config": CONFIG})
    try:
        d = json.loads(_get(f"{SIZE_URL}?{q}"))
        for s in d.get("size", {}).get("splits", []):
            if s.get("split") == SPLIT:
                return int(s.get("num_rows") or 0)
    except Exception:  # noqa: BLE001
        pass
    return 9289  # known EdAcc test size; fallback if the size endpoint is flaky


def page(offset, length):
    q = urllib.parse.urlencode(
        {"dataset": DATASET, "config": CONFIG, "split": SPLIT, "offset": offset, "length": length}
    )
    return json.loads(_get(f"{ROWS_URL}?{q}"))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--clips", type=int, default=120)
    ap.add_argument("--min-words", type=int, default=4, help="skip clips shorter than this (too short to score)")
    ap.add_argument("--per-stop", type=int, default=5, help="clips taken per spread offset")
    args = ap.parse_args()
    ff = ffmpeg()
    os.makedirs(OUT, exist_ok=True)

    total = total_rows()
    stops = max(1, args.clips // args.per_stop)
    stride = max(1, total // stops)
    print(f"fetching ~{args.clips} EdAcc {SPLIT} clips spread across {total} rows "
          f"({stops} stops, stride {stride}) → per-clip WAV …")

    refs, meta = {}, {}
    total_sec = 0.0
    with tempfile.TemporaryDirectory() as tmp:
        for s in range(stops):
            if len(refs) >= args.clips:
                break
            offset = min(s * stride, max(0, total - args.per_stop))
            try:
                rows = page(offset, args.per_stop).get("rows", [])
            except Exception as e:  # noqa: BLE001
                print(f"  skip offset {offset}: {e}")
                continue
            for item in rows:
                if len(refs) >= args.clips:
                    break
                row = item["row"]
                text = clean_text(row.get("text") or "")
                if len(text.split()) < args.min_words:
                    continue
                idx = len(refs)
                try:
                    src = row["audio"][0]["src"]
                except (KeyError, IndexError, TypeError):
                    continue
                raw = os.path.join(tmp, f"c{idx}.src")
                wav_name = f"clip{idx:04d}.wav"
                wav = os.path.join(OUT, wav_name)
                try:
                    with open(raw, "wb") as f:
                        f.write(_get(src))
                    subprocess.run(
                        [ff, "-nostdin", "-hide_banner", "-loglevel", "error",
                         "-i", raw, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
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
                meta[wav_name] = {
                    "accent": (row.get("accent") or "").strip(),
                    "l1": (row.get("l1") or "").strip(),
                }
            print(f"  {len(refs)}/{args.clips} (offset {offset})")

    with open(os.path.join(OUT, "refs.json"), "w") as f:
        json.dump(refs, f, indent=0)
    with open(os.path.join(OUT, "meta.json"), "w") as f:
        json.dump(meta, f, indent=0)

    words = sum(len(t.split()) for t in refs.values())
    accents = {}
    for m in meta.values():
        accents[m["accent"]] = accents.get(m["accent"], 0) + 1
    print(f"\nOK: {len(refs)} clips → {total_sec/60:.1f} min, {words} reference words")
    print(f"  accents: " + ", ".join(f"{a}={n}" for a, n in sorted(accents.items(), key=lambda x: -x[1])))
    print(f"  dir : {OUT}")


if __name__ == "__main__":
    main()

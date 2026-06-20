"""parakeet WER on EdAcc (accented English) — a Modal STT validation pass.

De-risks the B7 repair direction: BEFORE building the repair gate on EdAcc, confirm
parakeet is actually WEAK here (high WER) — otherwise there's no headroom for an
alternate source to repair, same dead-end as AMI. Runs the production
parakeet_stt_server.py on a spread sample of EdAcc test clips (fetched on the box
via the HF datasets-server /rows API, same path as benchmark/dataset/fetch_edacc.py)
and returns per-clip {ref, hyp, accent}; WER scoring + accent slicing is local
(score_edacc.py).

Self-contained on purpose (Modal re-imports this module in the container). Run:
  modal run scripts/modal_edacc_wer.py --clips 80
"""
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.parse
import urllib.request
from pathlib import Path

import modal

REPO_ROOT = Path(__file__).resolve().parents[1]
REMOTE_REPO = Path("/root/noto")
MODEL_MOUNT = Path("/cache/model")
GPU = os.getenv("NOTO_MODAL_GPU", "L40S")
CUDA_IMAGE = os.getenv("NOTO_MODAL_CUDA_IMAGE", "nvidia/cuda:12.4.1-cudnn-devel-ubuntu22.04")
MODEL_VOLUME = os.getenv("NOTO_MODAL_MODEL_VOLUME", "noto-model-cache")
ENVIRONMENT = os.getenv("NOTO_MODAL_ENVIRONMENT") or None

DATASET = "edinburghcstr/edacc"
CONFIG = "default"
SPLIT = "test"
ROWS_URL = "https://datasets-server.huggingface.co/rows"
TOTAL_ROWS = 9289
_ANNOT_RE = re.compile(r"<[^>]*>")


def _ignore(path: Path) -> bool:
    rel = path.relative_to(REPO_ROOT) if path.is_absolute() else path
    if set(rel.parts) & {".git", ".tools", ".venv", ".venv-modal", "bin", "tmp",
                         ".modal-results", ".code-review-graph"}:
        return True
    if rel.name.startswith(".env"):
        return True
    big = {
        Path("benchmark/identity/ami"), Path("benchmark/dataset/librispeech"),
        Path("benchmark/dataset/librispeech_wer"), Path("benchmark/dataset/synthetic"),
        Path("benchmark/dataset/synthetic_meetings"), Path("benchmark/dataset/words"),
        Path("benchmark/dataset/ami_ihm"), Path("benchmark/dataset/edacc_wer"),
    }
    return any(rel == d or d in rel.parents for d in big)


image = (
    modal.Image.from_registry(CUDA_IMAGE, add_python="3.12")
    .apt_install("bash", "ca-certificates", "curl", "bzip2", "xz-utils", "git", "ffmpeg")
    .pip_install("pyannote.audio>=4.0,<5")
    .pip_install("nemo_toolkit[asr]>=2.4", "cuda-python>=12.3")
    .pip_install("silero-vad>=5.1", "onnxruntime>=1.16")
    .add_local_dir(str(REPO_ROOT), str(REMOTE_REPO), copy=True, ignore=_ignore)
)
app = modal.App("noto-edacc-wer", image=image)
model_volume = modal.Volume.from_name(MODEL_VOLUME, create_if_missing=True, environment_name=ENVIRONMENT)


def _get(url, retries=3):
    last = RuntimeError("no attempt")
    for _ in range(retries):
        try:
            req = urllib.request.Request(url, headers={"User-Agent": "noto-edacc"})
            with urllib.request.urlopen(req, timeout=120) as r:
                return r.read()
        except Exception as e:  # noqa: BLE001
            last = e
    raise last


def clean_text(t: str) -> str:
    return " ".join(_ANNOT_RE.sub(" ", t).split())


@app.function(gpu=GPU, volumes={str(MODEL_MOUNT): model_volume}, timeout=2400)
def edacc_transcribe(clips: int = 80, min_words: int = 4, per_stop: int = 4) -> dict:
    wavdir = Path("/tmp/edacc")
    wavdir.mkdir(parents=True, exist_ok=True)
    ff = "ffmpeg"

    # Fetch a spread sample (the split is ordered by conversation → spread for
    # accent diversity), cleaning EdAcc's <...> annotation tags from refs.
    stops = max(1, clips // per_stop)
    stride = max(1, TOTAL_ROWS // stops)
    items = []  # (wavpath, ref, accent, l1)
    with tempfile.TemporaryDirectory() as tmp:
        for s in range(stops):
            if len(items) >= clips:
                break
            offset = min(s * stride, max(0, TOTAL_ROWS - per_stop))
            q = urllib.parse.urlencode({"dataset": DATASET, "config": CONFIG, "split": SPLIT,
                                        "offset": offset, "length": per_stop})
            try:
                rows = json.loads(_get(f"{ROWS_URL}?{q}")).get("rows", [])
            except Exception as e:  # noqa: BLE001
                print(f"skip offset {offset}: {e}")
                continue
            for it in rows:
                if len(items) >= clips:
                    break
                row = it["row"]
                ref = clean_text(row.get("text") or "")
                if len(ref.split()) < min_words:
                    continue
                try:
                    src = row["audio"][0]["src"]
                except (KeyError, IndexError, TypeError):
                    continue
                idx = len(items)
                raw = os.path.join(tmp, f"c{idx}.src")
                wav = wavdir / f"clip{idx:04d}.wav"
                try:
                    with open(raw, "wb") as f:
                        f.write(_get(src))
                    subprocess.run([ff, "-nostdin", "-hide_banner", "-loglevel", "error",
                                    "-i", raw, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le",
                                    "-f", "wav", "-y", str(wav)], check=True)
                except Exception as e:  # noqa: BLE001
                    print(f"skip clip {idx}: {e}")
                    continue
                items.append((str(wav), ref, (row.get("accent") or "").strip(),
                              (row.get("l1") or "").strip()))
    print(f"fetched {len(items)} EdAcc clips")

    env = {
        **os.environ,
        "HF_HOME": str(MODEL_MOUNT / "hf"),
        "NOTO_PARAKEET_PROVIDER": "cuda",
        "NOTO_PARAKEET_PRECISION": "bf16",
        "NOTO_PARAKEET_MODEL": "nvidia/parakeet-tdt-0.6b-v3",
        "NOTO_PARAKEET_BATCH": "8",
    }
    proc = subprocess.Popen(
        [sys.executable, str(REMOTE_REPO / "scripts" / "parakeet_stt_server.py")],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, env=env, text=True, bufsize=1,
    )
    assert proc.stdin is not None and proc.stdout is not None
    ready = json.loads(proc.stdout.readline())
    if not ready.get("ready"):
        raise RuntimeError(f"parakeet not ready: {ready}")
    print(f"parakeet ready: {ready}")

    out = []
    for rid, (wav, ref, accent, l1) in enumerate(items):
        proc.stdin.write(json.dumps({"id": rid, "wav": wav}) + "\n")
        proc.stdin.flush()
        resp = json.loads(proc.stdout.readline())
        out.append({"ref": ref, "hyp": resp.get("text", ""), "accent": accent, "l1": l1,
                    "error": resp.get("error", "")})
    proc.stdin.close()
    proc.wait(timeout=60)
    return {"results": out}


@app.local_entrypoint()
def main(clips: int = 80, out: str = ".modal-edacc.json"):
    res = edacc_transcribe.remote(clips)
    Path(out).write_text(json.dumps(res, indent=2))
    n = len(res.get("results", []))
    print(f"wrote {out}: {n} clips transcribed")

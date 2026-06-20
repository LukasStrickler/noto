"""parakeet WER on AMI-IHM (single-speaker close-talk headsets) — Modal STT pass.

Runs the production parakeet_stt_server.py on each individual headset channel, so
the WER is apples-to-apples with the Mix-Headset anchor — just single-speaker audio
instead of the overlapping mix. The image layer sequence mirrors modal_benchmark's
exactly (so Modal reuses the cached nemo/pyannote layers and the SAME model volume);
headsets are pulled from the AMI mirror on the Modal box (no upload). Per-headset
transcripts come back; scoring is local (score_ihm.py: min-WER vs each speaker ref).

Self-contained on purpose: Modal re-imports this module in the container, where
importing modal_benchmark (which builds its image from a local path at import time)
would fail. Run:
  modal run scripts/modal_ihm_wer.py --meetings ES2002a,ES2002b,ES2002c,ES2002d

CAVEAT (why score_ihm.py over a raw headset is misleading): AMI headsets carry
heavy CROSS-TALK BLEED — every mic picks up the loud/dominant speaker — so a full
headset transcribed end-to-end contains everyone's words, and scoring it against a
single speaker's reference yields ~85% WER (a bleed artifact, not real IHM quality;
real AMI-IHM is ~15-25%). A clean per-channel number needs per-speaker SEGMENT
extraction (transcribe only each speaker's own turns). For the product-relevant
"single-speaker WER" without that work, see benchmark/dataset/single_speaker_wer.py,
which partitions an existing anchor transcript by the reference's single-speaker vs
overlap regions (15.0% vs 58.3%) — no extra GPU.
"""
import json
import os
import subprocess
import sys
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

MIRROR = "https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/{m}/audio/{m}.Headset-{n}.wav"
HEADSETS = 4


def _ignore(path: Path) -> bool:
    """Mirror modal_benchmark.ignore_repo + drop the IHM wavs (fetched on Modal)."""
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
        Path("benchmark/dataset/ami_ihm"),
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
app = modal.App("noto-ihm-wer", image=image)
model_volume = modal.Volume.from_name(MODEL_VOLUME, create_if_missing=True, environment_name=ENVIRONMENT)


@app.function(gpu=GPU, volumes={str(MODEL_MOUNT): model_volume}, timeout=3600)
def ihm_transcribe(meetings: list[str]) -> dict:
    wavdir = Path("/tmp/ihm")
    wavdir.mkdir(parents=True, exist_ok=True)

    def grab(url, dst):
        req = urllib.request.Request(url, headers={"User-Agent": "noto-ihm"})
        with urllib.request.urlopen(req, timeout=300) as r, open(dst, "wb") as f:
            while True:
                chunk = r.read(1 << 20)
                if not chunk:
                    break
                f.write(chunk)

    paths = {}
    for m in meetings:
        ps = []
        for n in range(HEADSETS):
            dst = wavdir / f"{m}.H{n}.wav"
            if not dst.exists():
                try:
                    grab(MIRROR.format(m=m, n=n), dst)
                except Exception as e:  # noqa: BLE001
                    print(f"skip {m} H{n}: {e}")
                    continue
            ps.append(str(dst))
            print(f"  {dst.name}: {dst.stat().st_size/1e6:.1f} MB")
        paths[m] = ps
        print(f"{m}: {len(ps)} headsets downloaded")

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

    out = {}
    rid = 0
    for m, ws in paths.items():
        texts = []
        for w in ws:
            # ONE full-length headset per request — a 4-headset batch OOMs the GPU
            # (4 × ~30-min audios). Single-wav requests hit the server's per-file
            # path (batch_size 1), matching how the anchor transcribes a meeting.
            proc.stdin.write(json.dumps({"id": rid, "wav": w}) + "\n")
            proc.stdin.flush()
            resp = json.loads(proc.stdout.readline())
            if "error" in resp:
                print(f"{m} {os.path.basename(w)}: ERROR {resp['error'][:160]}")
                texts.append("")
            else:
                texts.append(resp.get("text", ""))
            rid += 1
        out[m] = texts
        print(f"{m}: transcribed {len(texts)} headsets, {sum(len(t) for t in texts)} chars")
    proc.stdin.close()
    proc.wait(timeout=60)
    return out


@app.local_entrypoint()
def main(meetings: str = "ES2002a,ES2002b,ES2002c,ES2002d", out: str = ".modal-ihm.json"):
    ms = [m.strip() for m in meetings.split(",") if m.strip()]
    res = ihm_transcribe.remote(ms)
    Path(out).write_text(json.dumps(res, indent=2))
    print(f"wrote {out}: " + ", ".join(f"{m}={len(v)}heads" for m, v in res.items()))

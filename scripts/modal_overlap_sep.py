"""Speaker SEPARATION on AMI overlap regions — does it recover the 2nd speaker?

The H10 test for "when 2 people talk, transcribe both individually." For each
2-speaker overlap region (from the reference timeline), we:
  1. slice that span out of the AMI Mix-Headset (the single mixed channel),
  2. run a 2-speaker separation model (SepFormer-WHAMR16k — trained on noisy,
     reverberant 2-speaker mixes, the closest pretrained match to AMI far-field),
  3. transcribe BOTH separated streams with the production parakeet server,
  4. also transcribe the MIXED slice once = the current single-stream baseline.
Local scoring (score_overlap_sep.py) asks: do the 2 separated transcripts recover
both reference speakers (region cpWER) better than the single mixed transcript?
If yes on a small sample, separation is worth wiring as an overlap-only preproc.

Self-contained (Modal re-imports in the container). Run:
  modal run scripts/modal_overlap_sep.py --meetings ES2002a,ES2002b --max-regions 40
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
MIRROR = "https://groups.inf.ed.ac.uk/ami/AMICorpusMirror/amicorpus/{m}/audio/{m}.Mix-Headset.wav"
SEP_MODEL = os.getenv("NOTO_SEP_MODEL", "speechbrain/sepformer-whamr16k")
SR = 16000


def _ignore(path: Path) -> bool:
    rel = path.relative_to(REPO_ROOT) if path.is_absolute() else path
    if set(rel.parts) & {".git", ".tools", ".venv", ".venv-modal", "bin", "tmp",
                         ".modal-results", ".code-review-graph"}:
        return True
    if rel.name.startswith(".env"):
        return True
    big = {Path("benchmark/identity/ami"), Path("benchmark/dataset/librispeech"),
           Path("benchmark/dataset/librispeech_wer"), Path("benchmark/dataset/synthetic"),
           Path("benchmark/dataset/synthetic_meetings"), Path("benchmark/dataset/words"),
           Path("benchmark/dataset/ami_ihm"), Path("benchmark/dataset/edacc_wer")}
    return any(rel == d or d in rel.parents for d in big)


image = (
    modal.Image.from_registry(CUDA_IMAGE, add_python="3.12")
    .apt_install("bash", "ca-certificates", "curl", "git", "ffmpeg")
    .pip_install("nemo_toolkit[asr]>=2.4", "cuda-python>=12.3")
    .pip_install("speechbrain>=1.0", "torchaudio", "soundfile")
    .add_local_dir(str(REPO_ROOT), str(REMOTE_REPO), copy=True, ignore=_ignore)
)
app = modal.App("noto-overlap-sep", image=image)
model_volume = modal.Volume.from_name(MODEL_VOLUME, create_if_missing=True, environment_name=ENVIRONMENT)


@app.function(gpu=GPU, volumes={str(MODEL_MOUNT): model_volume}, timeout=3600)
def separate_and_transcribe(jobs: dict, max_regions: int = 40, pad_sec: float = 0.0) -> dict:
    """jobs: {meeting: [{"start","end","speakers":{spk:[words]}}]} (2-spk regions)."""
    import soundfile as sf
    import torch
    import torchaudio
    from speechbrain.inference.separation import SepformerSeparation

    os.environ["HF_HOME"] = str(MODEL_MOUNT / "hf")
    wavdir = Path("/tmp/ovl")
    wavdir.mkdir(parents=True, exist_ok=True)

    def grab(url, dst):
        req = urllib.request.Request(url, headers={"User-Agent": "noto-ovl"})
        with urllib.request.urlopen(req, timeout=600) as r, open(dst, "wb") as f:
            while chunk := r.read(1 << 20):
                f.write(chunk)

    sep = SepformerSeparation.from_hparams(
        source=SEP_MODEL, savedir=str(MODEL_MOUNT / "sep" / SEP_MODEL.replace("/", "_")),
        run_opts={"device": "cuda"})

    # start the production parakeet STT server (one warm process)
    env = {**os.environ, "NOTO_PARAKEET_PROVIDER": "cuda", "NOTO_PARAKEET_PRECISION": "bf16",
           "NOTO_PARAKEET_MODEL": "nvidia/parakeet-tdt-0.6b-v3", "NOTO_PARAKEET_BATCH": "8",
           "NOTO_PARAKEET_CONFIDENCE": "1"}  # emit per-word confidence (the selector signal)
    proc = subprocess.Popen([sys.executable, str(REMOTE_REPO / "scripts" / "parakeet_stt_server.py")],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, env=env, text=True, bufsize=1)
    assert proc.stdin and proc.stdout
    ready = json.loads(proc.stdout.readline())
    if not ready.get("ready"):
        raise RuntimeError(f"parakeet not ready: {ready}")

    rid = 0

    def stt(wav_path: str):
        nonlocal rid
        proc.stdin.write(json.dumps({"id": rid, "wav": wav_path}) + "\n")
        proc.stdin.flush()
        rid += 1
        r = json.loads(proc.stdout.readline())
        return r.get("text", ""), r.get("confidences", []) or []

    out = {}
    for m, regions in jobs.items():
        src = wavdir / f"{m}.wav"
        if not src.exists():
            try:
                grab(MIRROR.format(m=m), src)
            except Exception as e:  # noqa: BLE001
                print(f"skip {m}: download {e}")
                continue
        data, sr = sf.read(str(src), dtype="float32")
        wav = torch.from_numpy(data)
        if wav.ndim > 1:
            wav = wav.mean(1)  # mono
        if sr != SR:
            wav = torchaudio.functional.resample(wav, sr, SR)
        res = []
        for i, reg in enumerate(regions[:max_regions]):
            a, b = int(reg["start"] * SR), int(reg["end"] * SR)
            clip = wav[a:b]
            if clip.numel() < SR // 2:  # <0.5s, skip
                continue
            mixp = wavdir / f"{m}_{i}_mix.wav"
            sf.write(str(mixp), clip.numpy(), SR)
            mixed_hyp, mix_confs = stt(str(mixp))
            # separate → [batch, time, n_src]. With pad_sec, separate a WIDER window
            # (surrounding single-speaker context helps the separator lock onto each
            # voice) then transcribe only the overlap [a,b] PORTION of each stream.
            try:
                pad = int(pad_sec * SR)
                sa, sb = max(0, a - pad), min(wav.shape[0], b + pad)
                sep_clip = wav[sa:sb]
                est = sep.separate_batch(sep_clip.unsqueeze(0).to("cuda"))
                est = est.squeeze(0).cpu()  # [time, n_src]
                oa, ob = a - sa, b - sa  # overlap-span offsets within the padded clip
                sep_hyps = []
                for s in range(est.shape[-1]):
                    sp = wavdir / f"{m}_{i}_s{s}.wav"
                    sf.write(str(sp), est[oa:ob, s].numpy(), SR)
                    txt, _ = stt(str(sp))
                    sep_hyps.append(txt)
            except Exception as e:  # noqa: BLE001
                print(f"{m} region {i}: separation failed {e}")
                sep_hyps = []
            mc = [float(c) for c in mix_confs]
            res.append({"start": reg["start"], "end": reg["end"], "speakers": reg["speakers"],
                        "mixed_hyp": mixed_hyp, "sep_hyps": sep_hyps,
                        "mix_conf_mean": (sum(mc) / len(mc)) if mc else None,
                        "mix_conf_min": min(mc) if mc else None})
        out[m] = res
        print(f"{m}: {len(res)} regions separated+transcribed")
    proc.stdin.close()
    proc.wait(timeout=60)
    return out


@app.local_entrypoint()
def main(meetings: str = "ES2002a,ES2002b", max_regions: int = 40, min_max_words: int = 3,
         pad_sec: float = 0.0, out: str = ".modal-overlap-sep.json"):
    # build 2-speaker overlap regions locally from the reference timeline.
    # min_max_words filters to SUBSTANTIVE overlaps: the most-talkative speaker in
    # the region said >= this many words (skip dominant + 1-word-backchannel pairs,
    # which aren't the "two people say real things at once" prize).
    sys.path.insert(0, str(REPO_ROOT / "benchmark" / "dataset"))
    import overlap_regions as ovl  # noqa: E402
    jobs = {}
    for m in [x.strip() for x in meetings.split(",") if x.strip()]:
        rf = REPO_ROOT / "benchmark" / "dataset" / "words" / f"{m}.words.json"
        if not rf.exists():
            print(f"no ref words for {m}; skipping")
            continue
        ref = json.load(open(rf))
        regs = []
        for s, e in ovl.overlap_intervals(ref):
            spk = {k: v for k, v in ovl.words_in(ref, s, e).items() if v}
            if len(spk) == 2 and max(len(v) for v in spk.values()) >= min_max_words:
                regs.append({"start": round(s, 3), "end": round(e, 3), "speakers": spk})
        if regs:
            jobs[m] = regs
            print(f"{m}: {len(regs)} substantive two-speaker overlap regions")
    res = separate_and_transcribe.remote(jobs, max_regions, pad_sec)
    Path(out).write_text(json.dumps(res, indent=2))
    print(f"wrote {out}")

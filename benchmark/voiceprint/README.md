# Voiceprint embedding-model benchmark

Reproducible benchmark behind the model choice documented in
[`.docs/benchmarks.md`](../../.docs/benchmarks.md) §1.
Run on **AMD EPYC 7702P**, CPU-only, `onnxruntime` with `intra_op_num_threads=4`.

## What it measures
- **Load time**, **per-segment latency** (3 s), **sustained throughput** (3 s windows
  over a ~5 min concatenated "meeting" clip) and **RTF** (compute ÷ audio).
- **Discrimination** (EER / AUC / mean same- vs different-speaker cosine) on a
  24-speaker × 4-clip LibriSpeech `validation.clean` trial set (144 positive,
  4416 negative pairs).

## Models (all from the **WeSpeaker** family → one shared 80-d kaldi-fbank + CMN frontend)
| HF repo | file |
|---|---|
| `Wespeaker/wespeaker-voxceleb-ecapa-tdnn512` | `voxceleb_ECAPA512.onnx` |
| `Wespeaker/wespeaker-voxceleb-resnet34-LM`   | `voxceleb_resnet34_LM.onnx` |
| `Wespeaker/wespeaker-voxceleb-resnet293-LM`  | `voxceleb_resnet293_LM.onnx` |
| `Wespeaker/wespeaker-voxceleb-campplus`      | `voxceleb_CAM++.onnx` (evaluated; export did not discriminate in-harness) |
| Resemblyzer (`pip install resemblyzer`)      | GE2E, own runtime |

Audio: `openslr/librispeech_asr` `clean/validation` pulled via the HF
datasets-server `/rows` API (see `audio_manifest.json` for the exact clips).

## Reproduce
```bash
uv venv --python 3.11 venv && source venv/bin/activate
uv pip install torch torchaudio --index-url https://download.pytorch.org/whl/cpu
uv pip install numpy onnxruntime soundfile huggingface_hub resemblyzer librosa scipy
# download the 4 onnx files into ./models, download trial clips into ./audio, then:
python bench.py     # full table + EER  -> results.json
python iso.py ecapa # clean isolated load/RAM/throughput, per model
```

## Headline result (this machine) → ship **1 default + 1 optional**
Two conditions: **clean**, and **hard** = 3 s turns + reverb (RT60 0.2 s) + babble @ 8 dB SNR
(`hard_eval.py`). The hard column is what decides curation.

| id | dim | size | RTF | EER clean | **EER hard** | decision |
|----|-----|------|-----|-----------|--------------|----------|
| `ecapa`     | 192 |  24 MB | 0.029 | 2.08 % | **7.96 %** | **default** — fastest, lightest, 192-d, best robustness/compute |
| `resnet293` | 256 | 110 MB | 0.152 | 2.08 % | **7.48 %** | **optional download** — only ~0.5 pp better at 7× cost |
| `resnet34`  | 256 |  26 MB | 0.035 | 2.07 % | **9.99 %** | drop — dominated (slower than ecapa *and* worse hard EER) |
| resemblyzer | 256 |  17 MB | 0.052 | 5.42 % | **20.4 %** | drop — far behind on both |
| campp       | 512 |  28 MB | 0.034 | 46.1 %  | —            | drop — WeSpeaker ONNX export wouldn't discriminate under any frontend variant |
| titanet     | —   | 100 MB | —     | —       | —            | drop — NeMo/torch dep; resnet293 covers the SOTA tier in pure ONNX |

Clean LibriSpeech saturates ~2 % EER so it can't separate the top models — the **hard**
condition does the real work. Key finding: ResNet34's better *clean* VoxCeleb number does
**not** survive noise + short turns (it falls behind ECAPA-512), and ResNet293's big-model
edge shrinks to ~0.5 pp. So `ecapa` is the default and `resnet293` is an opt-in. All run
**3–30× faster than real time on CPU**. (One recipe / seed / 24 speakers — a signal, not a
final EER, but the ranking was stable.)

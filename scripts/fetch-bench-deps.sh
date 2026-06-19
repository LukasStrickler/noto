#!/usr/bin/env bash
# Fetch the runtime + models the local-STT/diarization benchmarks need:
#   - sherpa-onnx prebuilt CLI (sherpa-onnx-offline) + shared libs
#   - Parakeet-TDT-0.6b-v3 int8 transducer model
#   - a static ffmpeg (decodes FLAC/m4a/video → 16 kHz mono WAV)
#
# Everything lands under $NOTO_BENCH_DEPS (default ~/.cache/noto-bench),
# idempotently (present files are skipped). Prints the env vars to export so
# `make bench-stt` / the benches can find them.
#
# Usage:  scripts/fetch-bench-deps.sh
set -euo pipefail

# Resolved BEFORE the cd into $DEPS, so the fp16 converter is found by abs path.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DEPS="${NOTO_BENCH_DEPS:-$HOME/.cache/noto-bench}"
SHERPA_VER="1.13.2"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m | sed -e 's/x86_64/x64/' -e 's/aarch64/aarch64/')"
ACCELERATOR="${BENCH_ACCELERATOR:-${NOTO_COMPUTE:-cpu}}"
SHERPA_PROVIDER="cpu"

if [ "$OS" != "linux" ]; then
  echo "NOTE: this fetcher targets linux-$ARCH. On macOS the production path is" >&2
  echo "      FluidAudio/CoreML; sherpa-onnx macOS assets exist but aren't wired here." >&2
fi

mkdir -p "$DEPS"
cd "$DEPS"

SHERPA_ASSET="sherpa-onnx-v${SHERPA_VER}-${OS}-${ARCH}-shared-no-tts.tar.bz2"
SHERPA_DIR="$DEPS/${SHERPA_ASSET%.tar.bz2}"
case "$ACCELERATOR" in
  cuda|gpu|modal)
    SHERPA_PROVIDER="cuda"
    if [ "$OS" != "linux" ] || [ "$ARCH" != "x64" ]; then
      echo "ERROR: sherpa CUDA benchmark runtime is only wired for linux-x64, got $OS-$ARCH" >&2
      exit 1
    fi
    SHERPA_ASSET="sherpa-onnx-v${SHERPA_VER}-cuda-12.x-cudnn-9.x-linux-x64-gpu.tar.bz2"
    SHERPA_DIR="$DEPS/${SHERPA_ASSET%.tar.bz2}"
    ;;
esac
MODEL_DIR="$DEPS/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8"

fetch() { # url dest
  if [ -s "$2" ]; then echo "  present: $(basename "$2")"; return; fi
  echo "  downloading $(basename "$2") …"
  curl -fsSL --retry 3 -o "$2" "$1"
}

echo "==> sherpa-onnx CLI ($SHERPA_VER, $OS-$ARCH, accelerator=$ACCELERATOR)"
if [ ! -x "$SHERPA_DIR/bin/sherpa-onnx-offline" ]; then
  fetch "https://github.com/k2-fsa/sherpa-onnx/releases/download/v${SHERPA_VER}/${SHERPA_ASSET}" "$SHERPA_ASSET"
  tar xjf "$SHERPA_ASSET"
fi

echo "==> Parakeet-TDT-0.6b-v3 int8 model"
if [ ! -s "$MODEL_DIR/encoder.int8.onnx" ]; then
  fetch "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-int8.tar.bz2" parakeet-v3.tar.bz2
  tar xjf parakeet-v3.tar.bz2
fi

# fp32 export (GPU experiment): int8 is a CPU optimization — on CUDA the
# dequantize overhead can make it SLOWER than full precision. Fetched only when
# requested (≈2.5 GB, one-time per volume). Also the SOURCE for the fp16 export,
# so fp16 pulls it in too (no fp16 model is published upstream — the csukuangfj
# fp16 HF repo is an empty stub as of 2026-06; we make our own, below).
FP32_DIR="$DEPS/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3"
PREC="${BENCH_STT_PRECISION:-int8}"
if [ "$PREC" = "fp32" ] || [ "$PREC" = "fp16" ]; then
  echo "==> Parakeet-TDT-0.6b-v3 fp32 model (GPU precision experiment / fp16 source)"
  mkdir -p "$FP32_DIR"
  HF_BASE="https://huggingface.co/csukuangfj/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3/resolve/main"
  for f in encoder.onnx encoder.weights decoder.onnx joiner.onnx tokens.txt; do
    fetch "$HF_BASE/$f" "$FP32_DIR/$f"
  done
fi

# fp16 export: the real GPU throughput lever (CUDA EP has native fp16 kernels,
# unlike int8). Converted from the fp32 model above, cached on the volume so the
# (one-time) conversion is paid once. Idempotent: skips when already present.
FP16_DIR="$DEPS/sherpa-onnx-nemo-parakeet-tdt-0.6b-v3-fp16"
if [ "$PREC" = "fp16" ]; then
  echo "==> Parakeet-TDT-0.6b-v3 fp16 model (converted from fp32)"
  # The conversion RECIPE (which roles run fp16, which ops stay fp32) is part of
  # the artifact's identity: a volume-cached dir built with a different recipe
  # (e.g. the old full-fp16 export whose autoregressive decode derails ES2002a)
  # must be rebuilt, not reused. The marker records the recipe that built the dir.
  FP16_ROLES="${PARAKEET_FP16_ROLES:-encoder}"
  FP16_BLOCK="${PARAKEET_FP16_BLOCK:-LayerNormalization,Softmax}"
  FP16_MARK="$FP16_DIR/conversion.marker"
  WANT_MARK="roles=$FP16_ROLES block=$FP16_BLOCK"
  if [ -s "$FP16_DIR/encoder.fp16.onnx" ] && [ -s "$FP16_MARK" ] && [ "$(cat "$FP16_MARK")" = "$WANT_MARK" ]; then
    echo "  present: $FP16_DIR/encoder.fp16.onnx ($WANT_MARK)"
  else
    [ -d "$FP16_DIR" ] && { echo "  recipe changed → rebuilding ($WANT_MARK)"; rm -rf "$FP16_DIR"; }
    PARAKEET_FP16_ROLES="$FP16_ROLES" PARAKEET_FP16_BLOCK="$FP16_BLOCK" \
      python3 "$SCRIPT_DIR/convert_parakeet_fp16.py" "$FP32_DIR" "$FP16_DIR"
    printf '%s' "$WANT_MARK" > "$FP16_MARK"
  fi
fi

echo "==> static ffmpeg"
if [ ! -x "$DEPS/ffmpeg" ]; then
  fetch "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz" ffmpeg.tar.xz
  tar xJf ffmpeg.tar.xz
  cp "$(find . -maxdepth 2 -name ffmpeg -type f | head -1)" "$DEPS/ffmpeg"
fi

echo "==> diarization models (pyannote segmentation + CAM++ en embedding)"
if [ ! -s "$DEPS/sherpa-onnx-pyannote-segmentation-3-0/model.onnx" ]; then
  fetch "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-segmentation-models/sherpa-onnx-pyannote-segmentation-3-0.tar.bz2" seg.tar.bz2
  tar xjf seg.tar.bz2
fi
# Multilingual (zh+en common) speaker embedding. NOT the en-only VoxCeleb CAM++,
# which collapses clusters; the common-data model separates speakers in English
# AND other languages (verified en + zh), so diarization isn't English-overfit.
if [ ! -s "$DEPS/diar-embedding.onnx" ]; then
  fetch "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_zh_en_16k-common_advanced.onnx" diar-embedding.onnx
fi

cat <<EOF

bench deps ready under $DEPS

Export these (or use 'make bench-stt' which sets them):
  export BENCH_STT_ENGINE=sherpa-parakeet
  export BENCH_STT_MODELS="$MODEL_DIR"
  export NOTO_SHERPA_BIN="$SHERPA_DIR/bin/sherpa-onnx-offline"
  export NOTO_SHERPA_LIB="$SHERPA_DIR/lib"
  export NOTO_SHERPA_PROVIDER="${SHERPA_PROVIDER}"
  export NOTO_FFMPEG="$DEPS/ffmpeg"
  export NOTO_SHERPA_THREADS=\$(nproc)
  # diarization:
  export BENCH_DIAR_ENGINE=sherpa-pyannote
  export NOTO_SHERPA_SEG_PROVIDER="${SHERPA_PROVIDER}"
  export NOTO_SHERPA_EMB_PROVIDER="${SHERPA_PROVIDER}"
  export NOTO_SHERPA_SEG_MODEL="$DEPS/sherpa-onnx-pyannote-segmentation-3-0/model.onnx"
  export NOTO_SHERPA_EMB_MODEL="$DEPS/diar-embedding.onnx"
EOF

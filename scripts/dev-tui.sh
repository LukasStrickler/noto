#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

export NOTO_CONFIG_DIR="${NOTO_CONFIG_DIR:-$PWD/.dev/noto-config}"
export NOTO_ARTIFACT_ROOT="${NOTO_ARTIFACT_ROOT:-$PWD/.dev/noto-artifacts}"

mkdir -p "$NOTO_CONFIG_DIR" "$NOTO_ARTIFACT_ROOT"

exec go run ./cmd/noto tui

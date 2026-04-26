#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

bin_dir="${NOTO_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$bin_dir"

go build -o "$bin_dir/noto" ./cmd/noto

printf 'Installed noto to %s\n' "$bin_dir/noto"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) printf 'Warning: %s is not on PATH for this shell.\n' "$bin_dir" >&2 ;;
esac

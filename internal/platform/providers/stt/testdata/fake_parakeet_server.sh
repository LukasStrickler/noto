#!/bin/sh
# Fake parakeet_stt_server.py for unit tests: speaks the same JSONL protocol
# without python/sherpa-onnx. Emits the ready handshake, then answers every
# request with a canned two-word result (echoing the request id). $FAKE_PARAKEET_MODE
# steers failure modes: "error" → an error response; "crash" → exit mid-request
# (exercises the client's restart-and-retry path).
printf '%s\n' '{"ready":true,"provider":"fake"}'
while IFS= read -r line; do
  id=${line#*\"id\":}
  id=${id%%[,\}]*}
  case "${FAKE_PARAKEET_MODE:-normal}" in
  error)
    printf '{"id":%s,"error":"boom"}\n' "$id"
    ;;
  crash)
    exit 1
    ;;
  *)
    # A noise line first: the client must skip non-protocol output.
    printf '%s\n' 'not json noise'
    case "$line" in
    *'"wavs"'*)
      printf '{"id":%s,"results":[{"text":"hello world","tokens":[" hello"," world"],"timestamps":[0.0,0.5],"durations":[0.4,0.4]},{"text":"hello world","tokens":[" hello"," world"],"timestamps":[0.0,0.5],"durations":[0.4,0.4]}]}\n' "$id"
      ;;
    *)
      printf '{"id":%s,"text":"hello world","tokens":[" hello"," world"],"timestamps":[0.0,0.5],"durations":[0.4,0.4]}\n' "$id"
      ;;
    esac
    ;;
  esac
done

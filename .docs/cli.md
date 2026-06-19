# CLI Reference

## How the CLI connects

Every `noto` command uses the same `notoapi.Client` interface, so all commands
work identically against an in-process server, a local daemon, or a remote backend.

**Discovery order** (checked on every command):

1. If `NOTO_API_URL` is set → HTTP to that URL (add `NOTO_API_TOKEN` for auth)
2. If the local UDS socket exists and responds → HTTP over UDS to local daemon
3. Otherwise → start an in-process server, run the command, shut it down

```bash
# In-process (default — no server needed)
noto list

# Point at a remote backend
export NOTO_API_URL=http://192.168.1.10:8731
export NOTO_API_TOKEN=abc123
noto list

# Or keep a local daemon alive for faster repeated commands
noto serve &
noto list        # auto-connects to the daemon
noto search "roadmap"
```

## Error shape

Commands with `--json` write JSON to stdout. Errors go to stderr in this shape:

```json
{
  "error": {
    "code": "not_found",
    "message": "meeting not found",
    "details": { "id": "abc-123" }
  }
}
```

## Command Reference

### TUI and server

```
noto                             Open TUI (in-process server if needed)
noto tui                         Same as noto
noto serve                       Run backend daemon on default UDS
noto serve --listen tcp:0.0.0.0:8731 --token-file /tmp/tok
                                 Remote-accessible TCP listener
noto dev                         TUI with debug paths printed, pre-warms capture helper
noto dev serve                   Backend-only dev mode (no TUI)
```

### Meeting browsing

```
noto list [--json] [--limit N]
noto show <meeting_id> [--json]
noto transcript <meeting_id> [--json]
noto summary <meeting_id> [--json]
noto files <meeting_id>                  On-disk artifact paths (JSON)
noto agent <meeting_id>                  Agent handoff: paths + CLI commands (JSON)
noto search <query> [--json]
```

### Recording and import

```
noto record [--title "Roadmap sync"]
noto stop
noto import-audio <path> [--title "..."] [--wait] [--json]
```

`--wait` blocks until the pipeline (ingest → transcribe → summarize → index) finishes.
Without `--wait` the command returns immediately with the job ID.

### Provider management

```
noto providers list [--json]
noto providers key-set <provider> <value>
noto providers key-remove <provider>
noto providers test <provider>
noto providers active-speech <provider>
noto providers active-llm <model-id>
```

Known provider IDs: `assemblyai`, `openrouter`.

### Speaker model

```
noto speaker-model status         Show whether the ECAPA embedder is installed
noto speaker-model download       Install model + ONNX runtime + ffmpeg into the data dir
```

The in-process ECAPA model enables cross-meeting speaker identity. It is picked up lazily
on the next transcribe, so no restart is needed after a download.

### Jobs and diagnostics

```
noto jobs [--json]
noto status                       Health + active recording + recent jobs (JSON)
noto ping                         Health check (JSON)
noto verify                       Enqueue a checksum-verify job for all meetings
```

### Dev / seed

```
noto seed                         Insert fixture meetings into local storage (dev only)
```

Refuses to run against a remote `NOTO_API_URL`.

## Environment Variables

| Variable | Purpose |
| --- | --- |
| `NOTO_API_URL` | Connect to this backend instead of starting in-process |
| `NOTO_API_TOKEN` | Bearer token for `NOTO_API_URL` (TCP mode) |
| `NOTO_CONFIG_DIR` | Override default config directory |
| `NOTO_ARTIFACT_ROOT` | Override default recordings root |
| `NOTO_ASSEMBLYAI_KEY` | AssemblyAI API key (fallback if not in keychain) |
| `NOTO_OPENROUTER_KEY` | OpenRouter API key (fallback if not in keychain) |
| `NOTO_SPEAKER_EMBEDDING_URL` | Remote embedding service URL — overrides the in-process ECAPA model (optional) |

## Remote backend setup

```bash
# On the server
noto serve --listen tcp:0.0.0.0:8731 --token-file /etc/noto/token

# On the client
ssh -L 8731:localhost:8731 yourserver   # port-forward (recommended)
export NOTO_API_URL=http://localhost:8731
export NOTO_API_TOKEN=$(cat /etc/noto/token)
noto tui
```

The TUI works identically against a remote backend. All processing (transcription,
summarization, search) runs on the server. The TUI only renders and sends commands.

## Providers

AssemblyAI is the production STT provider; OpenRouter provides LLM summarization; an
in-process ECAPA model provides speaker embeddings. Each falls back to deterministic synthetic
output when unconfigured, so the full pipeline (ingest → transcribe → summarize → index) works in
development and on Linux without any keys.

| Provider | Configure | Env var (fallback) |
| --- | --- | --- |
| AssemblyAI STT | `noto providers key-set assemblyai <key>` | `NOTO_ASSEMBLYAI_KEY` |
| OpenRouter LLM | `noto providers key-set openrouter <key>` | `NOTO_OPENROUTER_KEY` |
| Active LLM model | `noto providers active-llm <model>` | `NOTO_LLM_MODEL` |
| Speaker embedding | `noto speaker-model download` (in-process default) | `NOTO_SPEAKER_EMBEDDING_URL` (remote override) |

Keys can also be set in the TUI config screen (`,`). Without an AssemblyAI key the pipeline
synthesizes a short placeholder transcript; without an OpenRouter key, a deterministic placeholder
summary; with no speaker model installed, speaker mappings stay `"unmatched"` — never silently
wrong. See [speaker-identity.md](./speaker-identity.md) for the embedder and
[artifacts.md](./artifacts.md) for the normalized transcript/summary schemas all providers emit.

**Remote embedding override.** For a split deployment, point the backend at a remote HTTP
embedding service instead of the in-process model:

```
export NOTO_SPEAKER_EMBEDDING_URL=http://embedding-host:8080
POST {url}/v1/speaker-embeddings  { meeting_id, audio_base64, speakers[], segments[] }
                                  → { "model": "ecapa", "embeddings": { "A": [...] } }
```

**Adding a provider.** Implement `stt.STTProvider` (STT) or `llm.SummaryProvider` (LLM) under
`internal/platform/providers/`, register it in the registry, and add schema tests for the
normalized output.

## Related

- [Architecture](architecture.md) — connection modes, deployment
- [Agent interface](./agent-interface.md) — JSON command contracts for agents
- [Artifact schemas](./artifacts.md) — transcript / summary JSON shapes

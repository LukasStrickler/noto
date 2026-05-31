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
| `NOTO_SPEAKER_EMBEDDING_URL` | Speaker embedding service URL (optional) |

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

## Related

- [Architecture](../architecture/cloud-architecture.md) — connection modes, deployment
- [Agent interface](./agent-interface.md) — JSON command contracts for agents
- [Providers](./providers.md) — configuring AssemblyAI and OpenRouter

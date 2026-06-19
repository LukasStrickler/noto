# Noto documentation

Internal reference for the noto backend, TUI, and agent interface. The top-level
[README](../README.md) is the product overview; this tree is the detail.

```bash
noto                 # TUI (in-process server, auto-started)
noto serve &         # or run a local daemon for faster CLI calls
noto list --json
# remote backend:  export NOTO_API_URL=…  NOTO_API_TOKEN=…  ;  noto tui
```

| Doc | What's in it |
| --- | --- |
| [architecture.md](./architecture.md) | System design, tech stack, deployment modes, package map, storage seam |
| [pipeline.md](./pipeline.md) | Audio → transcript → summary/insights pipeline, privacy posture, gaps & risks |
| [speech-compute.md](./speech-compute.md) | Speech compute architecture, GPU benchmark loop, quality gates, and experiment process |
| [speaker-identity.md](./speaker-identity.md) | Cross-meeting voice identity — design as built |
| [benchmarks.md](./benchmarks.md) | Speaker-recognition results, failure modes, and dataset provenance |
| [cli.md](./cli.md) | CLI commands, env vars, remote setup, and provider configuration |
| [agent-interface.md](./agent-interface.md) | HTTP/SSE + JSON contract for agents |
| [artifacts.md](./artifacts.md) | Normalized artifact schemas (transcript, summary, …) |
| [tui.md](./tui.md) | TUI screens and keybindings |
| [decisions/](./decisions/) | Architecture decision records (ADRs) |

For code structure and contributor conventions (package map, testing patterns), see
[AGENTS.md](../AGENTS.md) and [CLAUDE.md](../CLAUDE.md).

## Design principles

- **Backend is source of truth.** `noto serve` owns all storage and processing; the TUI holds
  no local copy — it reads via `notoapi.Client`.
- **Three deployment modes, one client.** In-process (default), local daemon (Unix socket), or
  remote TCP — identical code path.
- **Storage seam enforced.** The `ArtifactRepository` interface (`internal/platform/repo`)
  isolates the filesystem from service logic; a Postgres/S3 backend is one package change.
- **AssemblyAI for production STT;** deterministic dry-run fallback without keys.
- **Speaker profiles are first-class.** Cross-meeting voice identity via an in-process ECAPA
  embedder (`noto speaker-model download`) and the backend matching pipeline.

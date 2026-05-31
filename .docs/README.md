# Noto Documentation

## Quick Start

```bash
# Run the TUI (starts an in-process server automatically)
noto

# Or keep a local server running for faster CLI commands
noto serve &
noto list --json

# Or connect to a remote server
export NOTO_API_URL=http://192.168.1.10:8731
export NOTO_API_TOKEN=<token>
noto tui
```

## Start Here

| I want to… | Read |
| --- | --- |
| Understand the architecture | [architecture/cloud-architecture.md](./architecture/cloud-architecture.md) |
| Know what's built and what's left | [guides/build-plan.md](./guides/build-plan.md) |
| Use the TUI | [reference/tui.md](./reference/tui.md) |
| Use the CLI | [reference/cli.md](./reference/cli.md) |
| Let an agent query meeting data | [reference/agent-interface.md](./reference/agent-interface.md) |
| Configure AssemblyAI / OpenRouter | [reference/providers.md](./reference/providers.md) |
| Write tests | [reference/testing.md](./reference/testing.md) |

## Reference Map

| Topic | File |
| --- | --- |
| Architecture + deployment modes | [architecture/cloud-architecture.md](./architecture/cloud-architecture.md) |
| Architecture (package map) | [reference/architecture.md](./reference/architecture.md) |
| Build plan + status | [guides/build-plan.md](./guides/build-plan.md) |
| TUI screens and keys | [reference/tui.md](./reference/tui.md) |
| CLI commands and env vars | [reference/cli.md](./reference/cli.md) |
| Agent interface (HTTP + CLI) | [reference/agent-interface.md](./reference/agent-interface.md) |
| Providers (AssemblyAI, OpenRouter) | [reference/providers.md](./reference/providers.md) |
| Artifact schemas | [reference/artifacts.md](./reference/artifacts.md) |
| Testing patterns | [reference/testing.md](./reference/testing.md) |
| Tech stack | [reference/tech-stack.md](./reference/tech-stack.md) |
| Product scope | [reference/product.md](./reference/product.md) |
| Human and agent features | [reference/features.md](./reference/features.md) |
| User stories | [reference/user-stories.md](./reference/user-stories.md) |

## Design Principles

- **Backend is source of truth.** `noto serve` owns all storage and processing.
  The TUI holds no local copy — it reads via `notoapi.Client`.
- **Three deployment modes, one client.** In-process (default), local daemon, or
  remote TCP. The TUI and CLI code is identical in all three.
- **Storage seam enforced.** `ArtifactRepository` interface isolates the filesystem
  from service logic. Future Postgres/S3 backend requires only one package change.
- **AssemblyAI for production STT.** Dry-run fallback for development without keys.
- **Speaker profiles are first-class.** Persistent cross-meeting voice identity via
  the backend embedding/matching pipeline. Configure `NOTO_SPEAKER_EMBEDDING_URL`.

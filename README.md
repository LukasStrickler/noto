# Noto

Terminal-first meeting recorder, transcriber, summarizer, and searchable memory.

Noto's main interface is the `noto` TUI you open in the terminal. It connects to a
remote `noto serve` backend that owns data storage, transcription via AssemblyAI,
LLM summarization, and a search index. The TUI is a local client; all meeting data
lives on the remote backend.

## Status

Implementation alpha. The architecture is:

```
Local TUI/CLI  ←→  Remote noto serve  ←→  AssemblyAI (transcription + diarization)
                       ↑
                       └── SQLite + filesystem artifacts (backend storage)
```

`noto serve` can run as a local daemon (same machine as TUI) or a remote server.
`noto record` captures audio via a macOS native helper; `noto serve` orchestrates
transcription, summarization, and indexing.

## Quickstart

```bash
# from a checked-out repo
make dev           # opens the TUI against local noto serve
```

If Go isn't installed system-wide, `make dev` downloads a local toolchain into
`./.tools/go/` on first run — see `scripts/go`. Active documentation lives in
[.docs](./.docs/README.md).

### Provider keys

Open the TUI, press `,` (or `4`) to land on the **config** screen. The left
pane shows what's active and where artifacts live. The right pane lists
providers — pick one, press `e`, paste the key. Keys persist via macOS
Keychain on darwin and `~/.noto/credentials.json` (0600) elsewhere. `a` on a
highlighted speech provider makes it the active route.

The default STT provider is AssemblyAI with speaker diarization enabled.

## Command Shape

```text
noto tui                  # open the TUI
noto serve                # start the backend daemon (local or remote)
noto record --title "Roadmap sync"
noto stop
noto import-audio ./roadmap-sync.m4a --title "Roadmap sync"
noto search --json "pricing decision"
noto verify --json
noto show <meeting_id>
noto transcript --json <meeting_id>
noto play <meeting_id> [--speed <rate>]
```

## Architecture

```mermaid
flowchart TD
    classDef tuiNode fill:#ede9fe,stroke:#7c3aed,stroke-width:2px,color:#111827
    classDef serverNode fill:#e0f2fe,stroke:#0284c7,stroke-width:2px,color:#111827
    classDef storageNode fill:#ffffff,stroke:#4b5563,stroke-width:1px,color:#111827

    subgraph Local["Local client"]
        TUI["noto TUI/CLI"]:::tuiNode
    end

    subgraph Remote["Remote backend (noto serve)"]
        API["HTTP API server"]:::serverNode
        JOBS["Job orchestrator"]:::serverNode
        DB[(SQLite + artifacts)]:::storageNode
    end

    subgraph Providers["External providers"]
        ASSEMBLY["AssemblyAI"]:::serverNode
    end

    TUI -->|HTTP/SSE| API
    API --> JOBS
    JOBS -->|transcribe| ASSEMBLY
    ASSEMBLY -->|transcript| JOBS
    JOBS --> DB
    API -->|browse/search| DB

    linkStyle 0 stroke:#7c3aed,stroke-width:2px
    linkStyle 1 stroke:#0284c7,stroke-width:2px
    linkStyle 2 stroke:#0284c7,stroke-width:2px
    linkStyle 3 stroke:#0284c7,stroke-width:2px
    linkStyle 4 stroke:#0284c7,stroke-width:2px
    linkStyle 5 stroke:#4b5563,stroke-width:2px
    linkStyle 6 stroke:#7c3aed,stroke-width:2px
```

The backend owns storage and API. The TUI is a thin local client that consumes
the remote API. Speaker profiles are a first-class feature: AssemblyAI provides
per-meeting diarization labels; persistent cross-meeting identity requires the
backend speaker profiles + embedding/matching pipeline.

## Scope

| Focus | Notes |
| --- | --- |
| Local TUI/CLI | Main interface, local process |
| Remote backend | Data source of truth, API server |
| AssemblyAI STT | Transcription + diarization |
| Speaker profiles | Cross-meeting identity via backend pipeline |
| SQLite + filesystem | Artifact storage (backend) |
| Future storage seam | Interface exists; Postgres/object storage is a later swap |

## Documentation

- [Documentation index](./.docs/README.md)
- [Cloud architecture](./.docs/architecture/cloud-architecture.md)
- [API and agent access](./.docs/reference/agent-interface.md)
- [TUI reference](./.docs/reference/tui.md)
- [Build plan](./.docs/guides/build-plan.md)

## License

Source-available under the PolyForm Noncommercial License 1.0.0.

Commercial use requires a paid license. Commercial use includes use by
companies, employees, contractors, freelancers using Noto for client work, or
teams using it for internal business meetings, operations, documentation, or
agent workflows.

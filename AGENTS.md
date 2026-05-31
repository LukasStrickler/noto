# NOTO — PROJECT KNOWLEDGE BASE

**Generated:** 2026-05-20
**Type:** Terminal-first meeting recorder (Go, Bubble Tea TUI)
**Core Stack:** Go 1.25 + Bubble Tea TUI + remote `noto serve` backend + SQLite FTS5 + AssemblyAI

---

## WHAT THIS PROJECT IS

Noto records meetings via a macOS native capture helper, sends recordings to a `noto serve` backend, transcribes audio with AssemblyAI, generates AI summaries through OpenRouter-compatible LLMs, indexes everything with SQLite FTS5, and exposes a local TUI/CLI plus HTTP/SSE API for browsing, search, and agent access.

**Two binaries:**
- `cmd/noto` — main entry: TUI + all CLI commands + in-process server
- `cmd/capture` — macOS audio capture helper (Swift? communicates via appsocket Unix socket)

**Primary modes:**
1. `noto` / `noto tui` — local terminal UI client
2. `noto serve` — backend daemon (Unix socket for local development, TCP with bearer token for remote use)
3. Direct CLI commands (`list`, `search`, `show`, `record`, `import-audio`, etc.) routed through `notoapi.Client`

---

## STRUCTURE

Packages are grouped by architectural **role**, following the dependency
flow (leaves → infra → app → delivery):

```
noto/
├── cmd/
│   ├── noto/          # main CLI entry point
│   └── capture/       # macOS audio capture helper (Go wrapper → Swift)
├── internal/
│   ├── core/                  # domain types + pure logic (leaves, no infra deps)
│   │   ├── artifacts/         # artifact types: meeting, audio, transcript, summary
│   │   ├── speakers/          # speaker-matching domain logic
│   │   └── notoerr/           # structured errors
│   ├── platform/              # infrastructure adapters (disk, db, network, AI)
│   │   ├── config/            # viper-based config (dirs, providers, models)
│   │   ├── secrets/           # Keychain (darwin) / file (linux) credential store
│   │   ├── db/                # single SQLite connection opener + Migrate
│   │   ├── storage/           # file-based artifact persistence (used only by repo)
│   │   ├── repo/              # ArtifactRepository iface + LocalArtifactRepository
│   │   ├── search/            # SQLite FTS5 index + query parser
│   │   ├── speakerstore/      # speaker profile + meeting-mapping repos (SQLite)
│   │   └── providers/         # STT + LLM provider registry + routers
│   │       ├── stt/           # AssemblyAI production STT adapter
│   │       ├── speech/        # transcript normalization
│   │       └── llm/           # OpenRouter LLM adapter
│   │           └── prompts/   # versioned LLM prompt templates (few-shot + CoT)
│   ├── app/                   # application core + composition root
│   │   ├── service/           # CORE ORCHESTRATOR — wires everything together
│   │   └── host/              # in-process server host (spawns service + server)
│   ├── transport/             # delivery boundaries
│   │   ├── notoapi/           # API types (requests, responses, client interface)
│   │   ├── server/            # HTTP server (routing, middleware, auth)
│   │   ├── apiclient/         # HTTP client to talk to noto server/daemon
│   │   └── appsocket/         # IPC client ↔ capture helper Unix socket
│   ├── ui/                    # user interfaces
│   │   ├── tui/               # Bubble Tea UI (screens, keys, theme, layout)
│   │   └── cli/               # all CLI command handlers (notoapi.Client callers)
│   └── testutil/              # FakeRepo + test helpers (not a layer)
├── scripts/           # go toolchain wrapper (downloads into .tools/go/)
├── bin/               # built binaries (gitignored)
└── .tools/            # local Go toolchain (gitignored)
```

Each role group (`core`, `platform`, `app`, `transport`, `ui`) has its own
`AGENTS.md` stating that layer's dependency rule, packages, and local
anti-pattern — read the one for the subtree you're editing. The two densest
packages, `app/service/` and `ui/tui/`, have a deeper `AGENTS.md` with a
per-file map and local conventions.

---

## KEY ARCHITECTURAL PATTERNS

### Service is the Hub

`internal/app/service/service.go` — every HTTP handler and every CLI command funnels through `Service`. It owns:
- Recording state (mutex-protected)
- Job lifecycle (jobCancels map)
- Composes: storage, search, registry, secrets, IPC client, event hub

### Host — The Connection Owner

`internal/app/host/` — decides whether to connect to a remote backend (`NOTO_API_URL` + `NOTO_API_TOKEN`), use a local daemon, or start an in-process development host. All `noto` commands go through this. Never import storage/search directly from CLI.

### Provider Registry

`internal/platform/providers/registry.go` — pluggable provider registry. Production STT is AssemblyAI only for now; summaries use OpenRouter-compatible LLMs. Providers implement interfaces in `types.go`.

### Artifacts as Structured Types

`internal/core/artifacts/` — typed artifacts (meeting, audio, transcript, summary) each implement `Artifact` interface with `Kind()`, `Version()`, `Validate()`. Artifacts are the universal exchange type between storage, search, and providers.

### Jobs Pipeline

Recording/import → job queued → AssemblyAI transcription/diarization → speaker profile mapping → summarize → index → artifacts written → FTS updated. Jobs are stored in SQLite (`noto-jobs.sqlite`), cancellable via `context.CancelFunc` map.

### appsocket IPC

`internal/transport/appsocket/` — `IPCClient` connects to the capture helper via Unix domain socket. `cmd/capture` is a Go binary that wraps the macOS Swift helper. The Swift side does the actual audio capture.

### Event Hub for SSE

`internal/app/service/events.go` — in-process `eventHub` for broadcasting job progress to SSE handlers and the TUI.

---

## WHERE TO LOOK

| Need | Location |
|------|----------|
| How a CLI command works | `internal/ui/cli/cli.go` (all `run*` methods) |
| How TUI screens are structured | `internal/ui/tui/screen*.go`, `root.go` |
| How recording is triggered | `internal/app/service/recording.go` |
| How STT is integrated | `internal/platform/providers/stt/provider.go` and `internal/platform/providers/stt/assemblyai.go` |
| How artifacts are written/read | `internal/core/artifacts/artifact.go`, `internal/app/service/storage.go` |
| How search index works | `internal/platform/search/search.go` |
| How FTS query parsing works | Read `internal/platform/search/` carefully |
| How jobs are queued/executed | `internal/app/service/jobs.go` |
| How providers are configured | `internal/app/service/providers.go`, `internal/platform/config/` |
| How capture helper IPC works | `internal/transport/appsocket/`, `cmd/capture/main.go` |
| How secrets are stored | `internal/platform/secrets/` |
| How LLM prompts are built | `internal/platform/providers/llm/prompts/`, `internal/platform/providers/llm/` |

---

## CONVENTIONS (THIS PROJECT)

- **go.mod**: module `github.com/lukasstrickler/noto`, Go 1.25 (toolchain 1.26.3)
- **Error handling**: `notoerr.Error` type (structured, typed errors); never bare `errors.New`
- **Config**: viper-based; defaults in `internal/platform/config/defaults.go`
- **Secrets**: macOS Keychain on darwin, `~/.noto/credentials.json` elsewhere
- **Artifacts**: stored in `ConfigDir/recordings/<meeting_id>/` as JSON + companion Markdown
- **SQLite**: two DBs — `noto.sqlite` (meetings index) and `noto-jobs.sqlite` (job queue)
- **No `pkg/`**: all shared code is `internal/` (standard Go layout)
- **Local Go toolchain**: `scripts/go` auto-downloads into `.tools/go/` if no Go in PATH
- **Tests**: `go test ./...` — integration tests tagged `-tags=integration`

---

## ANTI-PATTERNS (THIS PROJECT)

- **NEVER** import `storage` or `search` directly from CLI packages — go through `Service` via `notoapi.Client`
- **NEVER** use bare `errors.New` — use `notoerr.*` types
- **NEVER** hardcode paths — use `config.Config` struct (respects `NOTO_*` env vars)
- **NEVER** block the Service goroutine — all long work runs in background job goroutines
- **NEVER** log to stdout/stderr in library code — return errors, let caller decide

---

## UNIQUE STYLES

- `make dev` boots TUI from source + pre-warms capture helper
- `make seed` injects 3 fixture meetings (uses fixed UUIDs, idempotent)
- `go test ./...` for package validation; external-provider tests require explicit credentials
- TCP daemon mode uses random bearer token (written to `--token-file`)
- Provider keys configurable via TUI config screen or `noto providers key-set`
- Remote connections use `NOTO_API_URL` and `NOTO_API_TOKEN`

---

## COMMANDS

```bash
make dev              # TUI from source (fastest iteration)
make serve           # daemon mode (unix socket)
make build           # produce ./bin/noto
make test            # run all tests
make test-e2e        # only E2E pipeline test
make seed            # populate 3 fixture meetings
make vet             # go vet ./...
make lint            # vet + checks
make clean          # rm ./bin/

# Direct CLI
noto tui             # open TUI
noto list [--json]   # list meetings
noto search <q>      # search transcripts
noto show <id>       # show meeting details
noto record          # start recording
noto stop            # stop recording
noto import-audio <path> [--title "..."] [--wait]
noto jobs [--json]   # list job queue
noto providers list  # show configured providers
noto providers list  # show configured providers
```

---

## NOTES

- `cmd/capture` is a **Go binary** that wraps a macOS native audio capture mechanism (likely Swift-based screen/audio capture)
- The Swift capture helper is NOT in this repo — it's a separate macOS app that communicates via appsocket
- `noto dev` pre-warms the capture helper (5s timeout, best-effort)
- FTS5 search ranks by recency + BM25-like scoring
- Artifact checksum tracks integrity of stored JSON vs generated Markdown
- Active architecture: local TUI/CLI + remote-capable backend + AssemblyAI-only production transcription

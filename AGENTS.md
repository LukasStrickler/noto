# noto — repo knowledge base

Terminal-first meeting recorder, transcriber, summarizer, and searchable memory
(Go 1.25 · Bubble Tea v2 TUI · SQLite FTS5 · local Parakeet STT + pyannote diarization · OpenRouter LLM).
This is the big-picture entry point; each `internal/` role group has its own
`AGENTS.md` with the layer rule and per-file map. For the product overview see
[README.md](README.md); for TUI key/layout/mouse conventions see [CLAUDE.md](CLAUDE.md).

## What this project is

noto records or imports meeting audio, transcribes it with a local Parakeet
(NeMo TDT) recognizer and diarizes it in a separate pyannote stage — both run
in-process or offload to a remote GPU / Modal compute node over the same HTTP
contract — generates a cited summary via an OpenRouter-compatible LLM, links the same voice
across meetings with an in-process CPU-only speaker embedder, and indexes
everything in SQLite FTS5 — all behind one backend that a keyboard-first TUI, a
`--json` CLI, and an HTTP/SSE API all read through.

**Two binaries:**
- `cmd/noto` — main entry: TUI + all CLI commands + in-process server
- `cmd/capture` — macOS audio capture helper; a Go wrapper (`main.go`) around a
  Swift ScreenCaptureKit binary (`main.swift`, in this repo), talking to the
  backend over the `appsocket` Unix socket

**Three deployment modes, one client path** (`host.Connect` discovery order):
1. **in-process** (default) — zero-config; `noto`/`noto tui` auto-starts a server
2. **local daemon** — `noto serve` over a Unix socket, for faster repeated CLI calls
3. **remote** — `noto serve` over TCP + bearer token (`NOTO_API_URL` + `NOTO_API_TOKEN`)

All three go through the single `notoapi.Client` interface; callers never pick a transport.

---

## Structure

Packages are grouped by architectural **role**, following the dependency
flow (leaves → infra → app → delivery):

```
noto/
├── cmd/
│   ├── noto/          # main CLI entry point
│   └── capture/       # macOS capture helper: Go wrapper (main.go) + Swift (main.swift)
├── internal/
│   ├── core/                  # domain types + pure logic (leaves, no infra deps)
│   │   ├── artifacts/         # artifact types: meeting, audio, transcript, summary
│   │   ├── speakers/          # speaker-matching decision logic (cosine, robust centroid)
│   │   └── notoerr/           # structured errors
│   ├── platform/              # infrastructure adapters (disk, db, network, AI)
│   │   ├── config/            # viper-based config (dirs, providers, models)
│   │   ├── secrets/           # Keychain (darwin) / file (linux) credential store
│   │   ├── db/                # single SQLite connection opener + Migrate
│   │   ├── storage/           # file-based artifact persistence (used only by repo)
│   │   ├── repo/              # ArtifactRepository iface + LocalArtifactRepository
│   │   ├── search/            # SQLite FTS5 index + query parser
│   │   ├── speakerstore/      # speaker profile + meeting-mapping repos (SQLite)
│   │   └── providers/         # provider registry + adapters
│   │       ├── stt/           # local Parakeet STT (sherpa/NeMo) + remote-offload adapter
│   │       ├── diarize/       # speaker-turn stage (pyannote) + remote-offload adapter
│   │       ├── computewire/   # shared HTTP contract for remote STT/diarize offload
│   │       ├── speech/        # transcript normalization
│   │       ├── speaker/       # in-process ECAPA voice embedder (ONNX, CPU-only)
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

## Key architectural patterns

### Service is the Hub

`internal/app/service/service.go` — every HTTP handler and every CLI command funnels through `Service`. It owns:
- Recording state (mutex-protected)
- Job lifecycle (jobCancels map)
- Composes: storage, search, registry, secrets, IPC client, event hub

### Host — The Connection Owner

`internal/app/host/` — decides whether to connect to a remote backend (`NOTO_API_URL` + `NOTO_API_TOKEN`), use a local daemon, or start an in-process development host. All `noto` commands go through this. Never import storage/search directly from CLI.

### Provider Registry

`internal/platform/providers/registry.go` — pluggable provider registry. Production STT is the local Parakeet recognizer (`parakeet-local`); diarization is a separate stage, and both have a remote-offload path (a `noto serve` GPU/Modal node) via `computewire`. Summaries use OpenRouter-compatible LLMs. Providers implement interfaces in `types.go`.

### Artifacts as Structured Types

`internal/core/artifacts/` — typed artifacts (meeting, audio, transcript, summary) each implement `Artifact` interface with `Kind()`, `Version()`, `Validate()`. Artifacts are the universal exchange type between storage, search, and providers.

### Jobs Pipeline

Recording/import → job queued → Parakeet transcription + pyannote diarization (in-process or remote-offloaded) → speaker profile mapping → summarize → index → artifacts written → FTS updated. Jobs are stored in SQLite (`noto-jobs.sqlite`), cancellable via `context.CancelFunc` map.

### appsocket IPC

`internal/transport/appsocket/` — `IPCClient` connects to the capture helper via Unix domain socket. `cmd/capture` is a Go binary that wraps the macOS Swift helper. The Swift side does the actual audio capture.

### Event Hub for SSE

`internal/app/service/events.go` — in-process `eventHub` for broadcasting job progress to SSE handlers and the TUI.

---

## Where to look

| Need | Location |
|------|----------|
| How a CLI command works | `internal/ui/cli/cli.go` (dispatch) + `commands_*.go` |
| How TUI screens are structured | `internal/ui/tui/screen*.go`, `root.go` |
| How recording is triggered | `internal/app/service/recording.go` |
| How the pipeline runs (stages) | `internal/app/service/jobs_pipeline.go` (ingest/transcribe/summarize/index) |
| How jobs are queued/executed | `jobs.go` (queue) · `jobs_worker.go` (runtime) — both in `internal/app/service/` |
| How STT is integrated | `internal/platform/providers/stt/{provider,parakeet,remote}.go`; diarization in `internal/platform/providers/diarize/` |
| How speaker identity works | `internal/platform/providers/speaker/` (embedder) + `internal/core/speakers/` (matching) |
| How artifacts are written/read | `internal/core/artifacts/artifact.go`, `internal/app/service/storage.go` |
| How search / FTS query parsing works | `internal/platform/search/` |
| How providers are configured | `internal/app/service/providers.go`, `internal/platform/config/` |
| How capture helper IPC works | `internal/transport/appsocket/`, `cmd/capture/{main.go,main.swift}` |
| How secrets are stored | `internal/platform/secrets/` |
| How LLM prompts are built | `internal/platform/providers/llm/` + `llm/prompts/` |

---

## Conventions

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

## Anti-patterns

- **NEVER** import `storage` or `search` directly from CLI packages — go through `Service` via `notoapi.Client`
- **NEVER** use bare `errors.New` — use `notoerr.*` types
- **NEVER** hardcode paths — use `config.Config` struct (respects `NOTO_*` env vars)
- **NEVER** block the Service goroutine — all long work runs in background job goroutines
- **NEVER** log to stdout/stderr in library code — return errors, let caller decide

---

## Commands

```bash
make dev             # TUI from source (fastest iteration)
make serve           # daemon mode (unix socket)
make build           # produce ./bin/noto
make test            # run all tests
make test-race       # race detector
make test-e2e        # only E2E pipeline test
make check           # fmt-check + vet + lint + test — the gate CI enforces
make seed            # populate 3 fixture meetings
make clean           # rm ./bin/

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
noto speaker-model download|status   # install the in-process voice model
```

---

## Notes

- `cmd/capture` is a Go wrapper (`main.go`) around a Swift ScreenCaptureKit helper
  (`main.swift`) — both live in this repo; the Swift side does the actual macOS
  mic + system-audio capture and talks to the backend over the `appsocket` socket
- `make dev` pre-warms the capture helper (best-effort) so the first recording is fast
- FTS5 search ranks by recency + BM25-like scoring
- Artifact checksum tracks integrity of stored JSON vs generated Markdown
- Speaker recognition runs **in-process** (pure-Go fbank + ONNX ECAPA, CPU-only);
  biometrics never leave the backend. Transcription/diarization run locally
  (Parakeet/pyannote) or on the operator's own remote GPU/Modal node — never a
  third-party cloud STT.

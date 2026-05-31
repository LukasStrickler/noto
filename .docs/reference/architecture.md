# Architecture Reference

## Overview

Noto uses a local TUI/CLI client backed by a `noto serve` HTTP/SSE backend.
The backend owns all storage and processing. The TUI is a thin local consumer.

```
Local TUI/CLI ←→ noto serve (HTTP/SSE) ←→ AssemblyAI (STT) + OpenRouter (LLM)
                     ↓
              ArtifactRepository (internal/platform/repo)
                     ↓
              LocalArtifactRepository → SQLite + filesystem artifacts
```

## Components

| Component | Owns | Does not own |
| --- | --- | --- |
| `noto` TUI/CLI | Local UI state, keyboard handling | Remote storage, provider credentials |
| `noto serve` | HTTP API, job orchestration, storage, AssemblyAI routing, search | macOS permissions, local capture hardware |
| `cmd/capture` | macOS audio capture, split mic/system recording | API, storage, provider routing |

## Module Map

| Package | Responsibility |
| --- | --- |
| `cmd/noto` | TUI/CLI entry point, command wiring |
| `cmd/noto serve` | Backend daemon, HTTP server, job runner |
| `cmd/capture` | macOS native capture helper |
| `internal/platform/repo` | `ArtifactRepository` interface + `LocalArtifactRepository` |
| `internal/platform/storage` | Low-level filesystem artifact R/W (used only by `repo`) |
| `internal/core/artifacts` | Meeting, audio, transcript, summary types and schemas |
| `internal/app/service` | Business logic. Uses `repo` interface — never calls `storage` directly |
| `internal/transport/server` | HTTP API handlers, SSE, middleware |
| `internal/app/host` | Server lifecycle, dep injection, in-process/remote dispatch |
| `internal/transport/apiclient` | Direct (in-process) and HTTP client implementations |
| `internal/platform/search` | SQLite FTS5 index and queries |
| `internal/platform/providers/stt` | AssemblyAI STT adapter |
| `internal/platform/providers/llm` | OpenRouter LLM adapter |
| `internal/platform/speakerstore` | Speaker profile + meeting speaker mapping repositories (SQLite) |
| `internal/testutil` | `FakeRepo` and test helpers for unit tests |
| `internal/ui/tui` | Bubble Tea screens and models |

## Interfaces

| Interface | Package | Purpose |
| --- | --- | --- |
| `ArtifactRepository` | `internal/platform/repo` | All artifact persistence. Current: `LocalArtifactRepository`. Future: Postgres + S3. |
| `notoapi.Client` | `internal/transport/notoapi` | TUI/CLI → backend. Direct in-process or HTTP. |
| `STTProvider` | `internal/platform/providers/stt` | Transcription adapter (AssemblyAI). |
| `SummaryProvider` | `internal/platform/providers/llm` | LLM summarization adapter (OpenRouter). |
| `JobQueue` | `internal/app/service` | SQLite-backed job scheduling and state machine. |
| `SearchIndex` | `internal/platform/search` | FTS5 query and indexing interface. |

## Storage Seam

Service methods access storage **only** through `repo.ArtifactRepository`.
The `internal/platform/storage` package is an implementation detail of `LocalArtifactRepository`
and must not be imported by `internal/app/service` or `internal/transport/server`.

```
internal/app/service  →  internal/platform/repo (ArtifactRepository interface)
                          ↓
                  internal/platform/repo/local.go (LocalArtifactRepository)
                          ↓
                  internal/platform/storage (filesystem / SQLite)
```

For unit tests, inject `testutil.FakeRepo` instead. No disk I/O, no goroutines.

## Data Flow

1. `noto record` → capture helper records audio (local, same machine as TUI)
2. `noto stop` → service enqueues a pipeline job
3. **Pipeline** (ingest → transcribe → summarize → index):
   - Ingest: `repo.CreateMeeting()` writes manifest
   - Transcribe: AssemblyAI → `repo.SaveTranscript()`
   - Summarize: OpenRouter LLM → `repo.SaveSummary()`
   - Index: FTS5 index updated from transcript + summary
4. Speaker profile matching runs alongside transcription
5. TUI receives updates via SSE events

## Error Model

| Code | Meaning |
| --- | --- |
| `unsupported_capability` | Provider or processor cannot perform the requested capability. |
| `schema_validation_failed` | Output does not match the required Noto schema. |
| `provider_failed` | AssemblyAI or LLM returned a failed job or unusable response. |
| `artifact_conflict` | Manifest commit detected competing versions. |
| `permission_denied` | macOS, workspace, provider, or storage permission is missing. |
| `retryable_remote_error` | Remote API or provider error that can be retried. |

## Invariants

- The backend is the source of truth; TUI is a local consumer.
- Service methods never import `internal/platform/storage` directly.
- Capture stays local on the macOS machine.
- All provider traffic (AssemblyAI, OpenRouter) flows through the backend.
- Speaker profile matching runs on the backend, not in the TUI.

# Cloud Architecture

## Overview

Noto uses a local TUI/CLI client that connects to a `noto serve` backend.
The backend owns all data storage, AssemblyAI transcription, LLM summarization,
search indexing, and speaker profile management. The TUI is a thin local consumer
of the remote API.

```
┌──────────────────────────────────────────────────────────────────┐
│                        Local machine                              │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │  noto TUI / CLI                                            │  │
│  │  (Bubble Tea UI, JSON commands, local-only UI state)       │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
                               │ HTTP/SSE (UDS or TCP)
                               ▼
┌──────────────────────────────────────────────────────────────────┐
│                        noto serve backend                         │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐   │
│  │  HTTP API    │  │  Job runner  │  │  Speaker profiles    │   │
│  │  server      │  │  (pipeline)  │  │  + embedding/matching│   │
│  └──────────────┘  └──────────────┘  └──────────────────────┘   │
│                                                                   │
│  ┌──────────────────────────────────────────────────────────┐    │
│  │  ArtifactRepository (internal/platform/repo)                      │    │
│  │  LocalArtifactRepository → SQLite + filesystem artifacts │    │
│  │  Future: swap to Postgres + S3 without changing services │    │
│  └──────────────────────────────────────────────────────────┘    │
└──────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌──────────────────────────────────────────────────────────────────┐
│                     External providers                            │
│  AssemblyAI (transcription + diarization)                        │
│  OpenRouter-compatible LLM (summarization)                       │
└──────────────────────────────────────────────────────────────────┘
```

## Core Principles

1. **Backend is source of truth.** All meeting data lives on the server. The TUI
   browses and searches via the API — it holds no local copy.
2. **Speaker profiles are first-class.** AssemblyAI provides per-meeting diarization
   labels. The backend maintains persistent cross-meeting speaker identity via
   an embedding/matching pipeline.
3. **Storage seam enforced.** The `ArtifactRepository` interface (`internal/platform/repo`)
   is the only storage access point. Service methods never call the storage package
   directly. This keeps Postgres/S3 swap to one package change.
4. **AssemblyAI for production STT.** Local model stacks are not in scope.

## Binaries

| Binary | Role |
| --- | --- |
| `noto` / `noto tui` | Local TUI entry point. Connects to `noto serve`. |
| `noto serve` | Backend daemon: HTTP API, job runner, storage, provider routing. |
| `cmd/capture` | macOS native audio capture helper (same machine as TUI). |

## Data Flow

1. `noto record` → capture helper starts recording on the local machine
2. `noto stop` → capture helper finalises the audio file; service enqueues a pipeline job
3. **Pipeline job**: ingest → transcribe → summarize → index
4. Ingest: `repo.CreateMeeting()` writes the meeting manifest
5. Transcribe: AssemblyAI call → diarized transcript written via `repo.SaveTranscript()`
6. Summarize: OpenRouter LLM call → structured summary written via `repo.SaveSummary()`
7. Index: FTS5 search index updated from transcript + summary data
8. Speaker profile matching runs in parallel with transcription
9. TUI reflects updated state via SSE events or next poll

## API Contract

The backend exposes HTTP/SSE endpoints at `/v1/...`:

| Endpoint | Methods | Purpose |
| --- | --- | --- |
| `/v1/meetings` | GET | List meetings with optional FTS query |
| `/v1/meetings/{id}` | GET, DELETE | Single meeting CRUD |
| `/v1/meetings/{id}/transcript` | GET | Diarized transcript |
| `/v1/meetings/{id}/summary` | GET | Structured summary + markdown |
| `/v1/meetings/{id}/files` | GET | Filesystem artifact paths |
| `/v1/meetings/{id}/agent` | GET | Agent handoff (paths + CLI commands) |
| `/v1/meetings/{id}/speaker-mappings` | GET, PATCH | Per-meeting speaker labels |
| `/v1/search` | GET | FTS5 search with speaker/scope filters |
| `/v1/recording` | GET | Current recording state |
| `/v1/recording/start`, `/stop`, `/pause`, `/resume` | POST | Recording control |
| `/v1/recording/meters` | SSE | Live audio level stream |
| `/v1/jobs` | GET, POST | Job list and creation |
| `/v1/jobs/events` | SSE | Job and recorder event stream |
| `/v1/speaker-profiles` | GET, POST | Speaker profile management |
| `/v1/providers` | GET | Provider list and key status |
| `/v1/config` | GET, PATCH | Config read/write |
| `/v1/storage/verify`, `/reindex` | POST | Storage maintenance |

## ArtifactRepository Interface

`internal/platform/repo.ArtifactRepository` is the storage seam. Service methods use it
exclusively; they do not import `internal/platform/storage` directly.

```go
// internal/platform/repo/repo.go
type ArtifactRepository interface {
    CreateMeeting(ctx context.Context, id uuid.UUID, opts CreateMeetingOpts) error
    GetMeeting(ctx context.Context, id uuid.UUID) (*StoredMeeting, error)
    ListMeetings(ctx context.Context) ([]*StoredMeeting, error)
    DeleteMeeting(ctx context.Context, id uuid.UUID) error

    SaveTranscript(ctx context.Context, id uuid.UUID, t *artifacts.Transcript) error
    LoadTranscript(ctx context.Context, id uuid.UUID) (*artifacts.Transcript, error)
    SaveSummary(ctx context.Context, id uuid.UUID, md string, s *artifacts.Summary) error
    LoadSummary(ctx context.Context, id uuid.UUID) (string, *artifacts.Summary, error)

    PrepareAudio(ctx context.Context, id uuid.UUID, ext string) (path string, err error)
    AudioPath(id uuid.UUID) (string, bool)
    VerifyIntegrity(ctx context.Context, id uuid.UUID) error
    FilePaths(id uuid.UUID) *MeetingFilePaths // nil for non-filesystem backends
}
```

**Current implementation**: `LocalArtifactRepository` in `internal/platform/repo/local.go`,
backed by the `internal/platform/storage` package (SQLite + filesystem).

**Future swap**: Implement the interface for Postgres + S3 and inject it via
`service.Deps.Repo`. No service or handler code changes required.

**Test double**: `testutil.FakeRepo` in `internal/testutil/fake_repo.go` provides
an in-memory implementation for unit tests that run without disk I/O.

## Speaker Profiles

Speaker profiles enable persistent cross-meeting voice identity:

1. AssemblyAI returns per-meeting diarization labels (speaker_0, speaker_1, ...)
2. If `NOTO_SPEAKER_EMBEDDING_URL` is configured, the backend sends audio + diarized
   segments to that service to generate speaker embeddings
3. The embedding/matching pipeline compares new embeddings against the profile store
4. High-confidence matches rename the label to the profile name; low-confidence
   matches create a pending profile for user confirmation
5. Without a configured embedding service, transcription succeeds and mappings
   remain "unmatched" — never silently wrong

## Deployment Modes

| Mode | noto serve location | TUI access |
| --- | --- | --- |
| Local (in-process) | Same process as TUI | Direct client, zero overhead |
| Local (daemon) | Same machine, UDS socket | HTTP over Unix socket |
| Remote | Remote server, TCP | HTTP over TCP with bearer token |

The `host.Connect()` function handles discovery: checks `NOTO_API_URL` first,
then probes the local UDS, then starts an in-process server as last resort. The TUI
and all CLI commands use the same `notoapi.Client` interface regardless of mode.

## Package Map

| Package | Responsibility |
| --- | --- |
| `internal/platform/repo` | `ArtifactRepository` interface + `LocalArtifactRepository` |
| `internal/platform/storage` | Low-level filesystem R/W (used only by `repo`) |
| `internal/app/service` | Business logic; uses `repo` interface, never `storage` directly |
| `internal/transport/server` | HTTP handlers; delegates to `service` |
| `internal/app/host` | Server lifecycle, dep wiring, in-process vs remote dispatch |
| `internal/transport/apiclient` | HTTP and direct client implementations |
| `internal/platform/search` | SQLite FTS5 index |
| `internal/platform/providers` | AssemblyAI STT + OpenRouter LLM adapters |
| `internal/testutil` | `FakeRepo` and test helpers for unit tests |

## What Is Out of Scope

- Local model management
- Local/offline STT engine stacks
- Multi-worker broker architectures
- Distributed job queue systems
- Native cross-meeting speaker identification without embedding service

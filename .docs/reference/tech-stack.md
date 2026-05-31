# Tech Stack Reference

## Stack

| Area | Implementation | Extension boundary |
| --- | --- | --- |
| CLI/TUI | Go, Bubble Tea | `notoapi.Client` interface |
| Backend server | Go, net/http | `noto serve` entry point |
| STT | AssemblyAI (dry-run fallback) | `stt.STTProvider` interface |
| LLM summarization | OpenRouter-compatible | `llm.SummaryProvider` interface |
| Storage | SQLite + filesystem (`LocalArtifactRepository`) | `repo.ArtifactRepository` interface |
| Search | SQLite FTS5 | `search.SearchIndex` |
| macOS capture | Native helper (Swift binary) via ScreenCaptureKit | `appsocket.IPCClient` |
| Job queue | SQLite-backed, in-process workers | — |

## Connection Modes

All three modes use the same `notoapi.Client` interface:

| Mode | Transport | When |
| --- | --- | --- |
| In-process (default) | Direct Go function call | `noto` / `noto tui` with no daemon running |
| Local daemon | HTTP over Unix domain socket | `noto serve` running separately |
| Remote | HTTP over TCP with bearer token | `NOTO_API_URL` + optional `NOTO_API_TOKEN` |

## Core Constraints

- **No local STT.** AssemblyAI is the production provider. Dry-run fallback for dev.
- **No local model management.** Model selection is via `noto providers active-llm`.
- **No distributed workers.** Single `noto serve` handles all jobs.
- **No required hosted database for local use.** SQLite + filesystem, zero-config.
- **Storage seam enforced.** `internal/app/service` imports `internal/platform/repo` only, never `internal/platform/storage`.

## Efficiency Rules

- Capture helper only runs when recording is active.
- TUI idle does not tick — no global render loop.
- Jobs run in the backend, not in the TUI goroutine.
- SQLite search index can be rebuilt from artifacts at any time (`noto reindex`).
- All provider calls (AssemblyAI, OpenRouter) go through the backend, not the TUI.

## Anti-Requirements

- No local/offline STT provider.
- No local model management.
- No multi-worker broker job queue.
- No provider-native payload as a downstream contract (normalized artifact formats only).

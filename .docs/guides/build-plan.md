# Build Plan

## Strategy

Backend first, then TUI. The backend owns storage, AssemblyAI transcription,
LLM summarization, search indexing, and speaker profile matching.
The TUI is a thin consumer of the remote API.

Every phase is test-first: write the fixture, failing test, or schema check before
implementing the behavior.

## Status

### ✅ Phase 1: Backend Core — DONE

`noto serve` backend fully operational:

- HTTP API server with all planned REST endpoints
- SSE endpoint for job progress and recorder events
- `ArtifactRepository` interface (`internal/repo`) with `LocalArtifactRepository` implementation
- SQLite-backed job queue with workers, cancellation, restart recovery
- Full pipeline: ingest → transcribe → summarize → index
- AssemblyAI STT adapter (with deterministic dry-run fallback)
- OpenRouter LLM adapter (with deterministic fallback when no key)
- `runVerify` implemented — actual checksum verification via `repo.VerifyIntegrity()`
- Bulk `runIndex` implemented — walks all meetings via `repo.ListMeetings()`
- Speaker profile store + matching pipeline (external embedding service)
- SQLite FTS5 search index (sanitized input, title weighting, recency tiebreaker)
- Import audio (`noto import-audio`) wired through `repo.PrepareAudio()`
- Recording: start/stop/pause/resume/marker/preflight/live meters
- Config CRUD via API

### ✅ Phase 2: TUI Client — DONE

`noto` TUI fully operational:

- Bubble Tea dashboard: meetings list + FTS search, 1/3+2/3 split, embedded detail pane
- Detail pane: Summary / Transcript / Speakers tabs, inline speaker renaming
- Match jumping (n/N) across search hits
- Recorder screen: idle/active, live waveform, title input, markers
- Config screen: active routes, API keys, storage, paths
- Agent screen: handoff view with copyable CLI commands
- Root: screen stack, SSE subscription, command palette (`:`), help overlay, status bar
- All CLI commands: `list`, `search`, `show`, `transcript`, `summary`, `files`,
  `agent`, `status`, `providers`, `verify`, `record`, `stop`, `import-audio`,
  `jobs`, `ping`, `seed`, `dev`, `serve`

### ✅ Testing Infrastructure — DONE

- `internal/repo` — 7 atomic tests for `LocalArtifactRepository` (no host, no HTTP)
- `internal/service/meetings_unit_test.go` — 9 atomic unit tests using `testutil.FakeRepo`
- `internal/testutil.FakeRepo` — in-memory `ArtifactRepository` for fast unit tests
- E2E tests: `notohost/host_test.go` (record/pipeline/search/agent), `service/import_test.go`
- Service tests: speaker profiles, embedder pipeline, matching

### ❌ Phase 3: Capture Integration — NOT STARTED

- Native macOS capture helper (Swift binary) is separate from this repo
- `cmd/capture/main.go` is an IPC relay tool for scripting/dev — not the actual capture binary
- Without the Swift helper, recording runs in dry-run mode
- Estimated scope: Swift binary + macOS audio API + IPC protocol (already defined in `internal/appsocket`)

## Remaining Gaps

### Testing (needed for done criteria)

- No CLI golden tests for `--json` output shapes
- No fixture testdata set (`empty-root`, `one-meeting-transcript`, `invalid-artifact`)
- TUI snapshot test coverage is partial
- Agentic validation test suite (`noto verify --json` checking success/failure) not automated

### Features

- Speaker embedding service requires external `NOTO_SPEAKER_EMBEDDING_URL` — no bundled service
- Live STT during recording (`providers/live/speech.go` exists but not wired to recording pipeline)

## Out of Scope for Active Build Plan

- Local/offline STT engines
- Local model management commands
- Multi-worker broker deployments
- Distributed job queue systems
- Object storage sync (R2/S3)
- Postgres replacement for SQLite

These are preserved as interface seams (via `ArtifactRepository`), not active deliverables.

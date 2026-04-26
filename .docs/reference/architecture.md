# Architecture Reference

## Components

V1 uses the local `noto` TUI/CLI as the main interface, a native macOS capture
helper for recording, an artifact store, processors, and SQLite search. Sync
gateway implementations and remote API components are future architecture.

| Component | Owns | Does not own |
| --- | --- | --- |
| `noto` | Main TUI/CLI UX, foreground local jobs, artifacts, processors, search, JSON agent access. | macOS permission identity, remote policy. |
| Native capture helper | Split mic/system capture, permissions, minimal recording state. | Search, sync policy, provider normalization, primary UX. |
| Local artifacts | Portable source data. | Provider-specific raw payload as product data. |
| SQLite | Local search cache. | Source of truth. |
| Future sync gateway | Storage access, policy checks, manifest updates, sync/job requests. | Audio capture, local search, artifact schema ownership. |
| Future object storage | Immutable versions, manifests, repairable indexes. | Query engine, lock service, patch system. |
| Future remote API | Auth, device trust, policy, signed URLs, metadata, audit, billing. | Audio capture, local search, only copy of data. |

## Implementation Map

Default layout:

| Package/module | Owns |
| --- | --- |
| `cmd/noto` | CLI command wiring, JSON output, process exit codes. |
| `internal/appsocket` | Local socket client for the native capture helper. |
| `internal/artifacts` | IDs, layouts, schema validation, version commits, checksums. |
| `internal/processors` | Processor registry, capability matching, contract tests. |
| `internal/providers/stt/*` | STT adapters and provider-to-`transcript.v1` normalizers. |
| `internal/providers/summary/*` | Summary adapters and `summary.v1` validation. |
| `internal/search` | SQLite FTS5 schema, rebuild, query result shape. |
| `internal/tui` | Bubble Tea screens and keyboard behavior. |
| `mac/Noto` | Minimal native capture helper, permission onboarding, and split mic/system recording. |
| Future `internal/syncgateway` | Interface plus local, object-store, and remote implementations. |

Keep server code in `server/noto-api` so local builds avoid hosted dependencies.

## Interfaces

| Interface | Purpose |
| --- | --- |
| `ArtifactStore` | Read/write local artifact versions. |
| `Processor` | Transform one standard Noto input into one standard Noto output. |
| `TranscriptionProvider` | Submit audio, poll jobs, and normalize STT payloads. |
| `SummaryProvider` | Generate validated structured summaries from transcripts. |
| `CaptureController` | Start, stop, and inspect local recording through the native capture helper. |
| Future `SyncGateway` | Single port used by the app for sync, policy, object access, remote-capable jobs, and audit. |
| Future `LocalSyncGateway` | In-process implementation of `SyncGateway` for local filesystem and owner-credential object storage. |
| Future `RemoteSyncGateway` | HTTP implementation of `SyncGateway` for hosted and self-hosted Noto API deployments. |

## Module Boundaries

| Module | Inputs | Outputs | Replaceable by |
| --- | --- | --- | --- |
| Capture | macOS mic and system/app audio streams, meeting metadata | temp audio asset, capture receipt with source roles | another native recorder |
| Audio preparation | temp/imported audio | normalized audio asset metadata | encoder, silence trimmer, local DSP |
| Transcription | audio asset, provider config | `transcript.diarized.json` | cloud STT, local model, hosted worker |
| Summary | transcript, prompt config | `summary.json` | OpenAI-compatible model, local model |
| Rendering | JSON artifacts | Markdown artifacts | alternate renderer/exporter |
| Indexing | JSON artifacts | SQLite search rows | alternate local search index |
| Future sync | manifests, checksums, object keys | pushed/pulled immutable versions | local pass-through, object store, remote API |

Test each boundary with fixtures and schema-validated outputs. Modules communicate through artifacts or typed interfaces, not provider payloads.

Boundary tests and phase gates are defined in [testing.md](./testing.md). Any new module must add fixture coverage before it becomes part of a V1 workflow.

## Future SyncGateway Contract

Application code depends on this port, not API endpoints or storage SDKs. Selection is a config switch.

The method contract, commit semantics, and credential rules live in [storage-sync.md](./storage-sync.md).

## Processor Contract

Processors extend capture post-processing, transcription, summaries, rendering,
indexing, exports, and enrichment. They may call local code, hosted services, or
third-party APIs, but must read and write standard Noto structures.

| Processor type | Standard input | Standard output |
| --- | --- | --- |
| `AudioProcessor` | raw/imported audio plus meeting metadata | provider-ready audio asset and audio metadata |
| `TranscriptionProcessor` | audio asset | `transcript.diarized.json` |
| `SummaryProcessor` | `transcript.diarized.json` plus prompt config | `summary.json` |
| `RenderProcessor` | normalized JSON artifact | Markdown artifact |
| `IndexProcessor` | normalized transcript/meeting artifacts | SQLite FTS5 rows |
| `ExportProcessor` | normalized artifacts | external export format |

Processor metadata:

```json
{
  "id": "assemblyai:universal-3-pro",
  "kind": "transcription",
  "input_schema": "audio-asset.v1",
  "output_schema": "transcript.v1",
  "capabilities": ["diarization", "word_timestamps", "source_roles", "async"],
  "requires_network": true,
  "sends_raw_audio_off_device": true
}
```

Processor rules: select by ID/capabilities, fail fast on missing capabilities,
keep provider raw responses as debug data only, normalize before downstream
use, never require downstream provider branches, and validate outputs against
fixtures.

## Data Flow

1. The native capture helper records temporary split mic/system audio when available, or `noto` imports audio/transcript fixture data.
2. Completed recordings are ingested into `meeting.json` and `audio.json` with source roles.
3. The configured transcription processor normalizes STT output into `transcript.diarized.json` when audio is processed.
4. The configured summary processor writes `summary.json` with evidence segment IDs.
5. The render processor writes Markdown from normalized JSON.
6. The index processor rebuilds SQLite FTS5 from normalized artifacts.
7. The TUI reads artifacts and search results from local storage.

Post-V1, `SyncGateway` can push immutable versions and update manifests through filesystem, object-store, or HTTP API implementations.

## Error Model

All module errors are machine-readable.

| Code | Meaning |
| --- | --- |
| `unsupported_capability` | Selected gateway or processor cannot perform the requested capability. |
| `schema_validation_failed` | Output does not match the required Noto schema. |
| `provider_failed` | Provider returned a failed job or unusable response. |
| `artifact_conflict` | Manifest commit detected competing versions. |
| `permission_denied` | macOS, workspace, provider, or storage permission is missing. |
| `retryable_remote_error` | Remote API, object store, or provider error can be retried. |

## Invariants

- Capture is local in every deployment mode.
- Recorded V1 capture preserves mic/system source roles whenever macOS allows it.
- Artifacts are the durable source format.
- SQLite can be deleted and rebuilt.
- Remote gateway mode cannot be required for local read/search.
- V1 has no remote gateway, idle daemon, sync service, or detached transcription worker.
- Future local gateway behavior is in-process and event-driven; it does not require an idle daemon.
- Future API knowledge is isolated inside `RemoteSyncGateway`.
- Future local and remote gateway implementations must expose 1:1 behavior for supported methods.
- Provider-specific responses are debug data, not product data.
- Every replaceable processor must output standard Noto artifacts or typed unsupported-capability errors.

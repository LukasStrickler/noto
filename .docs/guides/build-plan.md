# Build Plan

## Strategy

Build around local artifacts first, then add the terminal TUI and native capture
helper. V1 is local-only and terminal-first: macOS split-source recording,
ingest/import, cloud transcription provider adapters, JSON/Markdown artifacts,
SQLite FTS5 search, summaries, a keyboard-first Bubble Tea interface, and
JSON/file-path access for agents.

V1 ships after Phase 5. Sync, hosted APIs, hosted provider routing, and local
transcription are post-V1 phases.

Every phase is test-first. Add the fixture, schema check, golden output, or
agentic validation command before implementing the behavior. The acceptance
gates live in [testing.md](../reference/testing.md).

## V1 Phases

0. Validation spike:
Build the benchmark harness, `benchmark-result.v1`, dataset access notes,
three public samples, one consented split-source private sample, two STT runs,
and one normalized transcript. Gate: benchmark fixtures capture quality,
source attribution, latency, cost, RSS, idle CPU, and provider versions.

1. Artifact core:
Build IDs, local layout, JSON schemas, version commits, checksums, Markdown
renderers, import/list/show/transcript/summary commands, and fixtures. Gate:
`noto verify --json` passes valid fixtures, fails invalid fixtures, and invalid
processor output cannot commit.

2. Search and agent access:
Build the SQLite FTS5 schema, rebuild command, `noto search --json`, and
generated `~/Noto/SKILL.md`. Gate: search returns cited segments with source
roles, and agent workflow tests answer with segment citations.

3. Providers and summaries:
Build the processor registry, capability matching, STT provider interface, one
baseline STT adapter, one comparison STT adapter, summary provider, and
`prompts/summary.v1.md`. Gate: both STT providers pass the same `transcript.v1`
contract tests, and summaries cite transcript evidence.

4. TUI and agent CLI:
Build the Bubble Tea dashboard, meetings/search/detail/transcript/settings
screens, JSON commands, artifact-path responses, 1,000-meeting fixture, and
2-hour transcript fixture. Gate: model tests, snapshot tests, idle tick tests,
and CLI JSON golden tests pass.

5. macOS capture helper:
Build the minimal native helper, permission onboarding,
ScreenCaptureKit/AVFoundation split mic/system capture, temp writer, Unix socket
protocol, and recovery. Gate: capture lifecycle tests prove record/stop, TUI
exit recovery, split-source ingest, source roles, and raw-audio retention.

## Post-V1 Phases

- Sync gateway: add `SyncGateway`, local filesystem sync, owner-credential
  object storage, and conflict detection.
- Remote gateway client: add the remote adapter and local/object-store/remote
  parity tests.
- Remote API spike: validate device trust, policy, signed object access,
  manifest metadata, and audit events.
- Hosted provider routing: add provider policy, key ownership modes, async
  workers, cost counters, and retry states.
- Local transcription: evaluate local model runtime, RAM, WER/DER, and install
  size before choosing beta scope.

## V1 Job Lifecycle

- The native capture helper owns active split mic/system recording and minimal
  recording state.
- Recording survives TUI exit without making a GUI the main product.
- The CLI/TUI talks to the capture helper over a local socket for record, stop,
  and status.
- CLI commands run foreground work and exit when complete.
- The TUI may start foreground child tasks for import, transcription, summary,
  render, and index rebuild.
- Active tasks report progress in the Jobs pane and must be cancelled or
  allowed to finish explicitly before TUI exit.
- Mutating jobs write only after schema and checksum validation.
- Partial outputs stay in `.tmp/` and are never promoted to the current version.
- Long-running processing can resume with explicit CLI commands:
  `noto transcribe`, `noto summarize`, and `noto index rebuild`.
- V1 has no detached transcription worker, idle HTTP server, sync daemon, or
  LaunchAgent.
- If later processing must survive TUI exit, add a separate helper with an
  explicit lifecycle.

## First Tasks

1. Define the fixture layout and validation commands from
   [testing.md](../reference/testing.md).
2. Create JSON schemas for `manifest.v1`, `meeting.v1`, `audio-asset.v1`,
   `transcript.v1`, `summary.v1`, and `checksums.v1`.
3. Add schema fixtures for overlap, missing word timestamps, split mic/system
   source roles, retained audio, deleted audio, conflicts, and invalid
   artifacts.
4. Write failing schema/checksum tests and `noto verify --json` golden
   responses.
5. Implement artifact writer, version commits, checksum validation, and
   Markdown renderers.
6. Implement import/list/show/transcript/summary commands with CLI JSON golden
   tests.
7. Implement benchmark fixture loader, scoring settings, and
   `benchmark-result.v1`.
8. Implement WER/DER/JER scoring wrappers.
9. Implement FTS rebuild and search with search fixture tests.
10. Implement processor registry, capability matching, and output validation.
11. Implement AssemblyAI plus one second STT adapter.
12. Implement summary provider and evidence validation.
13. Build TUI fixture set and polish dashboard/search/detail workflows with
    model/snapshot tests.
14. Implement agent JSON/file-path commands and agent workflow tests.
15. Implement native capture helper, local socket control, split mic/system
    capture, recovery, and recording ingest.

## Risks

| Risk | Mitigation |
| --- | --- |
| TUI overbuild slows V1 | Keep V1 to local artifacts, recording, ingest, search, summaries, and browsing. |
| macOS system audio is brittle | Native app, permission onboarding, mic-only fallback. |
| diarization quality is weak | Use mic/system source roles as the first speaker hint, provider benchmark, speaker rename, retain audio until transcript validates. |
| Remote API becomes required | Keep sync and remote adapters post-V1; local artifacts remain source of truth. |
| API handles large audio inefficiently | Signed object access and async workers. |
| Self-hosted stack grows too heavy | Small `noto-server`; external Postgres and S3-compatible storage. |
| Agent writes corrupt artifacts | Schema validation and new versions for writes. |
| Raw audio privacy issue | V1 local retention policy only; hosted upload requires later explicit workspace policy. |
| Processor swap breaks downstream behavior | Contract tests for every processor output schema. |

## Related

- [Product reference](../reference/product.md)
- [Feature alignment](../reference/features.md)
- [Testing and validation](../reference/testing.md)
- [Artifact reference](../reference/artifacts.md)

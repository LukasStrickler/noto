# Product Reference

## Goal

Noto records or imports meeting audio, transcribes it, and turns it into local,
searchable, citeable artifacts. V1 is terminal-first: the main interface is the
`noto` TUI, and the CLI exposes JSON and artifact paths for agents.

## Users

| User | Need |
| --- | --- |
| Individual | Record, import, browse, summarize, and search private meeting context locally. |
| Agent user | Retrieve local meeting context with transcript citations. |
| Future power user | Sync personal artifacts across trusted devices. |
| Future team | Use managed auth, device policy, billing, and provider routing. |
| Future compliance customer | Self-host API, metadata, object storage, and providers. |

## Surfaces

| Surface | Purpose |
| --- | --- |
| `noto` TUI | Primary user interface for recording control, browsing, search, transcript review, summaries, jobs, settings, and provider status. |
| `noto` CLI/JSON | Direct automation and agent interface for search, transcript, summary, actions, status, and artifact paths. |
| `~/Noto` | Local source format and search cache. |
| Providers | Transcription and summary services for recorded/imported audio and transcripts. |
| Native macOS capture helper | Permission-bound recording engine and minimal status only; not the product UI. |
| Future sync gateway | Local or remote storage, policy, sync, and remote-capable jobs. |

## V1 Scope

- Record microphone and supported system/selected-app audio on macOS.
- Preserve microphone and system/app audio as distinct sources for local-speaker vs participant attribution.
- Continue recording if the TUI exits.
- Ingest completed recordings into local artifacts.
- Import existing audio.
- Import existing normalized transcript JSON for fixtures/manual workflows.
- Produce normalized diarized transcript JSON.
- Render transcript Markdown.
- Produce summary JSON and Markdown.
- Link decisions and action items to transcript segment IDs.
- Rename speakers.
- Search locally with SQLite FTS5.
- Expose stable JSON commands for scripts and agents.
- Expose local artifact paths so agents can read files directly when appropriate.
- Switch processors/providers by configuration while preserving the same artifact schemas.
- Browse meetings, summaries, transcripts, actions, and provider status in a keyboard-first TUI.

## Later Scope

- Object-store sync through the same gateway contract in local and remote modes.
- Hosted and self-hosted remote gateways.
- Hosted provider routing, billing, and customer-managed keys.
- Local transcription and additional processors.
- Local MCP server.

## Non-Goals

- No meeting bot.
- No GUI-first app. The TUI is the main interface.
- No live transcript requirement.
- No live diarization requirement.
- No hosted audio capture.
- No required remote database or API for local use.
- No mobile app in the current plan.

## Quality Bar

- Local artifacts remain readable without Noto.
- Provider payloads are normalized before storage.
- Replaceable processors produce the same Noto artifacts.
- Summary claims cite transcript segment IDs.
- Search results include meeting, segment, speaker, and timestamp.
- Speaker attribution uses capture source hints: microphone means local speaker by default, system/app audio means remote participant by default.
- Raw audio deletion happens only after validated transcript artifacts exist and local policy allows deletion.
- Sync is not part of V1 and cannot block reading, search, or summary generation.
- Idle resource use is near zero: no busy TUI render loop, no idle HTTP server, no hot sync polling.
- Every user-visible command that mutates data validates schemas before commit.
- Every V1 feature has an agent-readable validation path through `--json`, local artifacts, or fixture tests.

## Success Metrics

| Area | V1 target |
| --- | --- |
| Recording reliability | Recording survives TUI exit and completed audio is ingested into artifacts. |
| Speaker attribution | Recorded meetings preserve mic/system source roles so local speaker and participants are distinguishable before manual rename. |
| TUI usability | Keyboard-only browsing, search, transcript reading, and action review work against local fixtures. |
| Agent access | JSON commands return citations and local artifact paths without requiring a server. |
| Agentic validation | `noto verify --json` and fixture tests detect schema, checksum, index, source-role, and retention failures. |
| Artifact portability | A meeting can be read from JSON and Markdown without the app. |
| Provider swap | Two STT processors produce valid `transcript.diarized.json` for the same fixture. |
| Search | Query returns cited segments from a 1,000-meeting fixture within interactive latency. |
| Idle cost | CLI exits when work is done; TUI does not tick or poll while idle. |
| Privacy | Raw audio leaves the device only under explicit provider or workspace policy. |

Detailed acceptance gates are in [testing.md](./testing.md).

## Related

- [Architecture reference](./architecture.md)
- [Artifact reference](./artifacts.md)
- [CLI reference](./cli.md)
- [Build plan](../guides/build-plan.md)

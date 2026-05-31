# Product Reference

## Goal

Noto records or imports meeting audio, sends it to a remote-capable backend,
transcribes it with AssemblyAI, summarizes it with an OpenRouter-compatible LLM,
and exposes searchable, citeable meeting memory to humans and agents.

## Users

| User | Need |
| --- | --- |
| Individual | Record, import, browse, summarize, and search private meeting context. |
| Agent user | Retrieve meeting context through stable JSON/API contracts with transcript citations. |
| Future team | Share a backend with managed auth, storage policy, and provider keys. |

## Surfaces

| Surface | Purpose |
| --- | --- |
| `noto` TUI | Primary keyboard-first interface for browsing, search, transcript review, summaries, jobs, settings, and provider status. |
| `noto` CLI/JSON | Direct automation and agent interface. |
| `noto serve` | Backend source of truth for storage, provider jobs, search, and API access. |
| AssemblyAI | Production transcription and diarization provider. |
| Native macOS capture helper | Permission-bound recording engine and minimal capture status. |

## Active Scope

- Record microphone and supported system/selected-app audio on macOS.
- Import existing audio.
- Upload completed recordings to the backend flow.
- Produce normalized diarized transcript JSON.
- Produce summary JSON and Markdown.
- Link decisions and action items to transcript segment IDs.
- Maintain persistent speaker profiles across meetings.
- Search meetings with SQLite FTS5 on the backend.
- Expose stable JSON commands and HTTP/SSE endpoints for scripts and agents.
- Browse meetings, summaries, transcripts, actions, and provider status in a keyboard-first TUI.

## Non-Goals

- No meeting bot.
- No GUI-first app. The TUI is the main interface.
- No live transcript requirement.
- No live diarization requirement.
- No hosted audio capture.
- No local/offline transcription engine.
- No local model management.

## Quality Bar

- Backend artifacts remain normalized and provider-independent.
- Provider payloads are debug data, not downstream product contracts.
- Summary claims cite transcript segment IDs.
- Search results include meeting, segment, speaker, and timestamp.
- Speaker profile mappings distinguish generic provider labels from persistent people.
- Raw audio retention follows explicit backend policy.
- Every user-visible command that mutates data validates schemas before commit.
- Every important user or agent workflow has JSON/API validation coverage.

## Related

- [Cloud architecture](../architecture/cloud-architecture.md)
- [Artifact reference](./artifacts.md)
- [CLI reference](./cli.md)
- [Build plan](../guides/build-plan.md)

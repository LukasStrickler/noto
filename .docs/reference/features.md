# Human And Agent Feature Alignment

## Purpose

Keep V1 focused on the two real users:

- Humans use `noto` as a terminal TUI.
- Agents use `noto --json` and local artifact paths.

The native macOS component is a capture helper only. It is not the product UI.

## Human V1 Features

| Feature | Human outcome | Interface |
| --- | --- | --- |
| Open meeting memory | See recent meetings, recording state, jobs, and index health immediately. | `noto` TUI dashboard |
| Record a meeting | Start, monitor, stop, and ingest local split-source recording. | TUI recorder pane, `noto record`, `noto stop` |
| Import audio | Bring existing recordings into the same artifact flow. | TUI import action, `noto import-audio` |
| Import transcript | Load fixture/manual transcripts without provider access. | TUI import action, `noto import-transcript` |
| Transcribe | Turn recorded/imported audio into normalized diarized JSON. | TUI jobs pane, `noto transcribe` |
| Summarize | Produce cited summaries, decisions, actions, risks, and open questions. | Meeting detail, `noto summarize` |
| Review transcript | Scroll long transcripts with stable speaker labels, timestamps, and segment IDs. | Transcript view |
| Identify speaker source | See likely mic/local or system/participant origin before manual rename. | Transcript view, search results |
| Rename speakers | Replace generic labels with display names without losing source labels. | Transcript/detail command |
| Search | Find segments and jump to the source context. | Search pane, `noto search` |
| Verify local data | Check schemas, checksums, source roles, retention, and index state. | Storage pane, `noto verify --json` |

## Agent V1 Features

| Feature | Agent outcome | Interface |
| --- | --- | --- |
| List meetings | Discover available meeting IDs and current versions. | `noto list --json` |
| Search meetings | Retrieve relevant cited transcript segments. | `noto search --json <query>` |
| Fetch transcript | Read normalized source evidence. | `noto transcript --json <meeting_id>` |
| Fetch summary | Use summary as orientation, not ground truth. | `noto summary --json <meeting_id>` |
| Fetch actions | Extract action items with evidence segment IDs. | `noto actions --json <meeting_id>` |
| Locate files | Read local artifacts directly when useful. | `noto files --json <meeting_id>` |
| Check state | See active recording/job/index state without parsing UI text. | `noto status --json` |
| Verify data | Detect schema, checksum, source-role, path, retention, and stale-index failures. | `noto verify --json` |

## Agent Response Contract

Agent-facing JSON should be boring and stable:

- Stable IDs: `meeting_id`, `version_id`, `segment_id`, `speaker_id`.
- Human citation fields: meeting title, speaker display label, source role, timestamp, segment ID.
- Local paths when available: `manifest_path`, `version_path`, `transcript_json_path`, `transcript_markdown_path`, `summary_json_path`, `summary_markdown_path`.
- Status fields: `recording_state`, `job_state`, `index_state`, `schema_valid`, `checksum_valid`.
- Speaker/source fields: `source_role`, `audio_source`, `channel`, and `speaker_origin` when known.
- Error shape from [cli.md](./cli.md), with machine-readable error codes.

Agents should never need the TUI, a local HTTP server, or remote storage for V1.

## Validation Alignment

Every human feature should have an agent-readable validation path:

| Feature group | Human confidence signal | Agentic validation |
| --- | --- | --- |
| Recording | Recorder pane shows elapsed time, source meters, retention, and ingest result. | Status, verify, and files JSON commands |
| Transcript | Transcript view shows speaker/source labels and segment timestamps. | `noto transcript --json <meeting_id>` plus schema validation. |
| Summary | Summary claims show evidence context. | `noto summary --json <meeting_id>` and evidence segment existence checks. |
| Search | Search pane opens cited transcript segments. | `noto search --json <query>` golden tests over fixtures. |
| Agent handoff | TUI shows local paths and copyable commands. | `noto files --json <meeting_id>` returns existing readable paths. |

Acceptance gates are defined in [testing.md](./testing.md).

## Product Boundaries

| In V1 | Not in V1 |
| --- | --- |
| Terminal TUI as primary UI | GUI-first app |
| Native capture helper behind CLI/TUI with mic/system source separation | Full macOS app workflow |
| Cloud STT provider adapters | Local transcription default |
| Local JSON/Markdown artifacts | Required hosted backend |
| CLI JSON and file paths for agents | Required local HTTP server |
| SQLite FTS5 local search | Server-side search |
| Explicit foreground processing jobs | Detached transcription worker |

## Design Implications

- The TUI must make recording state obvious without becoming a recorder-only screen.
- The TUI must show mic/system source health because source separation is the primary local-vs-remote speaker hint.
- Every TUI action that matters should have a CLI equivalent or JSON-readable result.
- Every summary claim should be traceable to transcript segment IDs.
- The TUI can be expressive, but the agent contract must be deterministic.
- Artifact paths are a first-class product feature, not an implementation leak.
- Recording animation and audio meters must follow [design.md](../design.md) so recording state is obvious without requiring a GUI.

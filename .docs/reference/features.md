# Human And Agent Feature Alignment

## Purpose

Keep implementation aligned around two users:

- Humans use the `noto` terminal TUI.
- Agents use JSON commands and HTTP/API contracts.

The native macOS component is a capture helper only. It is not the product UI.

## Human Features

| Feature | Human outcome | Interface |
| --- | --- | --- |
| Open meeting memory | See recent meetings, recording state, jobs, and index health. | TUI dashboard |
| Record a meeting | Start, monitor, stop, and process recording. | TUI recorder pane, `noto record`, `noto stop` |
| Import audio | Bring existing recordings into the backend processing flow. | `noto import-audio <path>` |
| Transcribe | Turn recorded/imported audio into normalized diarized JSON. | Backend job via TUI/CLI/API |
| Summarize | Produce cited summaries, decisions, actions, risks, and open questions. | Meeting detail, CLI/API |
| Review transcript | Scroll long transcripts with stable speaker labels, timestamps, and segment IDs. | Transcript view |
| Manage speaker profiles | Name, merge, confirm, and reuse speakers across meetings. | Detail/config flows |
| Search | Find segments and jump to source context. | Search pane, `noto search` |

## Agent Features

| Feature | Agent outcome | Interface |
| --- | --- | --- |
| List meetings | Discover meeting IDs and status. | `noto list --json` / API |
| Search meetings | Retrieve cited transcript segments. | `noto search --json <query>` / API |
| Fetch transcript | Read normalized source evidence. | `noto transcript --json <meeting_id>` / API |
| Fetch summary | Use summary as orientation, not ground truth. | `noto summary --json <meeting_id>` / API |
| Check state | See active recording/job/index state. | `noto status --json` / API |
| Verify data | Detect checksum and schema failures. | `noto verify` / `POST /v1/storage/verify` |

## Agent Response Contract

Agent-facing JSON should be boring and stable:

- Stable IDs: `meeting_id`, `segment_id`, `speaker_id`, `speaker_profile_id`.
- Human citation fields: meeting title, speaker display label, timestamp, segment ID.
- Status fields: `recording_state`, `job_state`, `index_state`, `schema_valid`, `checksum_valid`.
- Speaker fields: provider label, profile ID, display name, and match status when available.
- Error shape from [cli.md](./cli.md), with machine-readable error codes.

## Product Boundaries

| In scope | Out of scope |
| --- | --- |
| Terminal TUI as primary UI | GUI-first app |
| Remote-capable backend source of truth | Local/offline transcription engines |
| AssemblyAI production transcription | Provider-shopping architecture |
| Backend search and storage | Client-side data source of truth |
| Persistent speaker profiles | Treating per-meeting labels as people |

Acceptance gates are defined in [testing.md](./testing.md).

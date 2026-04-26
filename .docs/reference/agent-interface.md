# Agent Interface Reference

## Goal

Agents retrieve and cite meeting data locally through `noto --json` commands or direct artifact reads. Noto may later generate `~/Noto/SKILL.md` from this contract.

## Access Order

1. `noto --json` commands.
2. Local artifact paths returned by CLI commands.
3. Local JSON/Markdown artifacts under `~/Noto`.
4. Remote storage only after explicit post-V1 sync support exists.

## Commands

```text
noto list --json --limit 20
noto search --json "pricing"
noto transcript --json <meeting_id>
noto summary --json <meeting_id>
noto actions --json <meeting_id>
noto files --json <meeting_id>
noto status --json
noto verify --json
noto verify --json <meeting_id>
```

JSON responses that identify a meeting should include stable IDs, source-role
hints, and relevant local paths when available: `manifest_path`,
`version_path`, `transcript_json_path`, `transcript_markdown_path`,
`summary_json_path`, and `summary_markdown_path`.

## Citation Format

```text
Product architecture sync, Speaker 1, 00:14:02, seg_000210
```

Use `display_name` only when present. Do not infer people from generic speaker labels.

Use `source_role` when present. `local_speaker` usually means the user's microphone; `participants` usually means system/app audio from the meeting.

## Validation Workflow

Agents should validate before relying on meeting memory:

1. Run `noto status --json` to check active recording and job state.
2. Run `noto verify --json` or `noto verify --json <meeting_id>` before reporting from local artifacts.
3. Use `noto search --json` for broad questions.
4. Fetch transcript or summary JSON and cite segment IDs.
5. Report validation failures with the command, error code, and affected path or meeting ID.

The full validation contract lives in [testing.md](./testing.md).

## Agent Rules

Agents should:

- search first for broad questions
- use summaries for orientation
- verify important claims against transcript segments
- cite meeting, speaker, timestamp, and segment ID
- preserve source-role distinctions when summarizing who said what
- say what was searched when evidence is missing

Agents must not:

- invent decisions, owners, dates, or action items
- collapse local speaker and participant speech when source roles are available
- treat summaries as ground truth when transcripts exist
- upload transcripts externally without explicit user permission
- read raw audio unless explicitly requested
- write artifacts unless explicitly requested

## Local HTTP

Not in V1. Add only if CLI JSON and direct artifact reads are insufficient.

Rules:

- bind to `127.0.0.1`
- require bearer token
- store token in Keychain or mode-600 config
- never expose raw audio by default

Endpoints:

```text
GET /v1/health
GET /v1/meetings?limit=20
GET /v1/search?q=...
GET /v1/meetings/{id}/transcript
GET /v1/meetings/{id}/summary
```

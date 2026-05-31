# Agent Interface Reference

## Goal

Agents retrieve and cite meeting data through stable JSON commands and the
backend HTTP API. Never scrape TUI output.

## Access Priority

1. `noto --json` commands — preferred; stable contract, machine-readable errors.
2. Backend HTTP API — for richer access or when the CLI is unavailable.
3. Artifact file paths — only when returned explicitly by an API/CLI response.

## CLI Commands for Agents

```bash
# Discovery
noto status                          # health + recording state + recent jobs
noto list --json                     # all meetings (id, title, status, counts)
noto list --json --limit 20

# Per-meeting data
noto show <meeting_id> --json        # metadata + counts
noto transcript <meeting_id> --json  # diarized transcript with segment IDs
noto summary <meeting_id> --json     # structured summary + markdown
noto files <meeting_id>              # on-disk paths for all artifacts
noto agent <meeting_id>              # paths + copyable noto commands

# Search
noto search --json "pricing decision"

# Diagnostics
noto verify                          # enqueue checksum verify job for all meetings
noto jobs --json                     # recent jobs with status and progress
noto ping                            # health check
```

## Citation Format

```
<meeting_title>, <speaker_display_name>, <timestamp>, <segment_id>

Example:
  Product sync, Maya, 00:14:02, seg_000210
```

- Use `display_name`, not raw provider labels (`speaker_0`, `speaker_1`).
- Always include `segment_id` when citing transcript evidence.
- Use `speaker_profile_id` when available for cross-meeting identity.
- Do not infer people from unmatched speaker labels.

## Validation Workflow

Before relying on meeting data, validate:

```bash
noto status                          # check no job is mid-flight
noto verify                          # enqueue checksum check; poll noto jobs
noto search --json "key topic"       # broad search first
noto transcript <id> --json          # verify specific claims
```

Report validation failures with: the command run, the error code, and the affected meeting ID.

## HTTP API

Base URL: `NOTO_API_URL` or `http://unix:/path/to/api.sock` for local daemon.
Authentication: `Authorization: Bearer <NOTO_API_TOKEN>` (TCP mode only).

### Meetings

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/meetings` | List meetings. Query params: `q`, `limit`, `since`. |
| `GET` | `/v1/meetings/{id}` | Single meeting metadata + counts. |
| `DELETE` | `/v1/meetings/{id}` | Delete meeting and its search index entry. |
| `GET` | `/v1/meetings/{id}/transcript` | Diarized transcript with segments and speakers. |
| `GET` | `/v1/meetings/{id}/summary` | Structured summary (decisions, actions, risks, questions) + markdown. |
| `GET` | `/v1/meetings/{id}/files` | On-disk artifact paths. |
| `GET` | `/v1/meetings/{id}/agent` | Agent handoff: file paths + CLI commands. |
| `POST` | `/v1/meetings/{id}/verify` | Enqueue a checksum-verify job for this meeting. |
| `GET` | `/v1/meetings/{id}/speaker-mappings` | Per-meeting speaker→profile mappings. |
| `PATCH` | `/v1/meetings/{id}/speaker-mappings` | Update speaker label→profile assignments. |

### Search

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/search` | FTS5 search. Query params: `q`, `speaker`, `scope`, `limit`. |

### Recording

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/recording` | Current recording state. |
| `POST` | `/v1/recording/start` | Start recording. Body: `{title, sources, after_stop}`. |
| `POST` | `/v1/recording/stop` | Stop recording and kick pipeline. |
| `POST` | `/v1/recording/pause` | Pause (macOS helper only). |
| `POST` | `/v1/recording/resume` | Resume (macOS helper only). |
| `POST` | `/v1/recording/marker` | Drop a labeled marker. Body: `{label}`. |
| `GET` | `/v1/recording/preflight` | Check capture helper availability. |
| `GET` | `/v1/recording/meters` | SSE stream of live audio levels. |

### Jobs

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/jobs` | List jobs. Query params: `status`, `meeting_id`, `limit`. |
| `POST` | `/v1/jobs` | Create a job. Body: `{kind, meeting_id, options}`. |
| `GET` | `/v1/jobs/{id}` | Single job. |
| `DELETE` | `/v1/jobs/{id}` | Cancel a queued or running job. |
| `GET` | `/v1/jobs/events` | SSE stream of all job and recorder events. |

### Imports

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/v1/imports/audio` | Import an audio file. Body: `{path, title}`. Returns meeting + job. |

### Speaker profiles

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/speaker-profiles` | List all speaker profiles. |
| `POST` | `/v1/speaker-profiles` | Create a speaker profile. |
| `GET` | `/v1/speaker-profiles/{id}` | Single profile. |
| `PATCH` | `/v1/speaker-profiles/{id}` | Update profile (display name, etc.). |
| `DELETE` | `/v1/speaker-profiles/{id}` | Delete profile. |
| `POST` | `/v1/speaker-profiles/{id}/merge` | Merge source profile into this one. |

### Providers

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/providers` | List providers with key status. |
| `POST` | `/v1/providers/{id}/key` | Set provider API key. Body: `{value}`. |
| `DELETE` | `/v1/providers/{id}/key` | Remove provider API key. |
| `POST` | `/v1/providers/{id}/test` | Test provider connectivity. |
| `PUT` | `/v1/providers/{id}/active-speech` | Set active speech provider. |

### Config

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/config` | Current config. |
| `PATCH` | `/v1/config` | Update config fields. |
| `GET` | `/v1/config/paths` | Filesystem paths (config dir, recordings dir, socket). |

### Storage

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/storage` | Storage summary (meeting count, index state, dir). |
| `POST` | `/v1/storage/verify` | Enqueue full-storage checksum verify job. |
| `POST` | `/v1/storage/reindex` | Enqueue full FTS5 reindex job. |

### Agent API (optimised for AI agents)

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/agent/meetings` | Cursor-paginated list. Params: `limit`, `after` (RFC3339), `before` (RFC3339). Each item includes `short_summary` and counts — no extra round-trip needed for overview. Response includes `next_cursor` for the next page. |
| `GET` | `/v1/agent/meetings/{id}` | Full meeting context: metadata + structured summary with `segment_refs` + full transcript with speaker names pre-resolved. Single call for "read and cite". |

Use these instead of `/v1/meetings` when building agent workflows — they save
round-trips and return everything in citation-ready form.

### Health

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/healthz` | Server health: `{ok, version, uptime_sec, recording_active}`. |

## SSE Events (`/v1/jobs/events`)

```json
{ "kind": "job",      "job":      { "id": "...", "status": "running", "phase": "transcribing", "progress": 0.42 } }
{ "kind": "recorder", "recorder": { "active": true, "elapsed_sec": 72, "mic_db": -18, "participant_db": -24 } }
{ "kind": "meter",    "meter":    { "mic_db": -18, "participant_db": -24, "clip": false } }
{ "kind": "status_bar","status_bar":{ "recording_active": true, "running_jobs": 1, "total_meetings": 12 } }
```

## Agent Rules

**Do:**
- Search first for broad questions; use summaries for orientation
- Verify important claims against transcript segments with segment IDs
- Cite meeting title, speaker display name, timestamp, and segment ID
- Report validation failures explicitly

**Don't:**
- Invent decisions, owners, or action items
- Treat summaries as ground truth when transcripts exist
- Upload transcripts externally without user permission
- Read raw audio unless explicitly requested

# User Stories

## Purpose

Use these stories to drive implementation order and acceptance tests. Each story
starts with a failing fixture, schema test, golden CLI output, HTTP contract test,
or TUI model test before implementation.

## Stories

- US-01: Open a keyboard-first TUI connected to a backend.
  Acceptance: model/snapshot tests cover dashboard, meeting list, search,
  detail, transcript, and settings views from API-backed data.
- US-02: Start and stop a meeting recording from the TUI or CLI.
  Acceptance: capture lifecycle tests prove record/stop and backend ingest.
- US-03: Import existing audio and process it like a meeting.
  Acceptance: import creates meeting/audio records, enqueues processing when
  requested, and never mutates the source file.
- US-04: Transcribe a recorded or imported meeting through AssemblyAI.
  Acceptance: completed audio produces normalized diarized transcript JSON.
- US-05: Build and reuse speaker profiles across meetings.
  Acceptance: repeated speaker fixtures map to the same profile; ambiguous
  matches remain pending instead of auto-merging.
- US-06: Read a transcript with stable speaker labels and timestamps.
  Acceptance: transcript JSON validates, Markdown renders, and speaker mapping
  changes do not lose provider labels.
- US-07: Search all meetings through the backend.
  Acceptance: search returns meeting ID, segment ID, speaker, timestamp, and snippet.
- US-08: Answer from meeting memory with citations.
  Acceptance: agent workflow tests use JSON/API, verify data, cite segment IDs,
  and do not edit current records in place.

## Workflow Tests

| Workflow | Test fixture | Required validation |
| --- | --- | --- |
| Recording lifecycle | Recording and restart recovery | Status, verify, and ingest checks |
| Backend API | HTTP client against test server | Auth, JSON shape, SSE events |
| TUI navigation | Keyboard-only fixture flow | Model and snapshot tests |
| AssemblyAI normalization | Mock AssemblyAI response | Transcript schema and speaker labels |
| Speaker profiles | Two meetings with repeated speaker vectors | Stable profile mapping and pending threshold behavior |
| Search scale | Synthetic meetings plus long transcript | Search latency, result shape, and citation fields |
| Agent citations | Answer with real segment IDs | Verify, search, fetch transcript, cite segments |

See [testing.md](./testing.md) for the full validation plan.

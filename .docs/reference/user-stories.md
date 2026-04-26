# User Stories

## Purpose

Use these stories to drive implementation order and acceptance tests. Each story
should start with a failing fixture, schema test, golden CLI output, or TUI
model test before implementation.

## V1 Stories

- US-01: Open a keyboard-first TUI for local meeting memory.
  Acceptance: model/snapshot tests cover dashboard, meeting list, search,
  detail, transcript, and settings views from local artifacts.
- US-02: Start and stop a meeting recording from the TUI or CLI.
  Acceptance: capture lifecycle tests prove record/stop and split source-role
  ingest.
- US-03: Keep recording if the TUI exits.
  Acceptance: recovery tests prove the native helper owns active capture,
  exposes status, and can finish partial files after restart.
- US-04: Transcribe a recorded or imported meeting.
  Acceptance: completed audio creates `audio.json`, runs the configured STT
  provider, and writes valid `transcript.diarized.json`.
- US-04a: Distinguish the local speaker from meeting participants.
  Acceptance: mic segments default to local speaker, system/app audio defaults
  to participants, and speaker rename preserves source origins.
- US-05: Import existing audio and process it like a meeting.
  Acceptance: import creates `meeting.json` and `audio.json`, runs foreground
  processing when requested, and never mutates the source file.
- US-06: Import an existing transcript fixture.
  Acceptance: import creates valid transcript JSON, renders Markdown, and
  appears in the TUI without provider access.
- US-07: Read a transcript with stable speaker labels and timestamps.
  Acceptance: transcript JSON validates, Markdown renders, and speaker rename
  creates a new version.
- US-08: Search all meetings locally without a remote service.
  Acceptance: search returns meeting ID, segment ID, speaker, timestamp, source
  role when known, and snippet.
- US-09: Replace the default transcription provider.
  Acceptance: provider config changes while the same fixture still produces
  valid `transcript.v1`; summary and search still work.
- US-10: Answer from local meeting memory with citations.
  Acceptance: agent workflow tests use CLI JSON or artifact paths, verify
  artifacts, cite segment IDs, and do not edit current versions in place.
- US-11: Record, import, search, and read previous meetings offline.
  Acceptance: network failures do not block local recording, local search,
  artifact reads, or transcript fixture workflows.

## Workflow Tests

| Workflow | Test fixture | Required validation |
| --- | --- | --- |
| Recording lifecycle | Split-source recording and restart recovery | Status, verify, and source-role checks |
| Source attribution | Mic/system fixture | Local-speaker and participant precision/recall |
| TUI navigation | Keyboard-only fixture flow | Model and snapshot tests |
| Provider parity | Same 20-30 minute audio through two STT processors. | Both outputs pass the same `transcript.v1` contract tests. |
| Search scale | 1,000 synthetic meetings plus one 2-hour real transcript. | Search latency, result shape, and citation fields are stable. |
| Agent citations | Answer with real segment IDs | Verify, search, fetch transcript, cite segments |

See [testing.md](./testing.md) for the full TDD fixture plan and phase gates.

## Out Of Scope For V1

- Object-store sync.
- Hosted or self-hosted gateway.
- Live meeting bot.
- Live transcript editing.
- Mobile capture.
- Shared real-time workspace editing.

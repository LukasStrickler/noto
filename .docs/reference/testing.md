# TDD And Agentic Validation Reference

## Purpose

Use tests and fixtures to prove each V1 feature works before expanding scope. A
feature is not done because the TUI looks right; it is done when local artifacts
validate, CLI JSON is stable, search is rebuilt from artifacts, and an agent can
verify the result without scraping terminal UI text.

## Principles

- Write the fixture and failing test before implementing a feature.
- Validate artifact contracts at every mutating boundary.
- Prefer deterministic fixture tests over provider-dependent tests.
- Keep provider/network tests behind explicit integration flags.
- Expose verification through JSON commands so agents can run checks and explain failures.
- Treat TUI tests as behavior tests over state, keys, and rendered snapshots, not as manual screenshots only.

## Validation Layers

| Layer | Proves | Required before done |
| --- | --- | --- |
| Unit tests | Parsers, IDs, checksums, source-role mapping, normalizers, command output shape. | Every package with branching logic. |
| Schema tests | Core JSON artifacts and benchmark results. | Every artifact writer and provider normalizer. |
| Golden fixture tests | Markdown rendering, search snippets, CLI JSON, summary evidence, TUI snapshots. | Every user-visible output format. |
| Integration tests | Record/import/transcribe/summarize/index flows against local fixture roots. | Every V1 workflow. |
| Capture tests | macOS helper lifecycle, permission states, split mic/system receipt, TUI exit recovery. | Recording V1. |
| Benchmark tests | Provider WER/DER, source attribution, latency, cost, schema validity. | Default provider selection. |
| Agentic tests | Agent can use `--json` and file paths to answer with citations and verify state. | Agent-facing feature completion. |

## Fixture Set

Keep fixtures small, explicit, and versioned.

| Fixture | Contents | Used by |
| --- | --- | --- |
| `empty-root` | Empty `~/Noto` layout. | First-run TUI, list/search empty states. |
| `one-meeting-transcript` | Valid meeting, diarized transcript, summary, checksums. | CLI JSON, rendering, search, agent citation tests. |
| `split-source-recording` | Consent-safe short recording receipt with mic/system source roles. | Capture ingest, speaker origin, source attribution tests. |
| `missing-word-times` | Transcript with segment timestamps but missing word timestamps. | Provider normalization and graceful UI display. |
| `overlap-speakers` | Overlapping speech with `overlap=true`. | Diarization display, summary evidence, search. |
| `deleted-audio` | Valid transcript with raw audio removed by retention policy. | Retention, reprocessing errors, agent file listing. |
| `invalid-artifact` | Broken schema and checksum variants. | Verify command and failed commit tests. |
| `large-root` | 1,000 meetings and one 2-hour transcript. | Search latency, TUI responsiveness, index rebuild. |

Fixtures that include private audio or transcript text must live outside public testdata unless explicitly redacted.

## Agentic Validation Commands

Agents should be able to run these commands and decide whether a build is valid without reading the TUI:

```text
noto status --json
noto verify --json
noto verify --json <meeting_id>
noto files --json <meeting_id>
noto transcript --json <meeting_id>
noto summary --json <meeting_id>
noto search --json "pricing decision"
noto index rebuild --json
noto benchmark run --dataset fixture --provider <provider_id> --json
```

Minimum JSON fields for validation commands:

```json
{
  "ok": true,
  "schema_valid": true,
  "checksum_valid": true,
  "index_valid": true,
  "recording_state": "idle",
  "job_state": "idle",
  "meeting_count": 12,
  "errors": []
}
```

Errors must use the CLI error contract and include a stable `code`, a human `message`, and a `details` object that points to the failing file, field, command, or fixture.

## V1 Acceptance Matrix

| Feature | Test first | Done when |
| --- | --- | --- |
| Artifact core | Schema and checksum fixture tests. | Invalid writes never promote; valid versions render JSON and Markdown. |
| Import audio | Fixture import integration test. | Import creates `meeting.json` and `audio.json` without mutating source input. |
| Import transcript | Transcript fixture test. | Imported transcript validates, renders Markdown, appears in list/search. |
| Transcription provider | Provider normalizer contract test. | Provider output becomes standard `transcript.diarized.json` with source-role behavior documented. |
| Summary | Golden summary evidence test. | Every decision/action/open question cites valid segment IDs. |
| Search | Golden query tests over fixture index. | Results include meeting ID, segment ID, speaker, timestamp, snippet, and source role when known. |
| Agent access | Agent workflow test. | Agent can answer from CLI JSON/file paths with citations and no UI scraping. |
| TUI dashboard | Bubble Tea model and snapshot tests. | Keyboard-only navigation works; idle state does not tick. |
| Recording | Capture lifecycle integration test. | `noto record`/`noto stop` survive TUI exit and ingest split mic/system source roles. |
| Retention | Deleted-audio fixture test. | Raw audio deletes only after transcript validation and policy approval. |

## Phase Gates

Each build-plan phase needs a green validation gate before the next phase starts:

| Phase | Gate |
| --- | --- |
| 0. Validation spike | Benchmark runner writes valid `benchmark-result.v1` and scores one fixture. |
| 1. Artifact core | `noto verify --json` passes on valid fixtures and fails predictably on invalid fixtures. |
| 2. Search and agent access | Agent workflow test answers from search/transcript JSON with segment citations. |
| 3. Providers and summaries | Two processors pass the same output contract tests and summaries cite valid segments. |
| 4. TUI and agent CLI | TUI model tests, snapshot tests, idle tick test, and CLI JSON golden tests pass. |
| 5. macOS capture helper | Capture lifecycle test proves split-source recording, TUI exit recovery, ingest, and retention. |

Post-V1 phases must add their own parity gates before touching V1 behavior.

## Definition Of Done

A V1 feature is done only when:

- Its acceptance test exists before or with the implementation.
- `go test ./...` passes.
- Fixture validation passes for the affected artifact types.
- CLI JSON output has a golden test or schema test.
- Mutating commands prove they do not promote invalid partial output.
- Agentic validation commands can detect success and failure states.
- Docs link the feature to its acceptance gate.

## Failure Triage

Use this order when an agent or developer sees a failure:

1. Run `noto verify --json` to identify schema, checksum, index, or path failures.
2. Run the narrow fixture test for the failing artifact or command.
3. Rebuild the index only after artifact checks pass.
4. Re-run search and agent workflow tests.
5. Run provider or capture integration tests only when local deterministic tests pass.

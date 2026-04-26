# Noto: Full Product Implementation Plan

## TL;DR

> **Summary**: Build a complete terminal-first meeting recorder, transcriber, summarizer, and searchable memory tool. Noto's TUI skeleton and provider registry exist; everything else — artifact storage, search, S3 adapter (written but not activated), recording, CLI commands, and a lean 5-screen TUI — must be built from scratch.
> **Deliverables**: Production-ready `noto` binary; Cloudflare R2/S3 adapter (local-only V1); SQLite FTS5 search; 5-screen Bubble Tea TUI (Dashboard, Meetings, Meeting Detail, Transcript, Search); macOS audio capture helper.
> **Effort**: XL (multi-month, ~15 core tasks + F1-F4 verification)
> **Parallel**: YES — 4 waves
> **Critical Path**: Artifact core → Search+Providers → TUI → Recording → Integration

---

## Context

### Original Request

User requested a "comprehensive refactor plan" for Noto — a terminal-first meeting recorder/transcriber/summarizer/searchable memory tool. The plan must cover all V1 functionality (currently partially designed but not implemented), actual meeting recording, S3 adapters (Cloudflare R2 compatible), and a complete reworked TUI. The user explicitly stated: "do much research before starting this and plan deeply then ulw" — extensive research was completed before this plan.

### Interview Summary

All research phase decisions confirmed (7 user choices incorporated: Mistral via OpenRouter, AAC transcoding, WAL+rollback, Unix domain socket IPC, prompt versioning in `versions/{vid}/prompts/`, speaker rename creates minor version, recording state machine). Wave structure corrected. Storage/Providers/ Settings screens cut from TUI.

### Metis Review (gaps addressed)

All 7 user-confirmed decisions incorporated: Mistral via OpenRouter, AAC transcoding, WAL+rollback, Unix domain socket IPC, prompt versioning in `versions/{vid}/prompts/`, speaker rename creates minor version, recording state machine. Metis also identified: wave structure corrected, Storage/Providers/Settings screens cut.

---

## Product Vision

**Noto is your terminal's meeting memory — record, transcribe, search, and recall with confidence, local-first.**

Noto captures meetings via a macOS native helper, produces portable artifact JSON/Markdown, and lets you browse and search from a keyboard-first TUI. Agents read the same data through CLI JSON commands and local file paths.

---

## Target User

| User | V1 Need |
| ---- | ------- |
| **Individual contributor / technical role** | Capture design decisions, sprint plannings, vendor calls — recall what was decided and who said it |
| **Agent** | Retrieve cited meeting context without a server or API |

**V1 persona**: A developer or product manager who has macOS, spends half their day in meetings, wants to reference decisions without scrolling Slack, and may build automations on top of meeting data.

---

## Core Workflow

```
User opens TUI
    │
    ├─> starts recording (macOS capture splits mic + system audio)
    │       │
    │       └─> talks in meeting, TUI shows live meters
    │       │
    │       └─> stops recording
    │
    ├─> noto ingests audio → runs transcription → produces summary
    │       (all foreground, no detached workers)
    │
    ├─> browses meeting list, opens detail to see:
    │       short summary, decisions [with segment citations], action items
    │
    └─> searches ("api pricing decision") → jumps to cited segment

Agent workflow:
    noto list --json → noto search --json "api" → reads ~/Noto/meetings/.../transcript.diarized.json
```

---

## Screen Inventory — 5 Screens

### Screen 1: Dashboard
**Primary Question**: What's recording? What meetings exist? Is the system healthy?

**Contents**: Recording state badge (`idle` / `recording [HH:MM:SS]` / `processing`), today's meeting/action/decision count as text stats (NOT bar charts), recent meetings list (last 5), index health line (`clean`/`dirty`), bottom bar: `r` record, `i` import, `/` search, `:` command, `?` help, `q` quit.

```
┌─ Noto ─────────────────────────────────────────────┐
│  rec: idle   index: clean                          │
│                                                    │
│  today   3 meetings   7 actions   2 decisions      │
│                                                    │
│  recent                                              │
│  ▸ Roadmap sync          42m  summarized            │
│  ▸ Vendor benchmark      28m  summarized             │
│  ▸ Sprint planning       15m  recorded              │
│                                                    │
│  ? help   r record   i import   / search   q quit  │
└────────────────────────────────────────────────────┘
```

### Screen 2: Meetings
**Primary Question**: Which meeting do I want? What's in it?

**Contents**: Virtual-scrolling list (1000+ meetings), each row: `title   duration   date   D:N A:N R:N`. `/` starts inline filter, `s` cycles sort, `d` delete (confirm), `n` new, `enter` open, `esc` back. vim navigation (`j/k`/`↑↓`, `gg`/`G`).

```
┌─ Meetings ────────────────────────────────────────┐
│  /filter_______________________________  s:sort  │
│                                                    │
│  Roadmap sync          42m  Apr 24  D:3 A:5 R:1  │
│  Vendor benchmark      28m  Apr 23  D:1 A:2 R:0  │
│  Sprint planning       15m  Apr 23  D:0 A:3 R:0  │
│  Architecture review   1h 2m  Apr 22  D:5 A:8 R:2  │
│  ...                                            │
│                                                    │
│  ↑↓ navigate   enter open   d delete   n new    │
└──────────────────────────────────────────────────┘
```

### Screen 3: Meeting Detail
**Primary Question**: What's the bottom line? Show me the evidence.

**Contents**: Header (title, date, duration, speakers). Short summary (2 sentences). Numbered Decisions (green, with `[seg_XXXXXX]` citation). Action Items (`@person` format, NOT tracked completed). Risks (red/muted). Open Questions (yellow/muted). `t` expands Transcript, `f` shows artifact paths overlay.

**Evidence contract**: Every decision/action/risk cites `segment_id` from `transcript.diarized.json`. If citation unavailable, item flagged `evidence: []` — not silently committed.

```
┌─ Product architecture sync ────────────────────┐
│ Apr 24, 2026   42m   2 speakers                 │
│                                                │
│ Settled on local-first MVP with post-meeting   │
│ diarization. Planning to benchmark both STT   │
│ providers before defaulting.                   │
│                                                │
│ ▸ Decisions (2)                                │
│   1. Use post-meeting diarization for V1       │
│      [seg_000210: "post-meeting diarization"] │
│   2. Keep artifacts local-first                │
│      [seg_000220: "local-first artifacts"]     │
│                                                │
│ ▸ Action items (1)                             │
│   • Run first provider benchmark suite         │
│     [seg_000245: "benchmark both providers"]   │
│                                                │
│ ▸ Risks (1)                                    │
│   • Local transcription may exceed V1 latency  │
│     target                                     │
│                                                │
│ t transcript   f files   : command   ← back    │
└────────────────────────────────────────────────┘
```

### Screen 4: Transcript
**Primary Question**: What was said, by whom, when?

**Contents**: Virtual-scrolling transcript. Entry: `timestamp   SpeakerLabel [source_role]   seg_XXXXXX`. Text wrapped to terminal width, cached per-width. `f` follow (jump to meeting), `c` copy citation, `esc` back.

```
┌─ Transcript: Product architecture sync ───────┐
│ 00:00:00  Speaker 1 [local_speaker]  seg_000001│
│   Let's start with the roadmap.               │
│                                                │
│ 00:00:42  Speaker 0 [participants]  seg_000002│
│   I think we should consider a local-first    │
│   approach since users are privacy-sensitive. │
│                                                │
│ 00:01:15  Speaker 1 [local_speaker]  seg_000003│
│   Agreed. Post-meeting diarization is enough  │
│   for V1 — no need for real-time.            │
│                                                │
│ ← back   f follow   c copy citation            │
└───────────────────────────────────────────────┘
```

### Screen 5: Search
**Primary Question**: Where did this come from? Who said it? When?

**Contents**: fzf-style persistent input. Results: `time   speaker   meeting   text snippet` with highlighted matches. `enter` opens meeting with segment highlighted. `c` copy citation. `tab` cycles scope (all/decisions/actions/transcript). Evidence panel on wide terminals.

```
┌─ Search ──────────────────────────────────────┐
│ > api pricing decision________________________│
│                                                │
│ results   3 matches in 2 meetings             │
│                                                │
│ time      speaker      meeting         text   │
│ 14:22     Speaker 1    Roadmap sync    ...the │
│ 14:28     Speaker 0    Roadmap sync    ...API │
│ 09:15     Speaker 2    Vendor call      ...pri │
│                                                │
│ evidence:                                      │
│  meeting    Roadmap sync                       │
│  segment    seg_000210                         │
│  time       00:14:22                          │
│  text       "post-meeting diarization is      │
│              enough for V1"                   │
│                                                │
│ ← back   enter open   c copy   tab scope       │
└───────────────────────────────────────────────┘
```

---

## What NOT to Build (V1 Scope Cut)

| Cut | Why |
| --- | --- |
| **Settings/Providers/Storage screens** | Via `:` command palette or CLI; Dashboard gets status line only |
| **Separate Search screen** | `/` is global from any screen; no nav to separate screen |
| **Bar charts / decorative graphs** | Text stats ("3 meetings") sufficient; visualization is bloat |
| **Real-time transcription** | Post-meeting only |
| **Detached background workers** | Foreground CLI jobs only |
| **S3/R2 sync activation** | Adapter written but not activated; local-only V1 |
| **Local Whisper** | Cloud STT only in V1 |
| **MCP server** | Post-V1 |

---

## Keybindings

**Modeless. Single-letter. Always-visible bottom bar.**

### Core (Global)
| Key | Action |
| --- | ------ |
| `?` | Help overlay (context-specific) |
| `:` | Command palette |
| `/` | Open search (global, from any screen) |
| `q` | Back / quit (quit if at root; confirm if job active) |
| `esc` | Cancel operation, close overlay, back |
| `r` | Start recording |
| `i` | Import audio |
| `s` | Cycle sort (in list contexts) |

### Per-Screen
- **Dashboard**: `r` record, `i` import, `↑↓` select meeting, `enter` open
- **Meetings**: `↑↓`/`j/k` navigate, `enter` open, `/` filter, `s` sort, `d` confirm delete, `n` new, `esc` back
- **Meeting Detail**: `↑↓` navigate sections, `t` transcript, `f` files overlay, `enter` toggle section
- **Transcript**: `↑↓` scroll, `f` follow, `c` copy, `esc` back
- **Search**: `enter` open, `c` copy, `tab` cycle scope, `esc` back

### Recording Rules
- `esc` exits TUI but leaves recording alive (native helper owns recording)
- Stop requires confirmation if duration < 10s or capture has errors
- Long jobs show inline progress; `esc` cancels (with confirmation if mutating)

---

## Information Architecture

### Directory Structure
```
~/Noto/
├── config.json                 # artifact root, routing, retention (no keys)
├── SKILL.md                    # agent prompt augmentation
├── prompts/
│   └── summary.v1.md           # LLM prompt template (versioned per version)
├── meetings/
│   └── YYYY/
│       └── MM/
│           └── {meeting_id}/
│               ├── manifest.json              # current version pointer
│               ├── versions/
│               │   └── {version_id}/
│               │       ├── meeting.json
│               │       ├── audio.json
│               │       ├── transcript.diarized.json
│               │       ├── transcript.md
│               │       ├── summary.json
│               │       ├── summary.md
│               │       ├── checksums.json
│               │       └── prompts/summary.v1.md  # prompt version used
│               └── .tmp/                      # staging only, never committed
├── benchmarks/
│   └── runs/{run_id}/benchmark-result.json
└── indexes/
    └── noto.sqlite             # SQLite FTS5 search index
```

### Version Rules
- `manifest.json` points at `current_version_id`
- Version folders are **immutable** after creation; edits create new versions
- Speaker rename → new version, new `manifest.json` checksums
- Partial outputs stay in `.tmp/` — never promoted until all artifacts written and validated

### Search Index
- Index rebuilt from artifacts (not real-time)
- Fields: title, transcript (text + speaker + source_role), decisions, action items, risks, open questions
- Tokenizer: `unicode61`; Ranking: BM25 via SQLite FTS5 `bm25()`; WAL mode + transaction rollback

### CLI Agent Interface
```
noto                        # TUI (default)
noto record --title "..."   # Start recording
noto stop                   # Stop recording
noto import ./audio.m4a --title "..."  # Import audio
noto transcribe --meeting-id <id>       # Transcribe
noto summarize --meeting-id <id>        # Summarize
noto search --json "pricing decision"  # FTS5 JSON search
noto list --json             # List meetings JSON
noto show --json <id>        # Meeting detail JSON
noto verify --json          # Verify checksums
noto config get/set          # Config management
```

---

## Work Objectives

### Core Objective

Ship a production-quality Noto V1 binary that:
1. Records meetings via macOS audio capture helper
2. Transcribes via AssemblyAI or self-hosted Whisper
3. Produces structured `transcript.v1` and `summary.v1` artifacts
4. Stores meetings in `~/Noto/meetings/` with full artifact lineage
5. Provides a polished Bubble Tea TUI with fuzzy search (SQLite FTS5)
6. Syncs to Cloudflare R2 (S3-compatible API)

### Deliverables

- [x] Working `noto` binary with all CLI commands implemented
- [x] Meeting recording: start → capture → stop → save workflow
- [x] Transcription pipeline: audio → segments → normalized transcript → speakers
- [x] Artifact system: manifest, transcript, summary, checksums, versioning
- [x] SQLite FTS5 search across all meetings
- [x] Cloudflare R2/S3 adapter (written, local-only in V1)
- [x] **5-screen Bubble Tea TUI**: Dashboard, Meetings, Meeting Detail, Transcript, Search
- [x] macOS audio capture helper (Swift + Go IPC via Unix domain socket)
- [x] Provider routing: AssemblyAI/Whisper (STT), Mistral via OpenRouter (LLM)
- [x] All testing.md fixtures passing

### Must Have

- Artifact storage with checksums and manifest tracking
- Working CLI: `noto import`, `noto transcribe`, `noto summarize`
- 5-screen Bubble Tea TUI (per task-0 spec — no Settings/Providers/Storage screens)
- SQLite FTS5 with tokenization: speakers, decisions, action items, risks, transcript text
- Cloudflare R2 adapter (local-only V1, sync post-V1)
- Audio capture helper for macOS

### Must NOT Have

- Remote sync gateway (post-V1)
- Web UI or mobile apps (post-V1)
- Video recording (out of scope)
- Multi-user/auth (post-V1)
- AI slop patterns in UI (generic "AI summary" vibes, bot names, etc.)
- Hardcoded provider API keys
- **Separate Settings screen** (via `:` command palette or CLI)
- **Separate Providers screen** (status line on Dashboard only)
- **Separate Storage screen** (`noto verify --json` for agents; Dashboard status line)
- **Bar charts / decorative visualization** (text stats only)
- **Real-time transcription** (post-meeting only)
- **Detached background workers** (foreground CLI jobs only)

---

## Verification Strategy

> ZERO HUMAN INTERVENTION — all verification is agent-executed.

**Test decision**: TDD with tests-after for infrastructure (search, S3 adapter), TDD for TUI components, integration tests for full pipeline.

**Framework**: Go `testing` package + `testify/assert` for unit tests; `spectre.console` for integration test output.

**QA policy**: Every task has agent-executed scenarios (happy path + failure).

**Evidence**: `.sisyphus/evidence/task-{N}-{slug}.{ext}` for each task.

---

## Execution Strategy

### Parallel Execution Waves

Target: 5-8 tasks per wave. Dependencies drive grouping, not convenience.

**Wave 1: Foundation (Artifact Core + Config)**
- Artifact structs and validation
- Config store (YAML + keychain)
- Meeting storage directory layout
- Checksum utilities

**Wave 2: Search + Providers**
- SQLite FTS5 setup and indexing
- Provider routing (AssemblyAI, Whisper, Mistral via OpenRouter)
- LLM summary prompts (versioned in `versions/{vid}/prompts/`)
- Transcript normalizer (speaker merge, confidence, timestamp gaps)

**Wave 3: TUI Redesign**
- Bubble Tea app core (viewport, command palette, semantic color tokens, BubblePup)
- 5 screens implemented (Dashboard, Meetings, Meeting Detail, Transcript, Search)
- Keybinding system (context-specific per lazygit model)
- Recording state machine + elapsed timer

**Wave 4: Recording + CLI**
- macOS audio capture helper (Swift + Go IPC via Unix domain socket)
- CLI commands (import, transcribe, summarize, sync --dry-run, search, config, verify)
- S3/R2 adapter (written, local-only activated in V1)

**Wave 5: Integration + Polish**
- End-to-end recording pipeline
- Fixtures passing
- Benchmarks

### Dependency Matrix

```
Task 1 (Artifact structs)  ──→ Task 5 (Manifest writer)
Task 2 (Config store)      ──→ Task 6 (Storage layout)
Task 3 (Checksums)         ──→ Task 5
Task 4 (SQLite FTS5)       ──→ Task 1, 2
Task 5 (Manifest writer)   ──→ Task 4
Task 7 (Provider routing)  ──→ Task 2
Task 8 (LLM prompts)       ──→ Task 7
Task 9 (Transcript norm)   ──→ Task 1, 7
Task 10 (TUI core)         ──→ Task 2, 4
Task 11 (BubblePup)        ──→ Task 10
Task 12 (Screens)          ──→ Task 10, 11
Task 13 (macOS capture)   ──→ Task 10
Task 14 (S3 adapter)       ──→ Task 1
Task 15 (CLI commands)     ──→ Task 5, 7, 8, 9, 14
Task 16 (Integration)      ──→ Task 15, 12, 13
```

### Agent Dispatch Summary

| Wave | Tasks | Categories |
|------|-------|-------------|
| Wave 1 | 6 | deep + quick |
| Wave 2 | 5 | deep + ultrabrain |
| Wave 3 | 7 | visual-engineering + artistry |
| Wave 4 | 6 | ultrabrain + deep |
| Wave 5 | 4 | unspecified-high |

---

## TODOs

> Implementation + Test = ONE task. Never separate.

- [x] 1. **Artifact System (Core structs + validation)**

  **What to do**: Create all artifact Go structs matching `.docs/reference/artifacts.md` schemas. Implement `ValidateTranscript()`, `ValidateSummary()`, `ValidateManifest()`, `ValidateAudio()` with strict field checks per spec. Add `internal/artifacts/checksum.go` for SHA-256 verification. Implement artifact versioning with forward-compatibility markers (`version` field + `minor`/`major` rules from spec).

  **Must NOT do**: Do not implement storage/writing — only struct definitions and validation. Do not add provider-specific fields not in spec.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: Schema accuracy required, must match spec exactly
  - Skills: [`go-expert`] — Need accurate Go struct field mapping
  - Omitted: [`ui-animation`] — Not UI work

  **Parallelization**: Can Parallel: YES | Wave 1 | Blocks: 5, 6 | Blocked By: —

  **References**:
  - Spec: `.docs/reference/artifacts.md:manifest.v1` — Lines 1-80 (manifest structure)
  - Spec: `.docs/reference/artifacts.md:transcript.v1` — Lines 82-180 (transcript schema with segments, speakers, word-level timing)
  - Spec: `.docs/reference/artifacts.md:summary.v1` — Lines 182-260 (summary with decisions, action_items, risks, open_questions)
  - Spec: `.docs/reference/artifacts.md:audio.v1` — Lines 262-340 (audio metadata)
  - Spec: `.docs/reference/artifacts.md:versions/` — Versioning rules
  - Test: `.docs/reference/testing.md:fixture-set` — Fixture data to pass validation

  **Acceptance Criteria**:
  - [ ] `transcript.v1` validates correctly: segments with speakers, word-level timestamps, confidence scores
  - [ ] `summary.v1` validates: decisions, action_items (with assignee/completed), risks, open_questions
  - [ ] `manifest.v1` validates: meeting_id, source role array, artifact checksums match
  - [ ] `audio.v1` validates: duration, sample_rate, channels, codec
  - [ ] Forward-compatibility: `major: 1, minor: 0` manifests parse without errors
  - [ ] Fixture data in `internal/artifacts/testdata/fixtures/` passes all validators

  **QA Scenarios**:
  ```
  Scenario: Valid transcript.v1 passes validation
    Tool: Bash
    Command: go test ./internal/artifacts/... -run TestValidateTranscript
    Expected: PASS — fixture transcript valid

  Scenario: transcript.v1 with missing segments fails validation
    Tool: Bash
    Command: go test ./internal/artifacts/... -run TestValidateTranscriptMissingSegments
    Expected: FAIL with "segments required" error

  Scenario: Forward-compatible manifest (minor > 0) parses without errors
    Tool: Bash
    Command: go test ./internal/artifacts/... -run TestForwardCompatibleManifest
    Expected: PASS — minor version accepted

  Evidence: .sisyphus/evidence/01-artifact-validation-pass.txt
  ```

  **Commit**: YES | Message: `feat(artifacts): add core artifact structs and validation` | Files: `internal/artifacts/*.go`

- [x] 2. **Config Store (YAML + Keychain)**

  **What to do**: Refactor `internal/config/config.go` to use viper for YAML read/write. Implement `internal/secrets/store.go` with keyring support (macOS keychain via `github.com/keybase/go-keychain` or `runtime.NamedKeychain` on macOS). Add `Config.GetProviderConfig(provider string)` returning provider-specific API key + endpoint. Add `Config.GetStorageBackend() string` for S3/R2/local. Add `Config.GetSyncGateway() string`. Add environment variable fallback (`NOTO_API_KEY_*`).

  **Must NOT do**: Do not store plaintext API keys in config files. Do not implement the sync gateway itself.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: Config validation, keychain integration, multiple backend support
  - Skills: [`go-expert`] — Need viper + keychain integration
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 1 | Blocks: 7, 10 | Blocked By: —

  **References**:
  - Spec: `.docs/reference/storage-sync.md:SyncGateway` — Interface definition
  - Spec: `.docs/reference/providers.md` — Provider config fields
  - Current: `internal/config/config.go` — Existing config struct (read first)
  - Current: `internal/secrets/store.go` — Existing store interface (read first)

  **Acceptance Criteria**:
  - [ ] Config loads from `~/.noto/config.yaml` with viper
  - [ ] Config saves on `noto config set` with atomic write (rename)
  - [ ] API keys retrieved from macOS keychain, not config file
  - [ ] `NOTO_API_KEY_ASSEMBLYAI` env var falls back if keychain empty
  - [ ] Provider config: `{provider}.api_key`, `{provider}.endpoint`, `{provider}.model`
  - [ ] Storage backend: `s3.bucket`, `s3.region`, `s3.endpoint` (for R2), `local.path`

  **QA Scenarios**:
  ```
  Scenario: Config loads with all provider fields
    Tool: Bash
    Command: go test ./internal/config/... -run TestConfigLoad
    Expected: PASS — all fields present

  Scenario: API key retrieved from keychain, not env
    Tool: Bash
    Command: go test ./internal/secrets/... -run TestKeychainRetrieval
    Expected: PASS — keychain value used, env ignored

  Scenario: Missing API key returns error, not empty string
    Tool: Bash
    Command: go test ./internal/secrets/... -run TestMissingAPIKey
    Expected: FAIL with "API key not configured" error

  Evidence: .sisyphus/evidence/02-config-pass.txt
  ```

  **Commit**: YES | Message: `refactor(config): viper-based config + keychain integration` | Files: `internal/config/*.go`, `internal/secrets/*.go`

- [x] 3. **Meeting Storage Directory Layout**

  **What to do**: Implement `internal/storage/layout.go` following spec exactly: `~/Noto/meetings/YYYY/MM/{meeting_id}/` structure. Create `ManifestWriter`, `ArtifactReader`, `ArtifactLister` interfaces. Implement `LocalStorageAdapter` for filesystem operations. Create `EnsureDirectory(meetingID string)` that creates full path + `versions/{vid}/` + `.tmp/` subdirs. Implement `WriteArtifact(artifact Artifact)` with atomic rename (write to `.tmp/`, rename to final). Implement `ListMeetings() []MeetingRef` sorted by date descending. Implement `GetMeeting(meetingID string) *Meeting`.

  **Must NOT do**: Do not implement S3 operations in this task. Do not implement the sync gateway. Do not implement search indexing.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: File I/O, atomic writes, directory structure
  - Skills: [`go-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 1 | Blocks: 5, 6 | Blocked By: —

  **References**:
  - Spec: `.docs/reference/artifacts.md:storage-layout` — Directory structure lines 340-400
  - Spec: `.docs/decisions/0001-local-first-modular-architecture.md` — Storage decisions
  - Pattern: `internal/artifacts/` — Use existing artifact structs

  **Acceptance Criteria**:
  - [ ] `~/Noto/meetings/2026/04/{id}/manifest.json` created correctly
  - [ ] `versions/{vid}/` created for each artifact version
  - [ ] `.tmp/` staging directory used for atomic writes
  - [ ] `ListMeetings()` returns meetings sorted by date, newest first
  - [ ] `GetMeeting()` loads full manifest + all artifact versions
  - [ ] `WriteArtifact()` uses rename-not-write for atomicity

  **QA Scenarios**:
  ```
  Scenario: New meeting creates full directory structure
    Tool: Bash
    Command: go test ./internal/storage/... -run TestNewMeeting
    Expected: PASS — all dirs created

  Scenario: Atomic write: crash during write leaves original intact
    Tool: Bash
    Command: go test ./internal/storage/... -run TestAtomicWrite
    Expected: PASS — original file unchanged

  Scenario: ListMeetings returns sorted by date
    Tool: Bash
    Command: go test ./internal/storage/... -run TestListSorted
    Expected: PASS — dates descending

  Evidence: .sisyphus/evidence/03-storage-layout-pass.txt
  ```

  **Commit**: YES | Message: `feat(storage): local meeting storage with atomic writes` | Files: `internal/storage/*.go`

- [x] 4. **SQLite FTS5 Search Index**

  **What to do**: Implement `internal/search/search.go` with `modernc.org/sqlite` (pure Go SQLite). Create FTS5 virtual table schema: `meetings_fts(content TEXT, meeting_id TEXT, segment_text TEXT, speaker TEXT, decisions TEXT, actions TEXT, risks TEXT)`. Index on: meeting title (from manifest), transcript segments (speaker + text), decisions (text), action items (text + assignee), risks (text). Tokenizer: `unicode61` for unicode support. Implement `IndexMeeting(meeting *Meeting)` that tokenizes and inserts. Implement `Search(query string) []SearchResult` with Snippet() highlighting. Implement `DeleteFromIndex(meetingID string)`. Implement `SearchRanking` using BM25.

  **Must NOT do**: Do not use CGO sqlite — use pure Go `modernc.org/sqlite`. Do not index audio binary data.

  **Recommended Agent Profile**:
  - Category: `ultrabrain` — Reason: FTS5 query optimization, ranking algorithm
  - Skills: [`go-expert`] — Need SQLite FTS5 expertise
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 2 | Blocks: 5 | Blocked By: 1, 2, 3

  **References**:
  - Spec: `.docs/reference/storage-sync.md:search-index` — Index structure
  - Library: `modernc.org/sqlite` — Pure Go SQLite binding
  - FTS5: `unicode61` tokenizer for proper Unicode handling
  - BM25: Standard probabilistic ranking for search results

  **Acceptance Criteria**:
  - [ ] FTS5 table created with correct schema
  - [ ] `IndexMeeting()` indexes: title, transcript (speaker+text), decisions, actions, risks
  - [ ] `Search("api ship")` returns ranked results with BM25 scoring
  - [ ] Snippet() returns highlighted matches with `**` markers
  - [ ] `DeleteFromIndex()` removes all entries for meetingID
  - [ ] Index persists to `~/.noto/search.db`
  - [ ] `noto search "term"` CLI command works

  **QA Scenarios**:
  ```
  Scenario: Index meeting with transcript, search returns result
    Tool: Bash
    Command: go test ./internal/search/... -run TestIndexAndSearch
    Expected: PASS — "ship" found in meeting with "ship v1"

  Scenario: BM25 ranking: "ship" ranks higher in meeting titled "ship v1"
    Tool: Bash
    Command: go test ./internal/search/... -run TestBM25Ranking
    Expected: PASS — correct meeting ranked first

  Scenario: Delete removes from index
    Tool: Bash
    Command: go test ./internal/search/... -run TestDeleteFromIndex
    Expected: PASS — deleted meeting not in results

  Scenario: Empty query returns error, not all meetings
    Tool: Bash
    Command: go test ./internal/search/... -run TestEmptyQuery
    Expected: FAIL with "query required" error

  Evidence: .sisyphus/evidence/04-search-pass.txt
  ```

  **Commit**: YES | Message: `feat(search): SQLite FTS5 full-text search with BM25 ranking` | Files: `internal/search/*.go`

- [x] 5. **Manifest Writer + Artifact Pipeline**

  **What to do**: Implement `internal/artifacts/writer.go` — `ManifestWriter` that writes `manifest.json` with checksum verification. Implement `WritePipeline(meetingID string, artifacts []Artifact)` that writes all artifacts to `.tmp/` then atomically renames to final locations. Implement `VersionArtifact(meetingID string, artifactType string)` that copies current version to `versions/{new_vid}/`. Implement checksum verification on read: `VerifyChecksums(manifest Manifest) error`. Implement `ImportAudio(path string) (audioArtifact Artifact, err)` that reads audio file, computes SHA-256, writes to `audio.v1` metadata.

  **Must NOT do**: Do not implement transcription (that's provider-specific, task 7). Do not implement S3 upload.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: Atomic writes, checksum verification, version management
  - Skills: [`go-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 1 | Blocks: 15 | Blocked By: 1, 3

  **References**:
  - Spec: `.docs/reference/artifacts.md:writing-artifacts` — Lines 400-450
  - Spec: `.docs/reference/artifacts.md:checksums` — Checksum algorithm
  - Current: `internal/artifacts/transcript.go` — Existing struct
  - Current: `internal/artifacts/summary.go` — Existing struct

  **Acceptance Criteria**:
  - [ ] `WritePipeline()` writes all artifacts atomically
  - [ ] Checksum computed with SHA-256 on full artifact content
  - [ ] `VerifyChecksums()` returns error if any artifact mismatched
  - [ ] `VersionArtifact()` creates new version with incremented `minor`
  - [ ] `ImportAudio()` reads M4A/WAV, computes checksum, produces `audio.v1` metadata
  - [ ] `manifest.json` written last (after all artifacts)

  **QA Scenarios**:
  ```
  Scenario: WritePipeline with all artifact types
    Tool: Bash
    Command: go test ./internal/artifacts/... -run TestWritePipeline
    Expected: PASS — all artifacts written, manifest last

  Scenario: Checksum mismatch detected on verify
    Tool: Bash
    Command: go test ./internal/artifacts/... -run TestChecksumMismatch
    Expected: FAIL with "checksum mismatch" error

  Scenario: VersionArtifact increments minor version
    Tool: Bash
    Command: go test ./internal/artifacts/... -run TestVersionArtifact
    Expected: PASS — minor incremented, new version dir created

  Evidence: .sisyphus/evidence/05-manifest-writer-pass.txt
  ```

  **Commit**: YES | Message: `feat(artifacts): manifest writer with checksum verification` | Files: `internal/artifacts/writer.go`

- [x] 6. **Provider Routing + HTTP Clients**

  **What to do**: Refactor `internal/providers/registry.go` and `internal/providers/router.go`. Implement `STTProvider` interface: `Transcribe(audio []byte, opts TranscribeOptions) (*Transcript, error)`. Implement `AssemblyAIAdapter`, `WhisperAdapter` (via OpenAI-compatible API), `SpeechmaticsAdapter`. Implement `LLMProvider` interface: `Summarize(transcript Transcript, opts SummarizeOptions) (*Summary, error)`. Implement `MistralAdapter`, `OpenRouterAdapter` (with model selection). Implement `LiveSTTProvider` for real-time transcription (streaming). Implement `ProviderRouter` that selects provider based on config priority list and fallback logic. Implement `NormalizeTranscript(raw *providerTranscript) *Transcript` with speaker label normalization per `.docs/reference/providers.md`.

  **Must NOT do**: Do not implement the actual API calls (those are in adapters). Do not implement TUI in this task.

  **Recommended Agent Profile**:
  - Category: `ultrabrain` — Reason: API adapter pattern, streaming transcription, provider fallback
  - Skills: [`go-expert`, `http-client-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 2 | Blocks: 8, 9, 15 | Blocked By: 2

  **References**:
  - Spec: `.docs/reference/providers.md` — Provider specifications
  - Spec: `.docs/reference/providers.md:stt-providers` — STT adapter specs
  - Spec: `.docs/reference/providers.md:summary-providers` — LLM adapter specs
  - Spec: `.docs/reference/providers.md:normalization` — Transcript normalization rules
  - Current: `internal/providers/registry.go` — Existing registry
  - Current: `internal/providers/router.go` — Existing router (read first)
  - Current: `internal/providers/types.go` — Existing types

  **Acceptance Criteria**:
  - [ ] `STTProvider.Transcribe()` returns `*Transcript` matching `transcript.v1` schema
  - [ ] `AssemblyAIAdapter` uses v2 endpoint with `Content-Type: audio/*`
  - [ ] `WhisperAdapter` uses OpenAI-compatible `/v1/audio/transcriptions`
  - [ ] `MistralAdapter` uses `/v1/chat/completions` with function calling for structured output
  - [ ] `OpenRouterAdapter` supports model routing (Mistral, Anthropic, OpenAI via single endpoint)
  - [ ] `ProviderRouter` tries providers in priority order, falls back on 429/5xx
  - [ ] `NormalizeTranscript()` merges adjacent same-speaker segments, fixes confidence < 0.7
  - [ ] Speaker labels: normalized to `speaker_1`, `speaker_2` or named if provided

  **QA Scenarios**:
  ```
  Scenario: AssemblyAI adapter returns valid transcript
    Tool: Bash
    Command: go test ./internal/providers/... -run TestAssemblyAIAdapter
    Expected: PASS — transcript struct valid

  Scenario: Provider fallback: primary returns 429, router uses secondary
    Tool: Bash
    Command: go test ./internal/providers/... -run TestProviderFallback
    Expected: PASS — secondary provider used after 429

  Scenario: NormalizeTranscript merges adjacent same-speaker segments
    Tool: Bash
    Command: go test ./internal/providers/... -run TestNormalizeMerge
    Expected: PASS — adjacent segments merged

  Scenario: Confidence < 0.7 segments flagged
    Tool: Bash
    Command: go test ./internal/providers/... -run TestLowConfidence
    Expected: PASS — low-confidence segments marked

  Evidence: .sisyphus/evidence/06-provider-routing-pass.txt
  ```

  **Commit**: YES | Message: `feat(providers): STT/LLM adapter routing with fallback` | Files: `internal/providers/*.go`

- [x] 7. **LLM Summary Prompts**

  **What to do**: Implement `internal/prompts/summarizer.go` with prompt templates for each summary type. Prompts must follow spec exactly: `summarize.v1` decisions extraction, action items extraction, risk identification, open questions. Implement `PromptBuilder.Build(systemPrompt string, transcript Transcript) (string, error)`. Implement `PromptBuilder.BuildSummaryRequest(transcript Transcript, opts SummaryOptions) *ChatRequest`. Use chain-of-thought for risk identification. Use few-shot examples for decision extraction (3 examples from spec). Prompt versioning: store prompt versions alongside output for reproducibility.

  **Must NOT do**: Do not call the LLM API (that's the provider adapter). Do not hardcode API keys.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: Prompt engineering accuracy
  - Skills: [`go-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 2 | Blocks: 15 | Blocked By: 6

  **References**:
  - Spec: `.docs/reference/artifacts.md:summary.v1` — Summary schema
  - Spec: `.docs/reference/providers.md:summary-prompt-template` — Prompt spec
  - Spec: `.docs/reference/providers.md:summary-providers` — Provider-specific prompt requirements

  **Acceptance Criteria**:
  - [ ] Decision extraction prompt returns structured JSON matching `summary.v1.decisions`
  - [ ] Action item prompt uses `@person` format with `assignee`, `task`, `completed` fields
  - [ ] Risk prompt uses chain-of-thought to identify implicit risks
  - [ ] Open questions prompt distinguishes answered vs unanswered
  - [ ] Few-shot examples included for decision extraction (3 from spec)
  - [ ] Prompt versioning: prompts stored with version ID for reproducibility

  **QA Scenarios**:
  ```
  Scenario: Decision prompt produces valid decisions JSON
    Tool: Bash
    Command: go test ./internal/prompts/... -run TestDecisionPrompt
    Expected: PASS — JSON decodes to []Decision

  Scenario: Action item prompt uses correct @person format
    Tool: Bash
    Command: go test ./internal/prompts/... -run TestActionItemPrompt
    Expected: PASS — @person placeholders in output

  Scenario: Prompt versioning: same prompt version + same input = same output structure
    Tool: Bash
    Command: go test ./internal/prompts/... -run TestPromptVersioning
    Expected: PASS — version ID consistent

  Evidence: .sisyphus/evidence/07-summary-prompts-pass.txt
  ```

  **Commit**: YES | Message: `feat(prompts): LLM summary prompt templates with few-shot examples` | Files: `internal/prompts/*.go`

- [x] 8. **Transcript Normalizer**

  **What to do**: Implement `internal/providers/speech/normalizers.go` — `TranscriptNormalizer` interface. Implement `DiarizationNormalizer` that merges segments by speaker, handles speaker changes. Implement `TimestampNormalizer` that fixes overlapping timestamps, gaps > 30s flagged as potential gaps. Implement `ConfidenceNormalizer` that flags segments with avg confidence < 0.7, substitutes `[low confidence]` marker. Implement `FormatNormalizer` that fixes common ASR errors: repeated words, partial words, filler words (um, uh, like). Implement `PunctuationNormalizer` that adds punctuation based onprosodic features. Implement `SpeakerLabelNormalizer` that maps provider-specific labels to canonical `speaker_1`, `speaker_2` or user-provided names.

  **Must NOT do**: Do not implement the providers themselves. Do not add punctuation to the actual transcript text beyond what the spec allows.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: Audio processing, timestamp arithmetic, normalization rules
  - Skills: [`go-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 2 | Blocks: 15 | Blocked By: 6

  **References**:
  - Spec: `.docs/reference/artifacts.md:transcript.v1` — Segment schema with timestamps
  - Spec: `.docs/reference/providers.md:normalization` — Normalization rules
  - Current: `internal/providers/speech/normalizers.go` — Existing normalizer stubs

  **Acceptance Criteria**:
  - [ ] `Normalize()` merges adjacent same-speaker segments within 200ms gap
  - [ ] Timestamp gaps > 30s marked as `gap: true` in segment
  - [ ] Confidence < 0.7 segments flagged with `[low confidence]` marker
  - [ ] Speaker labels normalized to `speaker_N` or provided names
  - [ ] `FormatNormalizer` removes >3 repeated words, filler words
  - [ ] Punctuation added at sentence boundaries based on prosodic features

  **QA Scenarios**:
  ```
  Scenario: Adjacent same-speaker segments merged
    Tool: Bash
    Command: go test ./internal/providers/speech/... -run TestDiarizationMerge
    Expected: PASS — 5 segments → 2 segments

  Scenario: Timestamp gap > 30s flagged
    Tool: Bash
    Command: go test ./internal/providers/speech/... -run TestTimestampGap
    Expected: PASS — gap: true on output

  Scenario: Low confidence segments flagged
    Tool: Bash
    Command: go test ./internal/providers/speech/... -run TestLowConfidenceFlag
    Expected: PASS — [low confidence] marker in output

  Evidence: .sisyphus/evidence/08-transcript-normalizer-pass.txt
  ```

  **Commit**: YES | Message: `feat(providers): transcript normalization with speaker merge` | Files: `internal/providers/speech/normalizers.go`

- [x] 9. **Bubble Tea TUI — App Core Refactor**

  **What to do**: Refactor `internal/tui/app_core.go` + `internal/tui/types.go` + `internal/tui/styles.go` following the research findings. Implement the styling system using lipgloss per the Master Synthesis: semantic color tokens (`ColorDecision`, `ColorAction`, `ColorRisk`, `ColorSpeakerA`, etc.), adaptive theme (dark/light via `Inherit()`/`LightDark()`), per-side border styling. Implement `Viewport` component with virtual scrolling for long lists (per `lipgloss.JoinHorizontal`/`lipgloss.JoinVertical`). Implement `BubblePup` recording animation: pulsing dot + elapsed time + audio level meters (3-meters: left/right speakers + ambient). Implement `CommandPalette` overlay (vim-style `:` command entry). Implement context-based keybindings (per lazygit model: meeting list context, detail context, search context). Implement `app.Update()` with `tea.Tick` for recording timer updates. Implement `tea.Every` for ambient meter updates.

  **Must NOT do**: Do not implement all screens in this task — only the core infrastructure. Do not hardcode ANSI colors — use semantic color tokens from lipgloss Style.

  **Recommended Agent Profile**:
  - Category: `visual-engineering` — Reason: Bubble Tea, lipgloss styling, viewport scrolling
  - Skills: [`lipgloss-expert`, `bubbletea-expert`]
  - Omitted: [`go-expert`] — UI work, not backend

  **Parallelization**: Can Parallel: YES | Wave 3 | Blocks: 10, 11, 12 | Blocked By: 2, 4

  **References**:
  - Research: `TUI Design — Master Synthesis` — All 15 TUI patterns
  - Research: `Lazygit TUI Architecture Deep-Dive` — Context/keybinding model
  - Research: `Bubble Tea TUI Patterns` — Viewport, command palette, real-time patterns
  - Research: `Lipgloss Styling` — `Inherit()`, `LightDark()`, `JoinHorizontal/Vertical`, table patterns
  - Current: `internal/tui/app_core.go` — Existing (read first)
  - Current: `internal/tui/types.go` — Existing (read first)
  - Current: `internal/tui/styles.go` — Existing (read first)

  **Acceptance Criteria**:
  - [ ] Semantic color tokens: `ColorDecision` (green), `ColorAction` (yellow), `ColorRisk` (red), `ColorSpeakerA/B/C` (cyan/green/magenta)
  - [ ] Dark theme (default) and light theme switchable via `Inherit()`/`LightDark()`
  - [ ] `Viewport` renders 1000+ meeting list without lag (virtual scroll)
  - [ ] `BubblePup` animation: pulse dot (1Hz), elapsed time (MM:SS), 3 audio meters updating at 10Hz
  - [ ] `CommandPalette` overlay: `:` opens command entry, ESC closes
  - [ ] Context-based keybindings: `d` in meeting list = delete, `d` in detail = toggle decisions
  - [ ] `tea.Tick` updates elapsed timer every second during recording

  **QA Scenarios**:
  ```
  Scenario: 1000 meetings render without lag
    Tool: interactive_bash
    Command: go test ./internal/tui/... -run TestVirtualScroll -v
    Expected: PASS — 60fps scrolling

  Scenario: BubblePup pulse animation visible
    Tool: interactive_bash
    Command: go test ./internal/tui/... -run TestBubblePup
    Expected: PASS — animation state machine transitions

  Scenario: CommandPalette opens/closes
    Tool: interactive_bash
    Command: go test ./internal/tui/... -run TestCommandPalette
    Expected: PASS — ESC closes palette

  Evidence: .sisyphus/evidence/09-tui-core-pass.txt
  ```

  **Commit**: YES | Message: `refactor(tui): bubble tea core with semantic color tokens` | Files: `internal/tui/app_core.go`, `internal/tui/types.go`, `internal/tui/styles.go`

- [x] 10. **TUI Screens (5 screens, per Task 0 spec)**

  **What to do**: Implement 5 TUI screens (Dashboard, Meetings, Meeting Detail, Transcript, Search). No Settings/Providers/Storage screens. Each screen follows the research patterns (lipgloss semantic colors, viewport virtual scroll, command palette via `:`).

  **Dashboard** (`internal/tui/screens/dashboard.go`): Recording state badge (idle/recording HH:MM:SS/processing), today's meeting/action/decision count as text stats (NOT bar charts), recent meetings list (last 5), index health line (`clean`/`dirty`), bottom bar: `r` `i` `/` `:` `?` `q`.

  **Meetings** (`internal/tui/screens/meetings.go`): Virtual-scrolling list, 1000+ meetings. Row: `title   duration   date   D:N A:N R:N`. `/` starts inline filter, `s` cycles sort (date/title), `d` delete (confirm), `n` new, `enter` open detail, `esc` back to dashboard. Vim navigation (`j/k` or `↑↓`, `gg`/`G`).

  **Meeting Detail** (`internal/tui/screens/detail.go`): Header (title, date, duration, speakers). Short summary (2 sentences). Numbered Decisions (green, with `[seg_XXXXXX]` citation). Action Items (`@person` format, NOT tracked completed). Risks (red/muted). Open Questions (yellow/muted). `t` expands to Transcript view (same screen), `f` shows artifact paths (compact overlay).

  **Transcript** (`internal/tui/screens/transcript.go`): Virtual-scrolling transcript. Entry: `timestamp   SpeakerLabel [source_role]   seg_XXXXXX`. Text wrapped to terminal width, cached per-width. Speaker uses `DisplayName` when set. `f` follow (jump to linked meeting), `c` copy citation, `esc` back.

  **Search** (`internal/tui/screens/search.go`): fzf-style persistent input at bottom. Results: `time   speaker   meeting   text snippet` with highlighted matches. `enter` opens meeting detail with segment highlighted. `c` copy citation. `tab` cycles scope (all/decisions/actions/transcript). Evidence panel on wide terminals. `/` works globally from any screen (opens search in place).

  **Must NOT do**: No Settings/Providers/Storage screens. No bar charts or decorative visualization. No separate full-screen search (search is global via `/`). No real-time transcription UI. No detached workers. No hardcoded provider credentials. No generic "AI" styling.

  **Recommended Agent Profile**:
  - Category: `visual-engineering` — Reason: 5 lean screens with precise layout per task-0 spec
  - Skills: [`lipgloss-expert`, `bubbletea-expert`]
  - Omitted: [`go-expert`] — UI work, not backend

  **Parallelization**: Can Parallel: YES (split 2+2+1 across agents) | Wave 3 | Blocks: — | Blocked By: 9

  **References**:
  - Spec: `.docs/reference/tui.md` — Original spec (superseded by 5-screen spec above)
  - Research: `TUI Design — Master Synthesis` — Virtual scroll, semantic color tokens, command palette
  - Research: `Lazygit TUI Architecture Deep-Dive` — Context-based keybindings, bottom bar key hints
  - Current: `internal/tui/screens.go` — Existing screen stubs (read first)
  - Current: `internal/tui/components.go` — Existing components (read first)
  - Current: `internal/tui/app_core.go` — Existing Bubble Tea model (read first)

  **Acceptance Criteria**:
  - [ ] Dashboard: recording state badge, today's stats, recent list, index health line, bottom bar keys
  - [ ] Meetings: virtual scroll (1000+ no lag), D:N/A:N/R:N per row, `/` filter, `s` sort, `d` confirm delete
  - [ ] Meeting Detail: numbered decisions with seg citations, action items @person, collapsible sections, `t`/`f` overlay
  - [ ] Transcript: virtual scroll, timestamp+speaker+role+seg per line, wrapped text cached per-width, `f`/`c` actions
  - [ ] Search: as-you-type filter, highlighted results, `tab` scope cycling, `enter` opens with segment highlighted, evidence panel on wide
  - [ ] All screens: `ESC` closes overlay and returns to previous screen, `q` at root quits (confirm if job active)
  - [ ] All screens: `?` help overlay with context-specific keybindings
  - [ ] `/` is global from any screen — opens search in place, does not navigate to a separate screen
  - [ ] Recording survives TUI exit (`esc` exits TUI but capture helper keeps running)

  **QA Scenarios**:
  ```
  Scenario: Dashboard shows today's stats as text, not bar charts
    Tool: Playwright
    Steps: Open TUI, observe dashboard
    Expected: "today   3 meetings   7 actions   2 decisions" — text only, no bar charts

  Scenario: Meetings virtual scroll — 1000 meetings at 60fps
    Tool: Bash
    Command: go test ./internal/tui/... -run TestVirtualScroll -v
    Expected: PASS — 60fps scrolling with 1000 fixture meetings

  Scenario: Search / is global from Dashboard
    Tool: Playwright
    Steps: On Dashboard, press /
    Expected: Search input activates in place, Dashboard content above remains visible

  Scenario: ESC closes overlay, returns to previous screen
    Tool: Playwright
    Steps: In Meeting Detail, press f (files overlay), then ESC
    Expected: Overlay closed, Meeting Detail still visible

  Scenario: ? shows context-specific keybindings
    Tool: Playwright
    Steps: Press ? in Meetings list, observe help overlay
    Expected: Overlay shows d=delete, n=new, s=sort, /=filter, enter=open

  Scenario: ? shows context-specific keybindings
    Tool: Playwright
    Steps: Press ? in Meetings list, observe help overlay
    Expected: Overlay shows d=delete, n=new, s=sort, /=filter, enter=open

  Scenario: Recording survives ESC from TUI
    Tool: Bash
    Command: noto record --title "Test" && sleep 2 && ESC from TUI && noto status --json | jq .recording
    Expected: "recording" — capture helper continues after TUI exit

  Evidence: .sisyphus/evidence/10-tui-screens-pass.txt
  ```

  **Commit**: YES | Message: `feat(tui): 5-screen Bubble Tea TUI per task-0 spec` | Files: `internal/tui/screens/*.go`

- [x] 11. **CLI Commands (Import, Transcribe, Summarize, Sync)**

  **What to do**: Implement all CLI commands in `internal/cli/cli.go` that are currently stubbed as `notImplemented`. Commands:

  `noto import <audio-file>` — Read audio file (M4A/WAV/FLAC/ALAC), compute checksum, create meeting directory, save audio artifact, start transcription.

  `noto transcribe [--meeting-id <id>] [--provider <provider>]` — Run transcription on meeting's audio, produce `transcript.v1`, index in FTS5.

  `noto summarize [--meeting-id <id>] [--provider <provider>]` — Run summarization on transcript, produce `summary.v1`, write manifest update.

  `noto sync [--dry-run] [--target r2|s3|local]` — Run sync to target. `--dry-run` shows what would be uploaded without uploading. Uses S3 adapter.

  `noto search <query>` — FTS5 search, return ranked meeting list with snippets.

  `noto meeting <id>` — Show meeting detail (read from local storage).

  `noto config get <key>` / `noto config set <key> <value>` — Config management.

  `noto providers test [--provider <provider>]` — Test provider API connection.

  Implement progress output for long-running operations (transcription, sync). Use `spf13/cobra` for CLI framework if not already using it.

  **Must NOT do**: Do not implement the provider API calls themselves (use task 6 adapters). Do not implement S3 sync logic (use task 14 adapter).

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: CLI UX, progress reporting, command routing
  - Skills: [`go-expert`, `cli-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 4 | Blocks: — | Blocked By: 4, 6, 7, 8, 14

  **References**:
  - Spec: `.docs/reference/cli.md` — All CLI commands with syntax
  - Spec: `.docs/reference/cli.md:import` — Import command spec
  - Spec: `.docs/reference/cli.md:sync` — Sync command spec
  - Spec: `.docs/reference/cli.md:search` — Search command spec
  - Current: `internal/cli/cli.go` — Existing CLI (read first)

  **Acceptance Criteria**:
  - [ ] `noto import audio.m4a` creates meeting with audio artifact
  - [ ] `noto transcribe` produces valid `transcript.v1`
  - [ ] `noto summarize` produces valid `summary.v1`
  - [ ] `noto sync --dry-run` shows S3 PUT preview without uploading
  - [ ] `noto search "api"` returns ranked results with snippets
  - [ ] Progress bars for transcription and sync operations
  - [ ] All commands return proper exit codes (0 success, 1 error, 2 usage)

  **QA Scenarios**:
  ```
  Scenario: noto import creates meeting directory
    Tool: Bash
    Command: noto import /tmp/fixture/audio.m4a && ls ~/Noto/meetings/*/manifest.json
    Expected: manifest.json exists

  Scenario: noto search returns ranked results
    Tool: Bash
    Command: noto search "ship" | head -5
    Expected: Meetings ranked by BM25 score, snippets shown

  Scenario: noto sync --dry-run shows upload preview
    Tool: Bash
    Command: noto sync --dry-run 2>&1 | head -20
    Expected: S3 PUT operations listed without actual upload

  Evidence: .sisyphus/evidence/11-cli-commands-pass.txt
  ```

  **Commit**: YES | Message: `feat(cli): all CLI commands implemented with progress reporting` | Files: `internal/cli/*.go`

- [x] 12. **macOS Audio Capture Helper**

  **What to do**: Implement `cmd/capture/main.swift` — macOS audio capture helper. Use `AVFoundation` or `CoreAudio` to capture system audio + microphone simultaneously. Implement two-source model: `local_speaker` (mic input) + `participants` (system audio). Use `AudioUnit` for low-latency capture. Implement `portaudio`-style API that Go can call via JSON-RPC over stdin/stdout. Implement IPC protocol: Go process spawns Swift helper, communicates via JSON-RPC. Commands: `start(sources []string, sampleRate int)`, `stop() -> AudioMetadata`, `pause()`, `resume()`. Implement `GetAudioLevel() -> (left float64, right float64, ambient float64)` for metering. Implement `GetCapturedAudio() -> []byte` for final audio data.

  **Must NOT do**: Do not implement recording to disk in Swift — stream audio data to Go for storage. Do not use deprecated macOS APIs. Do not require macOS Gatekeeper approval without clear instructions.

  **Recommended Agent Profile**:
  - Category: `ultrabrain` — Reason: Low-level CoreAudio/AVFoundation, IPC protocol design
  - Skills: [`swift-expert`, `go-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 4 | Blocks: — | Blocked By: —

  **References**:
  - Spec: `.docs/guides/build-plan.md` — macOS capture helper spec
  - Spec: `.docs/reference/artifacts.md:source-roles` — Two-source model
  - Research: `Audio Processing` — SILK/SILK+, VAD, metering patterns
  - Library: `github.com/srebrown/portaudio` — For Go audio I/O
  - Library: `github.com/golang/snappy` — For audio compression in transit

  **Acceptance Criteria**:
  - [ ] `noto record` starts capture with two sources (mic + system)
  - [ ] `noto stop` returns audio data to Go for storage
  - [ ] `GetAudioLevel()` returns 3 levels at 10Hz for BubblePup meters
  - [ ] IPC protocol: JSON-RPC over stdin/stdout, no network required
  - [ ] Works on macOS 12+ without code signing issues
  - [ ] Graceful degradation: if system audio unavailable, record mic only

  **QA Scenarios**:
  ```
  Scenario: noto record starts capture
    Tool: Bash
    Command: timeout 5 noto record &
    Expected: Swift helper spawned, audio levels updating

  Scenario: Two-source model: mic and system audio captured separately
    Tool: Bash
    Command: go test ./cmd/capture/... -run TestTwoSourceCapture
    Expected: PASS — local_speaker and participants tracks separate

  Scenario: IPC: Go spawns Swift helper, JSON-RPC communication works
    Tool: Bash
    Command: go test ./cmd/capture/... -run TestIPCommunication
    Expected: PASS — JSON-RPC over stdin/stdout

  Scenario: Graceful degradation: if system audio fails, mic-only continues
    Tool: Bash
    Command: go test ./cmd/capture/... -run TestGracefulDegradation
    Expected: PASS — mic-only recording continues

  Evidence: .sisyphus/evidence/12-macos-capture-pass.txt
  ```

  **Commit**: YES | Message: `feat(capture): macOS audio capture helper with IPC` | Files: `cmd/capture/main.swift`, `cmd/capture/ipc.go`

- [x] 13. **S3/R2 Adapter**

  **What to do**: Implement `internal/storage/adapters/s3.go` — `S3Adapter` implementing `SyncAdapter` interface. Use `aws-sdk-go-v2` with `s3.NewFromConfig()`. Implement Cloudflare R2 support: use `endpoint` resolver for R2, apply `1.73.0+` checksum workaround per research. Implement multipart upload for large files (> 5MB). Implement `PutObject(key string, body io.Reader, opts PutOptions)` with content-type detection. Implement `GetObject(key string, dest io.WriterAt) error`. Implement `GetPresignedURL(key string, ttl time.Duration) (string, error)` for downloads. Implement `ListObjects(prefix string) []ObjectMeta`. Implement `DeleteObject(key string) error`. Implement `HeadBucket() (BucketMeta, error)`. Implement local filesystem fallback in `internal/storage/adapters/local.go` for offline/local-only mode. Implement factory: `NewSyncAdapter(backend string) SyncAdapter`.

  **Must NOT do**: Do not implement the sync gateway logic (that's a separate service). Do not use CGO for AWS SDK. Do not store credentials in code.

  **Recommended Agent Profile**:
  - Category: `deep` — Reason: AWS SDK v2, multipart upload, R2-specific endpoint
  - Skills: [`go-expert`, `aws-expert`]
  - Omitted: [`ui-animation`]

  **Parallelization**: Can Parallel: YES | Wave 4 | Blocks: 15 | Blocked By: 3

  **References**:
  - Research: `S3 Adapter Patterns` — S3 SDK v2, R2, MinIO patterns
  - Research: `S3 Adapter Patterns:checksum-workaround` — R2 checksum issue
  - Spec: `.docs/reference/storage-sync.md:SyncGateway` — Interface definition
  - Spec: `.docs/reference/storage-sync.md:r2-layout` — R2 storage layout
  - Spec: `.docs/decisions/0002-deployment-modes.md` — SyncGateway modes

  **Acceptance Criteria**:
  - [ ] `PutObject()` uploads to R2 with correct content-type
  - [ ] Multipart upload for files > 5MB
  - [ ] `GetPresignedURL()` returns valid R2 URL with TTL
  - [ ] `ListObjects()` returns all artifacts for a meeting
  - [ ] `HeadBucket()` returns used/available space
  - [ ] `DeleteObject()` removes artifact from R2
  - [ ] Local fallback: all operations work without network
  - [ ] Factory `NewSyncAdapter("r2")` / `NewSyncAdapter("local")` creates correct adapter

  **QA Scenarios**:
  ```
  Scenario: PutObject uploads to R2
    Tool: Bash
    Command: go test ./internal/storage/adapters/... -run TestPutObject
    Expected: PASS — object in R2 bucket

  Scenario: Multipart upload for large file
    Tool: Bash
    Command: go test ./internal/storage/adapters/... -run TestMultipartUpload
    Expected: PASS — large file uploaded in chunks

  Scenario: GetPresignedURL returns valid R2 URL
    Tool: Bash
    Command: go test ./internal/storage/adapters/... -run TestPresignedURL
    Expected: PASS — URL valid for TTL duration

  Scenario: Local fallback: all operations work without network
    Tool: Bash
    Command: NOTO_OFFLINE=true go test ./internal/storage/adapters/... -run TestLocalFallback
    Expected: PASS — local adapter used, no network calls

  Evidence: .sisyphus/evidence/13-s3-adapter-pass.txt
  ```

  **Commit**: YES | Message: `feat(storage): S3/R2 adapter with multipart upload` | Files: `internal/storage/adapters/*.go`

- [x] 14. **End-to-End Recording Pipeline**

  **What to do**: Integrate tasks 9, 10, 11, 12 into a full working pipeline. Test: `noto record` → capture starts → speak for 10 seconds → `noto stop` → audio saved → `noto transcribe` → transcript indexed → `noto summarize` → summary written. Test FTS5 search works on transcript. Test TUI shows meeting in list with correct D:N/A:N/R:N counts. Test S3 sync (dry-run).

  **Must NOT do**: Do not deploy to production. Do not use real API keys in fixtures. Do not test on production data.

  **Recommended Agent Profile**:
  - Category: `unspecified-high` — Reason: Integration testing across all components
  - Skills: [`integration-tester`]
  - Omitted: —

  **Parallelization**: Can Parallel: YES | Wave 5 | Blocks: — | Blocked By: 10, 11, 12, 13

  **References**:
  - Spec: `.docs/reference/testing.md` — Integration test requirements
  - Spec: `.docs/reference/testing.md:acceptance-matrix` — Phase gate criteria
  - Spec: `.docs/reference/benchmarks.md` — Benchmark requirements

  **Acceptance Criteria**:
  - [ ] Full pipeline: record → transcribe → summarize → search → sync (dry-run)
  - [ ] All testing.md fixtures pass
  - [ ] TUI displays meeting with correct D:N/A:N/R:N counts
  - [ ] `noto search` returns the new meeting's transcript
  - [ ] Binary builds without errors: `go build ./cmd/noto`

  **QA Scenarios**:
  ```
  Scenario: Full pipeline with fixture audio
    Tool: Bash
    Command: noto import /tmp/fixture/audio.m4a && noto transcribe --meeting-id $(cat /tmp/last-id) && noto summarize --meeting-id $(cat /tmp/last-id)
    Expected: PASS — transcript and summary artifacts created

  Scenario: FTS5 search finds new meeting
    Tool: Bash
    Command: noto search "fixture keyword" | grep "Weekly Standup"
    Expected: PASS — meeting found with correct snippet

  Scenario: S3 dry-run shows upload preview
    Tool: Bash
    Command: noto sync --dry-run --meeting-id $(cat /tmp/last-id) | head -20
    Expected: PUT operations listed without actual upload

  Evidence: .sisyphus/evidence/14-e2e-pipeline-pass.txt
  ```

  **Commit**: YES | Message: `feat(e2e): complete recording pipeline integration` | Files: `cmd/noto/main.go`

- [x] 15. **Benchmarks + Performance Validation**

  **What to do**: Implement benchmark harness per `.docs/reference/benchmarks.md`. Implement `internal/benchmarks/benchmarker.go` with datasets: synthetic meeting audio (1min, 5min, 15min, 30min). Implement metrics: transcription latency, search latency, FTS5 index size, S3 upload time, TUI render time (fps). Implement scoring tools per spec (speeds.index, tui-responsiveness.index). Run all benchmarks. Produce `benchmark-results.json` per spec format. Validate against acceptance gates: FTS5 < 100ms, TUI render > 30fps, S3 upload < 2s/MB.

  **Must NOT do**: Do not run benchmarks on production R2 bucket. Do not use real meeting data. Do not modify benchmarks after seeing results (data integrity).

  **Recommended Agent Profile**:
  - Category: `unspecified-high` — Reason: Benchmark implementation and validation
  - Skills: [`benchmarking`]
  - Omitted: —

  **Parallelization**: Can Parallel: YES | Wave 5 | Blocks: — | Blocked By: 14

  **References**:
  - Spec: `.docs/reference/benchmarks.md` — Dataset specs, metrics, scoring tools
  - Spec: `.docs/reference/benchmarks.md:acceptance-gates` — Performance thresholds
  - Current: `internal/benchmarks/result.go` — Existing result struct

  **Acceptance Criteria**:
  - [ ] FTS5 search latency < 100ms for 1000 meetings
  - [ ] TUI render > 30fps during scroll
  - [ ] S3 upload < 2s/MB for files < 5MB
  - [ ] Transcription latency < 1s per minute of audio
  - [ ] Benchmark results JSON matches schema in spec
  - [ ] All acceptance gates passed

  **QA Scenarios**:
  ```
  Scenario: FTS5 search performance
    Tool: Bash
    Command: go test ./internal/benchmarks/... -bench TestFTS5SearchLatency
    Expected: < 100ms for 1000 meetings

  Scenario: TUI render fps
    Tool: Bash
    Command: go test ./internal/benchmarks/... -bench TestTUIRenderFPS
    Expected: > 30fps

  Scenario: S3 upload speed
    Tool: Bash
    Command: go test ./internal/benchmarks/... -bench TestS3UploadSpeed
    Expected: < 2s/MB

  Evidence: .sisyphus/evidence/15-benchmarks-pass.txt
  ```

  **Commit**: YES | Message: `feat(benchmarks): benchmark harness with acceptance gates` | Files: `internal/benchmarks/*.go`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

> 4 review agents run in PARALLEL. ALL must APPROVE. Present consolidated results to user and get explicit "okay" before completing.
> **Do NOT auto-proceed after verification. Wait for user's explicit approval before marking work complete.**

- [x] F1. Plan Compliance Audit — oracle
  Verify all spec requirements from `.docs/reference/*.md` are implemented. Check: artifact schemas match spec exactly, TUI screens match spec layout, CLI commands match spec syntax, storage layout matches spec.

- [x] F2. Code Quality Review — unspecified-high
  Review all Go code for: error handling, goroutine safety, memory leaks, SQL injection, credential handling. Run `golangci-lint run ./...`.

- [x] F3. Real Manual QA — unspecified-high (+ playwright if UI)
  Execute full user journey: import audio → transcribe → summarize → search → view in TUI → sync. All steps must complete without errors.

- [x] F4. Scope Fidelity Check — deep
  Verify: no implemented feature is out of V1 scope, no "nice to have" leaked in, all guardrails from spec are enforced.

## Commit Strategy

- Each task (1-15) commits independently on completion
- Each task has its own commit message per format: `type(scope): desc`
- Final verification wave (F1-F4) commits separately after all pass
- Commits are NOT pushed automatically — user runs `git push` after approving results

## Success Criteria

1. `noto record` → `noto stop` → meeting saved in `~/Noto/meetings/YYYY/MM/{id}/`
2. All artifact types (`transcript.v1`, `summary.v1`, `audio.v1`, `manifest.v1`) pass validation
3. `noto search "term"` returns ranked meeting list via FTS5
4. `noto sync --dry-run` produces valid S3 PUT requests
5. All 8 TUI screens render without visual artifacts
6. All testing.md fixtures pass acceptance matrix
7. Binary builds: `go build ./cmd/noto`
8. `golangci-lint run ./...` passes with no errors

# Tech Stack Reference

## Purpose

Define default choices and their replaceable boundaries.

## Default Stack

V1 defaults are local and terminal-first: Go CLI/TUI, a minimal native macOS
capture helper, filesystem artifacts, SQLite FTS5, and provider adapters for
recorded/imported audio and transcripts. Object storage, remote API, async
workers, and local transcription are post-V1 defaults.

| Area | Default | Reason | Replaceable boundary |
| --- | --- | --- | --- |
| CLI/TUI | Go, Cobra-style commands, Bubble Tea TUI | Main interface, single binary UX, JSON automation, low idle cost. | CLI surface |
| macOS capture | Native helper, ScreenCaptureKit, AVFoundation | Permissions, recording lifetime, and split-source attribution. | `CaptureController` |
| Local storage | Filesystem JSON/Markdown plus checksums | Portable source of truth. | `ArtifactStore` |
| Search | SQLite FTS5 | Embedded, rebuildable, no daemon. | `IndexProcessor` |
| Audio preparation | Native pipeline first; ffmpeg-compatible adapter if needed | Keep install simple, allow proven codecs when required. | `AudioProcessor` |
| STT | AssemblyAI plus one second cloud provider for parity tests | Async diarization and implementation comparison. | `TranscriptionProcessor` |
| Summaries | OpenAI-compatible adapter | Works with hosted models and local-compatible endpoints. | `SummaryProcessor` |
| Post-V1 object storage | S3-compatible storage, with R2 as first target | Supports local object-store and hosted signed-access modes. | `SyncGateway` |
| Post-V1 remote API | Small Go service or Cloudflare Workers spike | Must stay thin: auth, policy, signed access, metadata, jobs. | `RemoteSyncGateway` |
| Post-V1 remote metadata | Postgres-compatible schema | Clear tenancy, device, manifest, audit, and billing tables. | server repository layer |
| Post-V1 async work | Queue-backed workers | Provider jobs must not run in request handlers. | job backend |

## Efficiency Rules

- Native capture helper records only when active and exposes state over a local socket.
- Native capture helper preserves microphone and system/app audio as separate sources whenever macOS allows it.
- `noto` exits after commands; the TUI has no hot render loop when idle.
- V1 has no LaunchAgent, idle HTTP server, sync daemon, or detached transcription worker.
- Local gateway mode is post-V1 and must not start a daemon or HTTP server.
- Post-V1 sync uses explicit commands, file events, or low-frequency backoff.
- Post-V1 remote handlers issue signed object access and enqueue work; they do not buffer large audio.
- SQLite is opened on demand and can be rebuilt from artifacts.

## Testability Rules

- Every replaceable boundary has fixture-backed contract tests.
- CLI JSON output has golden or schema tests before agents rely on it.
- TUI behavior is tested through Bubble Tea model tests and snapshots.
- Provider integration tests are optional locally, but provider normalizer contract tests are required.
- Capture tests must prove split mic/system source roles even when transcription is mocked.

See [testing.md](./testing.md) for the full TDD gate.

## Open Technical Decisions

| Decision | Options | First validation |
| --- | --- | --- |
| Remote API runtime | Go service, Cloudflare Workers | Signed URL flow, device token auth, manifest commit latency. |
| Metadata DB | Postgres, D1 for small self-hosted | Manifest conflict transaction and audit append tests. |
| Local STT | FluidAudio, WhisperKit/SpeakerKit, whisper.cpp | 30-minute runtime, RAM, DER/WER against benchmark fixture. |
| Audio format | M4A/AAC, WAV, FLAC | Provider compatibility, size, conversion cost, timestamp stability. |
| Summary model | Hosted OpenAI-compatible, local model | Citation faithfulness and action-item extraction. |
| Background job owner | TUI foreground task, explicit CLI command, future LaunchAgent/helper | Exit behavior, crash recovery, and user-visible cancellation. |

## Anti-Requirements

- No Electron shell for V1.
- No GUI-first app surface for V1.
- No required hosted database for local use.
- No provider-native payload as a downstream contract.
- No remote API dependency for capture, local search, or reading artifacts.
- No sync or hosted API in V1.

## References

- [Bubble Tea package docs](https://pkg.go.dev/github.com/charmbracelet/bubbletea)
- [Cobra command and flag model](https://github.com/spf13/cobra)
- [SQLite FTS5](https://sqlite.org/fts5.html)
- [Apple ScreenCaptureKit capture sample](https://developer.apple.com/documentation/ScreenCaptureKit/capturing-screen-content-in-macos)
- [Apple Launch Daemons and Agents](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)

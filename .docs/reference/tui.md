# TUI Reference

## Goal

`noto` should be the primary user interface: keyboard-first, local-only for V1,
clear about recording state, and fast for meeting browsing. The native macOS
component is only a capture helper.

## Screens

| Screen | Purpose |
| --- | --- |
| Dashboard | Recording state, recent meetings, foreground jobs, index state, provider state. |
| Recorder | Elapsed time, separate me/mic and participant/system meters, capture mode, retention state. |
| Meetings | Searchable meeting list grouped by date/status. |
| Meeting detail | Summary, transcript, actions, files, versions. |
| Transcript | Timestamped speaker turns with segment IDs. |
| Providers | Provider status, benchmark notes, active defaults. |
| Storage | Local artifact health, checksums, verify/repair. |
| Settings | Paths, retention, prompts, keybindings. |

## Default Layout

```text
 Noto                                        rec: idle  index: clean
+--------------------------+-----------------------------------------+
| Meetings                 | Product architecture sync               |
| / search meetings        | 42m 36s  summarized  STT baseline       |
| today                    | Decisions                               |
| > Product sync           | 1. Use post-meeting diarization.        |
|   Vendor benchmark       | 2. Keep artifacts local-first.          |
+--------------------------+-----------------------------------------+
| Jobs                     | Transcript                              |
| ok indexed               | 00:14:02 Speaker 1  AssemblyAI is...    |
| ok summary               | 00:14:28 Speaker 0  But source roles... |
+--------------------------+-----------------------------------------+
 ? help   r record   i import   / search   tab pane   enter open   q quit
```

## Keys

| Key | Action |
| --- | --- |
| `?` | Help. |
| `r` | Start or open recording controls. |
| `i` | Import audio or transcript. |
| `/` | Search/filter current pane. |
| `:` | Command palette. |
| `tab` | Next pane. |
| `enter` | Open selected item. |
| `v` | Verify local artifacts and search index. |
| `p` | Providers screen. |
| `q` | Back/quit. |

## Job Rules

- Active recording is owned by the native macOS capture helper, not the TUI process.
- `esc` exits the TUI but leaves active recording alive.
- Stop requires confirmation if capture has errors or duration is under 10 seconds.
- Raw-audio retention state is always visible during and after recording.
- Mic/system source roles are always visible during recording because they drive local-speaker vs participant attribution.
- V1 jobs are foreground child tasks owned by the current CLI/TUI process.
- Long tasks show status, current step, elapsed time, and cancellability.
- Exiting the TUI asks before cancelling an active mutating job.
- Finished jobs promote artifacts only after schema and checksum validation.
- Partial job outputs stay in `.tmp/`.

## Performance Rules

- No global render loop.
- Idle dashboard does not tick.
- Progress updates tick only while a job is active.
- Recording status pulses at most once per second; separate me/participant audio meters update only while visible.
- Transcript view virtualizes visible rows.
- Wrapping is cached by terminal width.
- Jobs run outside the TUI event loop.

See [design.md](../design.md) for recording animation, empty states, responsive rules, and ASCII layout sketches.

## Test Rules

- Model tests cover key handling, pane focus, job state transitions, and recorder state.
- Snapshot tests cover dashboard, recorder, search, meeting detail, transcript, settings, empty states, and error states.
- Idle tests prove the TUI does not tick when no recording, job, spinner, or meter is visible.
- Recording tests verify that source meters display me/mic and participants/system separately.
- Every important TUI state must have equivalent JSON state through `noto status --json`, `noto verify --json`, or artifact commands.

See [testing.md](./testing.md) for the full validation plan.

## References

- [Bubble Tea](https://github.com/charmbracelet/bubbletea)
- [Bubbles viewport](https://pkg.go.dev/github.com/charmbracelet/bubbles/v2/viewport)
- [Bubbles list](https://pkg.go.dev/github.com/charmbracelet/bubbles/v2/list)
- [Bubbles table](https://pkg.go.dev/github.com/charmbracelet/bubbles/table)

# TUI Reference

## Goal

`noto` is the primary human interface: keyboard-first, remote-backend-aware,
clear about recording state, and fast for meeting browsing.

## Screens

| Screen | Key | Purpose |
| --- | --- | --- |
| Dashboard | `1` | Meetings list + search, embedded detail pane, jobs strip, recording strip |
| Recorder | `r` / `3` | Start/stop a recording, live waveform, markers |
| Config | `,` / `4` | Active routes, API keys, storage, paths |
| Agent | `a` | Per-meeting agent handoff: file paths + copyable CLI commands |

The Dashboard is the default screen and combines browsing, FTS search, and detail
viewing in one surface. There is no separate "meetings list" or "transcript" screen —
the detail pane (right column) shows Summary, Transcript, and Speakers tabs.

## Dashboard Layout

```
┌── dashboard (1/3) ─────────────────────────────────────────────────┐
│ / search…               3 total                                     │
│                                                                      │
│  ▸ Product sync         Jan 05 14:30   ◆ 2  ▸ 1  ⚠ 1               │
│    Sprint planning      Jan 04 09:00   ·                            │
│    1:1 with Maya        Jan 03 11:00   ◆ 1  ▸ 2                    │
│                                                                      │
│  / search  ↑↓ navigate  ⏎ open  n next  x clear                     │
├── jobs ──────────────────┬── recording ─────────────────────────────┤
│  transcribe ▓▓░░░░  done │  ○ idle                                  │
│                          │  press r to record                        │
└──────────────────────────┴──────────────────────────────────────────┘
                                                                       
┌── details (2/3) ─────────────────────────────────────────────────────┐
│ Product sync                                         Jan 05  14:30   │
│  [Summary]  [Transcript]  [Speakers]                                 │
│                                                                       │
│  Three decisions on the roadmap. Timeline risk flagged.              │
│  ◆ Ship v1 by end of Q2                                              │
│  ◆ Keep terminal as primary UI                                        │
│  ▸ Maya to circulate updated timeline by Friday                      │
└───────────────────────────────────────────────────────────────────────┘
```

The bottom strip (jobs + recording) is hidden when both are idle.

## Keys

### Global

| Key | Action |
| --- | --- |
| `?` | Toggle help overlay |
| `:` | Command palette |
| `q` / `ctrl+c` | Back / quit |
| `esc` | Back / close input / unfocus pane |
| `1` | Go to dashboard |
| `r` / `3` | Go to recorder |
| `4` / `,` | Go to config |
| `a` | Open agent handoff for selected meeting |
| `ctrl+r` | Refresh meetings list |

### Dashboard — list focus

| Key | Action |
| --- | --- |
| `↑` / `↓` | Move cursor |
| `⏎` / `tab` | Focus detail pane |
| `/` | Focus search input |
| `n` / `N` | Jump to next / previous search match |
| `x` | Clear search |
| `t` | Switch detail pane to Transcript tab |
| `k` | Switch detail pane to Speakers tab |
| `d` | Delete selected meeting |

### Dashboard — detail pane focus

| Key | Action |
| --- | --- |
| `←` / `→` or `h` / `l` | Switch tabs (Summary / Transcript / Speakers) |
| `↑` / `↓` or `j` / `k` | Scroll active tab |
| `n` / `N` | Jump to next / previous search match in transcript |
| `tab` / `esc` | Return focus to list |
| `d` | Delete meeting |

### Recorder

| Key | Action |
| --- | --- |
| `i` | Edit title |
| `r` | Start recording |
| `s` | Stop recording |
| `n` | Drop a marker |
| `esc` | Back (active recording stays alive) |

## Connection Modes

The TUI connects to `noto serve` in three ways, controlled by env vars:

| Mode | How | When |
| --- | --- | --- |
| In-process (default) | `noto` auto-starts an in-process server | No server running, no `NOTO_API_URL` |
| Local daemon | `noto` probes the local UDS, connects if alive | `noto serve` is running separately |
| Remote | `NOTO_API_URL=http://host:port noto` | Remote server, optionally with `NOTO_API_TOKEN` |

The TUI code is identical in all three modes — it always uses `notoapi.Client`.

## Status Bar (bottom row)

```
● REC  02:14   1 job   12 meetings   noto 0.1.0
```

| Indicator | Meaning |
| --- | --- |
| `● REC` | Recording is active, shows elapsed time |
| `N job(s)` | Running or recently-completed pipeline jobs |
| `N meetings` | Total meeting count in storage |

## Performance Notes

- No global render loop. Idle TUI does not tick.
- Progress updates run only while a job is active.
- Recording meters pulse at 8 Hz while recording; zero cost when stopped.
- Jobs run in the backend, outside the TUI event loop.
- Search fires on every keypress; results are debounced via query equality check.

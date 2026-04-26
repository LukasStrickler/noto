# Noto Terminal Design

This file is the product design workbench. It uses ASCII sketches so we can iterate in the same medium the product uses.

## Design Thesis

Noto should feel like a terminal-native meeting memory workbench:

- The human interface is the `noto` TUI.
- The agent interface is `noto --json` plus local artifact paths.
- The native macOS helper is invisible unless permissions or active capture need attention.
- The UI is evidence-first: summaries are useful, but transcript segments are the source of truth.

The design should not feel like a GUI squeezed into a terminal. It should feel closer to a calm operations console for meeting evidence: focused, fast, legible, and scriptable.

## Research Inputs

| Input | What it changes in Noto |
| --- | --- |
| Bubble Tea model/update/view | Keep state transitions explicit and testable. |
| Bubbles list/viewport/table | Use proven primitives for meeting lists, transcript scrollback, and artifact tables. |
| Bubbles spinner/progress/timer | Animate only active jobs, recording meters, and elapsed time. |
| Lazygit-style panes | Use stable panes and keyboard navigation for dense workflows. |
| Keyboard UI guidance | Visible focus, visible shortcuts, no pointer-only actions. |
| Accessibility guidance | Never communicate recording or errors through motion/audio alone. |
| Terminal-agent research | Keep human and agent state representationally compatible through JSON and files. |

Sources:

- Bubble Tea: https://pkg.go.dev/github.com/charmbracelet/bubbletea
- Bubbles: https://github.com/charmbracelet/bubbles
- Bubbles list: https://pkg.go.dev/github.com/charmbracelet/bubbles/v2/list
- Bubbles viewport: https://pkg.go.dev/github.com/charmbracelet/bubbles/v2/viewport
- Lazygit: https://lazygit.dev/
- Keyboard UI guidance: https://learn.microsoft.com/en-us/previous-versions/windows/desktop/dnacc/guidelines-for-keyboard-user-interface-design
- Apple accessibility guidance: https://developer.apple.com/design/human-interface-guidelines/accessibility/
- Terminal agent collaboration: https://arxiv.org/abs/2603.10664

## Product Modes

```text
Idle
  browse meetings
  search evidence
  inspect files

Recording
  capture helper active
  TUI shows elapsed time, mic/system sources, meters, markers, retention

Processing
  ingest -> transcribe -> summarize -> index
  foreground jobs visible and cancellable where safe

Evidence
  search result -> segment -> summary claim -> files

Agent Handoff
  copy JSON command
  copy local artifact path
  cite segment
```

## Motion Rules

Terminal animation must be useful, cheap, and stoppable.

| State | Motion | Frequency | Reduced motion |
| --- | --- | --- | --- |
| Idle | none | none | same |
| Recording status | two-frame pulse plus elapsed time | 1 Hz | static `REC` plus elapsed time |
| Mic/system meters | separate level bars with peak hold/decay | 8-12 Hz when recorder visible | numeric dB values only |
| Job running | spinner or progress bar | 4-8 Hz | static phase label |
| Search/filter | no animation | instant | same |
| Pane focus | border/style change | instant | same |
| Errors | persistent label | no blinking | same |

Implementation notes:

- Use `tea.Tick` only while a visible active state needs updates.
- Do not run a global render loop.
- Do not animate transcript scrolling or keyboard focus movement.
- Avoid blinking-only indicators. Recording must have text, elapsed time, and color/style.
- Prefer short loops with semantic frames over decorative motion.

## Recording Animation

The status bar should make active capture impossible to miss without creating noise.

```text
frame A: REC * 00:12:48  me -14dB  participants -22dB  temp ok
frame B: REC . 00:12:49  me -13dB  participants -21dB  temp ok
```

Reduced motion:

```text
REC 00:12:49  me -13dB  participants -21dB  temp ok
```

Recorder pane meter:

```text
me/mic        [############....|.] -14 dB  peak -08
participants  [########........|.] -22 dB  peak -15
```

Meter behavior:

- Filled bar follows current RMS.
- `|` peak marker holds briefly, then decays.
- Clipping turns the label into persistent `CLIP`, not a flash.
- If an input disappears, freeze the last value and show `lost input`.

```text
me/mic        [##################] CLIP  peak 0
participants  [???? input lost ???] last -22 dB
```

Source labels:

- `me/mic` means local microphone and defaults to local speaker.
- `participants` means system/app audio and defaults to remote meeting participants.
- If a source is mixed or unavailable, label it honestly as `mixed` or `unknown` instead of pretending diarization is certain.

## Source-Aware Attribution

Split-source capture is a V1 product invariant. The UI, artifacts, provider
adapters, and agent commands should keep the source role visible until a human
intentionally renames a speaker.

```text
+------------------------+----------------------+----------------------+
| Capture source         | Default role         | Transcript hint      |
+------------------------+----------------------+----------------------+
| me/mic                 | local_speaker        | "me"                 |
| participants/system    | participants         | "participants"       |
| mixed                  | mixed                | needs review         |
| unknown                | unknown              | do not infer         |
+------------------------+----------------------+----------------------+
```

Rules:

- Speaker rename changes display names, not source origin.
- Summary and search citations should include source role when it helps distinguish who said something.
- Agent JSON must expose `source_id`, `source_role`, and speaker origin when available.
- If the provider loses channel/source information, keep the original source metadata and mark transcript attribution as `mixed` or `unknown`.

## New Meeting Flow

The recording path should feel intentional, not like a raw command wrapper.

```text
1. Press r or run noto record
2. TUI opens New Recording panel
3. User confirms title/source/retention
4. Capture helper preflights permissions
5. Recording starts
6. TUI shifts into recording-active layout
7. Stop creates ingest job
8. Ingest produces artifacts
9. User can transcribe now or later
```

New recording panel:

```text
+ New recording ------------------------------------------------+
| title      Roadmap sync                                      |
| source     (x) me/mic  (x) participants/system audio          |
| retention  delete raw audio after valid transcript           |
| provider   benchmark-selected STT                            |
| after stop [x] ingest  [x] transcribe  [x] summarize         |
|                                                               |
| permissions: mic ok   screen/system audio ok                 |
| attribution: mic -> me, system/app audio -> participants      |
+---------------------------------------------------------------+
enter start  tab next  space toggle  esc cancel
```

Permission needed:

```text
+ Permission needed -------------------------------------------+
| Noto needs microphone permission before recording.            |
|                                                               |
| The native capture helper will trigger the macOS prompt.      |
| After permission is granted, return here and press r again.   |
+---------------------------------------------------------------+
enter open prompt  esc back
```

Active recording compact layout:

```text
+ Noto -------------------- REC * 00:12:48  idx clean  jobs idle +
| r recorder  n marker  / search  tab pane  q quit TUI           |
+ Meetings ------------------------+ Live recording --------------+
| today                            | Roadmap sync                 |
| > Roadmap sync        recording  | me/mic [###########.....|.]  |
|   Vendor benchmark    summarized | parts  [######.........|.]  |
|                                  | markers                      |
|                                  | 00:08 pricing decision       |
+ Jobs ----------------------------+ Notes -----------------------+
| capture helper active            | - ask about onboarding owner |
| temp .tmp/recording.m4a          |                              |
+----------------------------------+------------------------------+
```

Stop flow:

```text
+ Stop recording? ----------------------------------------------+
| Roadmap sync                                                   |
| duration 42:16   source me/mic + participants/system   temp ok |
|                                                               |
| After stop: ingest -> transcribe -> summarize -> index         |
+---------------------------------------------------------------+
enter stop  t stop + transcribe  s stop only  esc keep recording
```

Post-stop pipeline:

```text
+ Roadmap sync -------------------------------------------------+
| stopped 42:16                                                 |
+ Pipeline -----------------------------------------------------+
| ingest      [##########] done      audio.json                 |
| transcribe  [######....] 61%       provider job stt_123       |
| summarize   [..........] waiting                             |
| index       [..........] waiting                             |
+ Evidence preview ---------------------------------------------+
| transcript will appear here when segments validate             |
+---------------------------------------------------------------+
```

## Default Layout: Command Center

This is the default V1 candidate. It must work as the main daily interface.

```text
+ Noto ------------------- rec idle  idx clean  jobs 1  1204 mtg +
| ? help  r record  i import  / search  : command  q quit        |
+ Meetings -----------------------+ Meeting ----------------------+
| today                           | Product architecture sync     |
| > Product sync       42m  done  | 42m  summarized  3 speakers   |
|   Vendor benchmark   28m  todo  |                               |
| yesterday                       | Decisions                     |
|   Hiring loop        51m  sum   | 1. Keep terminal as main UI    |
|   Agent notes        12m  text  | 2. Use artifacts as source     |
+ Jobs ---------------------------+ Transcript -------------------+
| stt  Vendor benchmark queued    | 00:14:02 Maya   Terminal UI..  |
| idx  clean            1204 mtg  | 00:14:28 Lukas  Agents need..  |
| files ready           3 paths   | 00:15:10 Maya   Cite segment.. |
+---------------------------------+-------------------------------+
```

Design decision:

- Meetings list is the spine.
- Right side changes by focus.
- Bottom left is operational state.
- Bottom right is evidence preview.

## Evidence Search

Search should answer "where did this come from?"

```text
+ Search: pricing decision ---------------------------- 18 hits +
| scope all  type all  speaker any  date any                     |
+ Results ------------------------+ Evidence ---------------------+
| > Product sync    00:14:02 Maya | Product sync                 |
|   pricing stays per-seat...     | 2026-04-24  42m              |
|                                 |                              |
|   Vendor bench   00:33:18 Chen  | seg_000210 Maya 00:14:02    |
|   enterprise price needs...     | pricing stays per-seat...    |
|                                 |                              |
|   GTM review     00:08:41 Sam   | Summary claim                |
|   discount only yearly...       | Decision 2 cites this seg    |
+---------------------------------+------------------------------+
enter open  c copy citation  f filter  j/k move  esc back
```

Innovation to preserve:

- Search result and evidence are side by side.
- Copy citation is a first-class action.
- The TUI result shape mirrors `noto search --json`.

## Meeting Detail

Meeting detail should keep summaries accountable to transcript evidence.

```text
+ Product architecture sync -------- 42m summarized clean -------+
| tabs summary | transcript | actions | files | versions         |
+ Summary -----------------------+ Evidence ---------------------+
| Short                          | seg_000210 Maya 00:14:02     |
| Terminal-first V1 with native  | "The terminal should be the   |
| capture helper and JSON agent  | main interface..."            |
| access.                        |                              |
|                                | seg_000245 Lukas 00:16:44    |
| Decisions                      | "Agents need file paths..."   |
| 1. TUI is primary interface    |                              |
| 2. Agents use JSON and paths   |                              |
+ Actions -----------------------+ Files ------------------------+
| [ ] Add .docs/design.md        | transcript.diarized.json      |
| [ ] Add files --json           | summary.md                    |
+----------------------------------------------------------------+
```

## Transcript View

The transcript view is where trust is earned.

```text
+ Transcript: Product architecture sync ------------------------+
| speaker filter all   copied citation none   segments 283       |
+ Timeline ----+ Transcript -------------------------------------+
| 00:00 intro  | 00:14:02 Maya                                   |
| 00:08 price  | [me] Terminal UI should be the primary interface,|
| 00:14 design | with the native app only handling capture.       |
| 00:22 agent  |                                                   |
|              | 00:14:28 Lukas                                  |
|              | [participants] Agents need JSON and direct       |
|              | artifact paths, not a UI they have to scrape.   |
+--------------+--------------------------------------------------+
c copy citation  s rename speaker  / search transcript  esc back
```

Timeline notes:

- Left rail shows markers, decisions, and search hits.
- Transcript body stays virtualized.
- Segment IDs are visible on demand or in copy output, not noisy by default.

## Agent Handoff

Make agent access visible to humans. This turns local files into a product advantage.

```text
+ Agent Access: Product architecture sync ----------------------+
| meeting_id   mtg_20260424_153012_ab12                         |
| version_id   ver_20260424_142001_c932                         |
|                                                               |
| transcript   ~/Noto/meetings/2026/04/.../transcript.json       |
| summary      ~/Noto/meetings/2026/04/.../summary.json          |
| markdown     ~/Noto/meetings/2026/04/.../summary.md            |
|                                                               |
| commands                                                      |
| noto transcript --json mtg_20260424_153012_ab12                |
| noto summary --json mtg_20260424_153012_ab12                   |
| noto files --json mtg_20260424_153012_ab12                     |
+---------------------------------------------------------------+
c copy command  p copy path  j/k move  esc back
```

This view is the human-readable version of the agent contract.

## Empty States

No meetings yet:

```text
+ Noto ---------------------------------------------------------+
| No meetings yet.                                              |
|                                                               |
| Start with one of these:                                      |
|                                                               |
| r  record a meeting                                           |
| i  import audio or transcript                                 |
| ?  open help                                                  |
+---------------------------------------------------------------+
```

No transcript yet:

```text
+ Vendor benchmark ---------------------------------------------+
| Audio is ingested, but no transcript exists yet.               |
|                                                               |
| t  transcribe with default provider                            |
| p  choose provider                                             |
| f  show artifact files                                         |
+---------------------------------------------------------------+
```

Provider missing:

```text
+ Provider setup -----------------------------------------------+
| No transcription provider is configured.                       |
|                                                               |
| Set an API key in the provider settings or import a transcript.|
+---------------------------------------------------------------+
p providers  i import transcript  esc back
```

## Responsive Rules

```text
>= 140 cols: 3 panes allowed
100-139 cols: 2 panes, jobs/status compressed
80-99 cols: single primary pane plus bottom status
< 80 cols: command/list mode, no preview panes
```

Collapse order:

1. Decorative labels.
2. Jobs pane.
3. Transcript preview.
4. Meeting metadata columns.
5. Secondary tabs.

Never hide:

- Recording active state.
- Focus location.
- Current command hints.
- Error state.

## Key Map Draft

```text
global
  ?          help
  /          search current scope
  :          command palette
  tab        next pane
  shift-tab  previous pane
  q          back/quit

recording
  r          start/open recorder
  s          stop recording when recorder focused
  n          add note/marker

meeting
  enter      open
  t          transcript
  a          actions
  f          files/agent handoff
  c          copy citation/path depending on focus

jobs
  x          cancel cancellable foreground job
  v          verify artifacts/index
```

## Visual Language

Use restrained color. Meetings are work data, not decoration.

```text
recording active     red or high-contrast inverse
success/clean        green only for status markers
warning/conflict     yellow
error                red
selected focus       strong border or inverse row
secondary metadata   dim
citations            cyan/blue, consistent with links
me/mic meter         green or neutral
participants meter   blue/cyan or neutral
audio meter hot      yellow
audio clipping       red label plus persistent text
```

Avoid:

- Dense rainbow status bars.
- Hidden shortcuts.
- More than three active panes.
- Summary-only views with no path back to transcript evidence.
- Animation that runs while idle.
- Blinking as the only signal.

## V1 Design Decision

Build these first:

1. Command Center default layout.
2. Recording-active layout with pulse, elapsed time, and separate me/participant audio meters.
3. Evidence Search with citation copy.
4. Meeting Detail with summary and evidence side by side.
5. Agent Handoff with commands and paths.

The first implementation should feel like:

```text
terminal-native + recording-aware + evidence-first + agent-readable
```

not:

```text
GUI squeezed into a terminal
```

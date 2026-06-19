# internal/ui/tui — Bubble Tea TUI

The most convention-heavy package in the repo. **Before changing keys or
layout, read the repo-root `CLAUDE.md`** — it is the single source of truth for:

- **Keybindings:** every key is defined once in `keys/` (`key.Binding`); every
  label in the UI is *derived* from that definition. Never hand-write a key
  string in a view or compare `msg.String() == "f"` for an action key.
- **Screen numbering:** the 1/2/3 keys are auto-assigned from the `topScreens`
  table in `screen.go`. Add a screen by adding a `screenReg` row, nothing else.
- **Layout:** all pane/row sizing goes through `layout.Split` — never hand-roll
  `width/3` or `total - other - 1`.
- **Mouse:** clickable/hoverable elements go through the `button` component +
  the `hit` map (CLAUDE.md → *TUI mouse*). A view never handles a raw
  `tea.MouseMsg` or computes click coordinates — it declares a `button` and
  gets hover, press, click routing, and keyboard parity for free.

`go test ./internal/ui/tui/...` enforces the keybinding/numbering invariants.

## Where things live

- `tui.go` — package entry (`Run`)
- `root.go` — root model, the global key router, the screen factory, the
  mouse/pointer event handlers (`frameHits`, `regionAt`, hover/press/release),
  and the per-frame render cache (`viewCache`/`dirty`): `View` rebuilds only on
  a state change, so the AllMotion hover firehose doesn't re-render the whole UI
  per cell. See CLAUDE.md → *Interaction semantics* for the dirty rules.
- `interactive.go` — the clickable `button` component (normal/hover/active/
  pressed states) + `pointer`/`region` + the `pointer.state` resolver; turns one
  literal into a hover- and click-aware element. Also the **FILL** feedback family
  `rowFeedback(line, st, s, width, protect)`/`fillRow`/`paintRow` for full-width
  list rows (active = a solid accent bar; `protect` keeps a leading `attnGutter`
  amber bar outside the fill) and `hitsIf`. The nav pills in `chrome.go` are the
  reference use; every screen body uses these. Inline text (detail tabs, chips)
  uses the **LABEL** family instead — `tabSeg` + theme `Label*`, which recolour
  the FONT only (no background), since a tab's active state is a colour, not a bar.
- `replay.go` — clickable action chips: `chipButton`/`chipButtonAs` render a
  key-binding chip that replays its key on click (`replayKey`), so a chip and its
  keystroke stay one definition. The universal hint row and every screen's action
  hints are built from these.
- `chrome.go` — the persistent frame around every screen: the top nav bar
  (numbered page pills + the `⚑ to identify` attention badge), the bottom
  system status bar, the global hint row, the banner, and the `chip*` helpers
- `help_overlay.go` — the full-keymap help panel + `overlayCenter`, the
  centered-overlay compositor the confirm/identify dialogs also use
- `screen.go` — `screenID` consts + the `topScreens` registry (screen numbering)
- `messages.go` — the `tea.Msg` types
- `events.go` — SSE/event-stream handling into the model
- `palette.go` — the command palette
- `helpers.go`, `speakers_helpers.go` — shared view/format helpers

Screens (each `screen_*.go`; large ones split into a `_view.go`):
- `screen_dashboard.go` + `dashboard_search.go` + `dashboard_view.go` — meeting
  list + search (the 1/3 + 2/3 split)
- `detail_pane.go` + `detail_pane_view.go` + `detail_pane_speakers_view.go` +
  `detail_pane_identity.go` — the meeting detail pane (model/lifecycle ·
  summary/transcript rendering · Speakers-tab rendering · Speakers-tab identity
  behaviour: person jump, rename editor, the Identify/reassign dialog)
- `screen_people.go` + `people_view.go` — the person directory (model/keys ·
  rendering, the confirm overlay, status badges)
- `screen_recorder.go` · `screen_config.go` · `screen_agent.go`

## Subpackages

- `keys/` — the key bindings (single source of truth)
- `layout/` — the `Split` flex solver (`Fixed`/`Flex`/`FlexMin`, `HStack`/`VStack`)
- `hit/` — mouse hit-testing: `Rect`, the generic `Map[T]`, and the `Row`
  builder that registers each rendered segment's rect (the `button` component
  in `interactive.go` is built on this)
- `scroll/` — the one vertical-scroll primitive: `Follow` (cursor-follow window
  offset), `Clamp` (free-scroll), `Bar`/`Needed` (the shared scrollbar gutter).
  The meeting list and the wheel handler use it; see CLAUDE.md → *TUI scrolling*.
- `theme/` — colors and lipgloss styles

## Local rule

The TUI is a thin client: it talks to the backend only via `notoapi.Client`,
never `platform/storage` or `platform/search`. See `internal/ui/AGENTS.md` and
the repo-root `AGENTS.md`.

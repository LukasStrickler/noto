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

`go test ./internal/ui/tui/...` enforces the keybinding/numbering invariants.

## Where things live

- `tui.go` — package entry (`Run`)
- `root.go` — root model, the global key router, and the `chip*` label helpers
- `screen.go` — `screenID` consts + the `topScreens` registry (screen numbering)
- `messages.go` — the `tea.Msg` types
- `events.go` — SSE/event-stream handling into the model
- `palette.go` — the command palette
- `helpers.go`, `speakers_helpers.go` — shared view/format helpers

Screens (each `screen_*.go`; large ones split into a `_view.go`):
- `screen_dashboard.go` + `dashboard_search.go` + `dashboard_view.go` — meeting
  list + search (the 1/3 + 2/3 split)
- `detail_pane.go` + `detail_pane_view.go` — meeting detail pane
- `screen_recorder.go` · `screen_config.go` · `screen_agent.go`

## Subpackages

- `keys/` — the key bindings (single source of truth)
- `layout/` — the `Split` flex solver (`Fixed`/`Flex`/`FlexMin`, `HStack`/`VStack`)
- `theme/` — colors and lipgloss styles

## Local rule

The TUI is a thin client: it talks to the backend only via `notoapi.Client`,
never `platform/storage` or `platform/search`. See `internal/ui/AGENTS.md` and
the repo-root `AGENTS.md`.

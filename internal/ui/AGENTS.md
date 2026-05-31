# internal/ui — user interfaces

The two human-facing surfaces: the Bubble Tea TUI and the CLI command set.

**Dependency rule:** talk to the backend **only** through `notoapi.Client`
(obtained from `app/host`). **NEVER** import `platform/storage` or
`platform/search` directly — that bypasses the service layer and the storage
seam.

**Packages**

| Package | Responsibility |
|---------|----------------|
| `tui` | Bubble Tea screens, models, and the `keys`/`layout`/`theme` subpackages |
| `cli` | All CLI verb handlers (each a thin `notoapi.Client` caller): `cli.go` (dispatch), `commands_*.go` (verbs), `helpers.go` |

**TUI conventions live in the repo-root `CLAUDE.md`:** keys are defined once in
`tui/keys` and every label is derived from that definition; pane/row sizing goes
through `tui/layout.Split` (never hand-rolled `width/3`). Read it before touching
keybindings or layout.

**Anti-pattern:** no direct storage/search access; no hand-written key strings
in views. See the repo-root `AGENTS.md` for the big picture.

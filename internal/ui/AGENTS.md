# internal/ui — user interfaces

The two human-facing surfaces: the Bubble Tea TUI and the CLI command set.

**Dependency rule:** talk to the backend **only** through `notoapi.Client`
(obtained from `app/host`). **NEVER** import `platform/storage` or
`platform/search` directly — that bypasses the service layer and the storage
seam.

## Packages

- `tui` — Bubble Tea screens, models, and the `keys`/`layout`/`theme`
  subpackages. See its own `AGENTS.md` for the per-file map and conventions.
- `cli` — all CLI verb handlers (each a thin `notoapi.Client` caller):
  `cli.go` (dispatch), `commands_*.go` (verbs), `helpers.go`

**Anti-pattern:** no direct storage/search access; no hand-written key strings
in views (see the TUI conventions in the repo-root `CLAUDE.md`). See the
repo-root `AGENTS.md` for the big picture.

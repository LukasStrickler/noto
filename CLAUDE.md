# noto — project guide for Claude

## Build / test

`go` is not on `$PATH`; the toolchain lives in the repo. Prefix commands:

```bash
export PATH="$PWD/.tools/go/bin:$PATH"
go build ./...
go test ./...
```

(The `Makefile` already does this via `$(GO)` — `make test`, `make build`.)

## TUI keybindings — single source of truth

The TUI (`internal/tui`) follows one rule: **every key is defined once, and
every label shown in the UI is derived from that definition.** There are no
hand-written key strings in the views — if you change a key, the chips, hint
bars, help overlay, and command palette all update automatically.

### Adding / renumbering a top-level screen (the 1/2/3 keys)

Screen numbers are **auto-assigned** from one ordered table —
`topScreens` in `internal/tui/screen.go`. A screen's number is its 1-based
position in that slice, so the run can never develop a gap (the old 1/3/4 bug).

To add a numbered screen:

1. add its `screenID` const in `screen.go`,
2. add a `screenReg` line to `topScreens` **in the position you want its
   number to be**,
3. that's it. The key binding, the global router's number handling
   (`screenNavTarget`), `buildScreen`, the command palette, and the help
   overlay all derive from the table. `go test ./internal/tui/...` verifies
   the numbering.

`screenReg` fields: `title` (short label), `palette` (long command-menu
label), `aliases` (mnemonic keys shown in help, e.g. `r`), `hidden` (alias
keys bound but not shown, e.g. `,`), `new` (constructor).

Screens reachable only contextually (e.g. the agent handoff, opened with `a`)
are deliberately **not** in `topScreens` — `buildScreen`'s fallback handles
them, and they get no number.

### Adding an action key (letters: delete, rename, jump, …)

1. add a `key.Binding` field to `keys.Map` in `internal/tui/keys/keys.go`,
   with `key.WithKeys(...)` and `key.WithHelp(keyLabel, desc)`,
2. handle it in the relevant screen with `key.Matches(msg, ctx.keys.Foo)` —
   never `msg.String() == "f"`,
3. show it in the UI with the chip helpers in `root.go`:
   - `chip(s, b)` — renders the binding's own key + description,
   - `chipAs(s, b, "label")` — binding's key with a context-specific label,
   - `chipPair(s, a, b, "label")` — two bindings as one `↑/↓ label` chip.

The help overlay (`renderHelpOverlay`) and `TestHelpOverlayDerivesKeysFromBindings`
assert that action keys actually appear — keep new keys wired into both the
handler and a chip so the test stays meaningful.

## TUI layout — one flex solver

Pane and row/column sizing goes through **`layout.Split`** (`internal/tui/layout/layout.go`).
Do **not** hand-roll `width/3`, `total - other - 1`, or `if h < min { h = min }`
in a screen — describe the row as `Slot`s and let the solver do the math:

- `layout.Fixed(n)` — an exact size (status bar, known-width sidebar),
- `layout.Flex(weight)` — grows to fill leftover space by weight,
- `layout.FlexMin(weight, min)` — flex with a floor.

`Split(total, gap, slots...)` reserves `gap` cells between sections, honors
mins, and hands the rounding remainder to the last flex slot so sizes sum to
exactly `total - gaps`. Pair it with `layout.HStack` (1-cell gap) / `layout.VStack`
(no gap) so the join matches the gap the solver accounted for — that's how a
row's rendered width stays equal to the width you split. The 1/3+2/3 dashboard,
the config sections split, the bottom strip, and the root content height are all
expressed this way. Tests live in `layout_test.go`.

### Conventions

- The root router intercepts global keys before the active screen; when a
  text input is focused (`screen.inputActive()`), only non-letter globals
  (ctrl+c, esc, `:`) fire so typing isn't hijacked.
- Arrow keys are canonical movement; vim `h`/`l` ride along only on the
  detail-pane tab-cycle bindings, where no input can be focused.
- esc/enter/tab handling *inside* a focused text field is allowed to compare
  `msg.String()` raw — those are field controls, not discoverable actions.

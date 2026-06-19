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

The TUI (`internal/ui/tui`) follows one rule: **every key is defined once, and
every label shown in the UI is derived from that definition.** There are no
hand-written key strings in the views — if you change a key, the chips, hint
bars, help overlay, and command palette all update automatically.

### Adding / renumbering a top-level screen (the 1/2/3 keys)

Screen numbers are **auto-assigned** from one ordered table —
`topScreens` in `internal/ui/tui/screen.go`. A screen's number is its 1-based
position in that slice, so the run can never develop a gap (the old 1/3/4 bug).

To add a numbered screen:

1. add its `screenID` const in `screen.go`,
2. add a `screenReg` line to `topScreens` **in the position you want its
   number to be**,
3. that's it. The key binding, the global router's number handling
   (`screenNavTarget`), `buildScreen`, the command palette, and the help
   overlay all derive from the table. `go test ./internal/ui/tui/...` verifies
   the numbering.

`screenReg` fields: `title` (short label), `palette` (long command-menu
label), `aliases` (mnemonic keys shown in help, e.g. `r`), `hidden` (alias
keys bound but not shown, e.g. `,`), `new` (constructor).

Screens reachable only contextually (e.g. the agent handoff, opened with `a`)
are deliberately **not** in `topScreens` — `buildScreen`'s fallback handles
them, and they get no number.

### Adding an action key (letters: delete, rename, jump, …)

1. add a `key.Binding` field to `keys.Map` in `internal/ui/tui/keys/keys.go`,
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

Pane and row/column sizing goes through **`layout.Split`** (`internal/ui/tui/layout/layout.go`).
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

## TUI scrolling — one viewport primitive

Vertical scrolling goes through **`scroll`** (`internal/ui/tui/scroll/scroll.go`),
the same way `layout.Split` owns sizing. A screen never hand-rolls
"first visible line = cursor − height/2" or a bespoke scrollbar — it describes
content as lines + a focus point and asks two things:

- **`scroll.Follow(total, height, cursorTop, cursorH, prefer)`** — the minimum
  scroll offset that keeps a (possibly multi-line) cursor row visible, starting
  from `prefer` (the current offset). The meeting list calls this from
  `followCursor` **only on arrow-key navigation** — selection drives the view.
- **`scroll.Clamp(offset, total, height)`** — pins a free-scroll offset into
  range. Both the transcript's `transcriptScroll` and the list's `listScroll` use
  it (the wheel pans, then clamps).
- **`scroll.Bar(height, total, offset, s)`** — the shared scrollbar gutter (one
  column; thumb sized/positioned proportionally). Thumb and track are the SAME
  glyph — a full block `█` — and differ ONLY by colour, NOT the `│` border glyph.
  This is the one drawing that renders identically in every terminal: a full block
  has no partial geometry and no dither, so the column stays constant. Earlier
  glyph-varying tracks both broke on macOS Terminal.app — a right-one-eighth `▕`
  rail drew at the wrong width (partial-block glyphs are poorly supported there),
  and a `░` shade is a dither whose dots don't tile across cells in some fonts.
  The thumb is a **neutral gray**
  (`ScrollThumb` = Subtle), deliberately not the Primary accent, so a scroll
  position never reads as the selection. Returns `""` when content fits; pair with
  **`scroll.Needed(total, height)`** to reserve the gutter column before rendering.

**Scroll ≠ select.** The list keeps its scroll position (`listScroll`, in lines)
SEPARATE from the cursor:

- the **wheel** moves `listScroll` and nothing else — it pans the viewport; the
  selection and the detail pane stay put (`handleWheel`);
- the **arrow keys** move the cursor, then `followCursor` nudges `listScroll` the
  minimum to keep it visible;
- **clicking** a row is what changes the selection.

`renderList` only *clamps* `listScroll` for display (pure — never writes back, so
a resize self-corrects and `View` doesn't mutate the model). It reserves a column
when `Needed`, paints rows at `innerW-1`, registers hit regions at the *windowed*
y, joins `Bar` on the right (`padCells` anchors it), and pins the hint bar to the
panel bottom so an overflowing list no longer clips the hints. `listLineLayout` /
`rowLineCount` / `listRowBudget` give `Update` the same geometry the view uses,
without rendering. Tests: `scroll/scroll_test.go`, `dashboard_scroll_test.go`.

**Wheel** is wired in the root: `tea.MouseWheelMsg` resolves the region under the
pointer (the same hit map clicks use) and forwards a `mouseWheelMsg{over, up}` to
the active screen, which scrolls *whatever the pointer is over* without it being
focused first — over `dash:pane`/`dash:tab:*` it scrolls the pane; over the list
it pans `listScroll`. Add a new scrollable region by handling `mouseWheelMsg` and
branching on `over`.

**The narrow sidebar compacts dynamically; the wide detail pane stays spelled
out.** Widths in `renderList`/`renderListRowDefault` are *measured*, not guessed
with shoulder constants — the breakpoint is wherever content would actually
overflow:

- `rowInnerW = rowW − 3` (the gutter), and `rowW` is `innerW` minus the
  scrollbar column when `scroll.Needed` (decided up front from the
  width-independent line count). Reserving the bar's column BEFORE clipping is
  what stops `gutter + content + bar` from running one cell past the panel and
  wrapping the row onto a new line (`TestListNoWrapWithScrollbar`).
- row 1's title gets `width − lipgloss.Width(meta)` (the measured date+status), so
  it fills the real remainder instead of a fixed `width−22`.
- row 2's counter strip uses the spelled form (`◆ 2 decisions   …` via
  `renderCounterStrip`) or the icon+count form (`◆2 ▸3 ⚠1 ?2` via
  `renderCounterStripCompact`), decided ONCE for the whole list by
  `listCountsCompact` — if any row's full counts+flag overflow the column, *every*
  row compacts, so the break point is identical across rows (a flagged row's wider
  `⚑N` never compacts a step earlier than a plain one). The singular/plural words
  are padded to a constant width (`countWord`) so columns line up regardless of
  count. A fully-resolved meeting shows no rollup at all — the list flags only
  outstanding work (`⚑N`), never a "done" badge. (`TestSidebarCounterStripCompactsWhenNarrow`,
  `TestSidebarCounterCompactionIsListWide`, `TestCounterStripConstantWidth`.)

The detail pane is the wide column and deliberately keeps the **full** tab labels
and full words — compaction belongs to the cramped sidebar, not there. A new
strip in a narrow column should likewise measure and step down ("spelled if it
fits, else icons") rather than assume width.

### Conventions

- The root router intercepts global keys before the active screen; when a
  text input is focused (`screen.inputActive()`), only non-letter globals
  (ctrl+c, esc, `:`) fire so typing isn't hijacked.
- Arrow keys are canonical movement; vim `h`/`l` ride along only on the
  detail-pane tab-cycle bindings, where no input can be focused.
- esc/enter/tab handling *inside* a focused text field is allowed to compare
  `msg.String()` raw — those are field controls, not discoverable actions.

## TUI detail-pane body — one renderer, one left-edge rule

Every detail-pane tab body (summary, actions, decisions, risks, questions,
transcript, speakers) goes through **`paneBody`** (`detail_pane_view.go`), the
same way `layout.Split` owns sizing and `scroll` owns the gutter. A tab body
never windows itself or draws its own scrollbar — it hands `paneBody` a
`renderAt(contentW)` callback and a scroll model, and gets back a block that
**wraps** (no line spills past the right edge) and **scrolls** (content is never
hidden past the fold — a thumb appears instead). `paneBody` calls `renderAt` once
at full width to count lines, then **again at `width-1`** when a scrollbar is
needed, so a width-tuned row (the speakers tab's talk-time bars) is rebuilt one
cell narrower rather than re-wrapped into a stray one-cell line. Two scroll
shapes, mirroring the `scroll` package: free-scroll (`focus == nil`, the summary)
and cursor-follow (`focus(contentW)` returns the line span to keep visible — the
items, the transcript's active segment, the selected speaker). Text tabs pass
`wrapText(content)`; `wrappedLineOf` maps a source line to its wrapped index for
the cursor-follow focus. `TestAllTabBodiesWrapAndScroll` asserts all seven tabs
wrap-and-scroll at once.

**One left-edge rule, derived from the body's shape:** reading content is
**flush at column 0** and uses the full width — the summary AND the transcript
(`[MM:SS] speaker` header, spoken text wrapped full width beneath, segments
separated by a blank line); the transcript's active segment is marked
**width-neutrally** by highlighting its timestamp, so focus moving never shifts
text. A discrete-entry list with a per-row cursor reserves a **2-cell marker
gutter** (`▸ ` on the focused row, blank otherwise) — the item lists; one that
must show a cursor **and** an attention flag at once uses the 3-cell `attnGutter`
(speakers). Do **not** invent a new indent — the transcript used to waste a 4-cell
header pad + 6-cell body indent; that's the anti-pattern this rule replaced. Pick
flush vs. gutter from the body's shape, and let `paneBody` wrap to the width left.

## TUI mouse — one hit map, one component

Mouse interactivity follows the same single-definition spirit as the keys: a
view **never computes click coordinates and never handles a raw `tea.MouseMsg`.**
It declares a `button`; the framework wires hover, press, click routing, and
keyboard parity. Three layers (all in `internal/ui/tui`):

- **`hit/`** — pure geometry. `Map[T]` answers "what's at cell (x,y)?"; the
  `Row` builder lays out a horizontal strip and registers each segment's rect as
  it appends it, so nothing hand-computes a column. Generic payload, no UI deps.
- **`interactive.go`** — the component. `button{id, active, onClick, render}` +
  `button.place(row, pointer)` resolves the visual `uiState`
  (normal/hover/active/pressed) via the shared `pointer.state(id, active)` rule,
  draws it, and registers the hit region. One literal per clickable element. Also
  the body-row painters: `paintRow` lays a continuous background under an
  already-styled multi-segment line (lipgloss resets otherwise clear it), and
  `rowFeedback(line, st, s, width, protect)` is the **FILL** feedback family for
  full-width list rows — active = a solid accent bar across the whole row (the
  selected meeting reads like a lit nav pill, not a one-cell ▸ marker); press =
  the brighter re-click flash; hover = a subtle raised fill keeping segment
  colours; normal = untouched. `protect` is the count of leading cells the fill
  must leave alone — flagged rows pass `1` so the amber attention `▍` (column 0)
  stays beside the fill instead of being flattened under it. See "two feedback
  families" below for why inline text buttons do NOT use this.
- **`replay.go`** — clickable chips. `chipButton`/`chipButtonAs` place a
  key-binding chip that lights on hover and, on click, **replays its key**
  (`replayKey` → a synthesised `tea.KeyPressMsg` routed back through `Update`), so
  a chip and its keystroke are one action defined once. `placeChip` takes a custom
  `onClick` for chips that must do more first (e.g. config focuses the right pane
  before the key fires).
- **wiring** — the root rebuilds a per-frame `hit.Map` (`m.frameHits`) in
  `content()`, resolves pointer events against it (`regionAt`), and hands screens
  `ctx.hitRow(x, y)` + `ctx.pointer()` (`screen.go`) so a screen body registers
  regions exactly like the chrome does. `hitsIf(ctx, clickable)` returns the map
  or nil so a render pass that must register nothing (a sub-mode owns input)
  shares the same code path. Every top-level screen (dashboard, people, config,
  recorder) threads its panel `BodyOffset()` into its render funcs to register
  rows/tabs/chips; the universal hint bar (chrome) is clickable on every screen.

### Adding a clickable element

Chrome (root-owned rows) and screen bodies use the identical idiom — only the
row source differs (`m.frameHits`-based in chrome, `ctx.hitRow(x, yLocal)` in a
screen, which offsets body-local rows by the header height):

```go
button{
    id:      "people:row:" + id,        // UNIQUE across the whole frame
    active:  id == selected,            // is this the focused/selected one?
    onClick: func() tea.Cmd { … },      // or replay a key — see navClick
    render:  func(st uiState) string { return rowFor(st) },
}.place(row, ctx.pointer())             // chrome: m.pointer()
```

Rules:

- **`render` must be the SAME WIDTH in every `uiState`** — hover/press/active
  recolour, never resize, so a row can't reflow under the pointer.
- **ids are globally unique** (prefix by element, e.g. `nav:`, `people:row:`) —
  hover/press track elements by id across frames.
- Prefer **replaying the canonical key** for anything that already has a binding
  (`navClick` → `pushOrReplaceTo`), so behaviour stays defined once.

Pick the right tool for the shape:

- **single-line strip of segments** (nav pills, a tab bar) → `hit.Row` +
  `button.place` per segment;
- **a rich full-width list row** (meeting/person/section rows) → register a
  `hit.Rect` for the whole row, resolve `ctx.pointer().state(id, selected)`, and
  wrap each rendered line in `rowFeedback(line, st, s, innerW, protect)` (`protect`
  = 1 only when the row carries an `attnGutter` amber bar, else 0);
- **an action hint** that maps to a key → `chipButton(row, ptr, s, id, binding)`,
  which replays the key on click. Track the chip row's absolute `y` (count the
  lines you've appended, or sum `lipgloss.Height` past a multi-line block) and
  build the `hit.Row` at `originX, originY+lineIdx`.

### Two feedback families (don't generalise one over the other)

Pointer feedback comes in two deliberately distinct languages — pick by the
element's *shape*, and keep them apart so the UI reads consistently:

- **FILL** — full-width surfaces (nav pills, meeting/person/section/key rows).
  Active = a solid accent **bar** across the whole element (`RowSelected`), press =
  a brighter bar (`RowPressed`), hover = a subtle raised background (`RowHoverBg`).
  Rendered by `rowFeedback`/`fillRow` (rows) and `button.place` (pills). The bar is
  the point: a selected row is unmistakable, not a one-cell ▸ marker. `protect`
  keeps a leading `attnGutter` amber `▍` outside the fill so attention survives
  selection.
- **LABEL** — inline text (detail tabs via `tabSeg`, action chips). Active, hover,
  and press are **font-colour shifts only — never a background**: active =
  `HeaderEm`, hover = `LabelHover` (Bright), press = `LabelPressed` (PrimaryBright).
  A tab's active state is already just a colour, so a bar or box behind the word
  would be far too loud. Tune the label language via the theme's `Label*` styles
  without touching the fill family.

A new clickable element joins one family or the other — do **not** give a tab a
fill or a row a bare colour change.

### Interaction semantics (don't reinvent these)

- **Hover** needs all-motion mouse (`View.MouseMode = AllMotion`); the highlight
  is the previous frame's resolved id applied on the next render (instant).
- **The frame is cached; motion is the firehose.** AllMotion delivers one event
  per cell the pointer crosses, and the runtime calls `View` after *every*
  message — so a naive `View` that rebuilds `content()` each time re-renders the
  whole UI hundreds of times a second just to move a highlight (a ~1379-alloc
  job; this was the "laggy TUI" bug). Instead `rootModel` caches the last frame
  and rebuilds only when `dirty`. The motion handler in `Update` short-circuits
  (no screen reacts to raw motion, so it never forwards) and sets `dirty` **only
  when the hovered id actually changes**; every other message sets `dirty` by
  default. Two consequences to respect: (1) any new high-frequency, no-visible-
  change message (e.g. wheel that nothing consumes) should follow motion's
  short-circuit pattern; (2) **never mutate model/screen state outside `Update`
  and expect `View` to reflect it** — without a dirtying message the cache is
  served. `perf_bench_test.go` guards this (`BenchmarkMotionSameCell` must stay
  ~0 allocs; `TestMotionWithoutHoverChangeReusesFrame`).
- **Press = hold, action on release**; releasing off the pressed element cancels
  (web-button semantics). The mouse needs no timer.
- The **press flash shows only on the already-focused element** — i.e.
  re-clicking what you're on. Clicking a *different* element changes focus, and
  that focus change is the feedback, so it deliberately doesn't animate
  (`button.place` enforces this priority: pressed-focused > active > hover).
- **Keyboard parity:** re-pressing the current page's nav key flashes its pill
  (timer-cleared, since a key has no "release"); toggle keys (`?`, `:`, `/`)
  never animate. See the `screenNavTarget` branch in `root.go`.
- A **modal overlay owns the screen** — `regionAt` returns nothing while help or
  the palette is open, so clicks/hover can't leak to the chrome behind them.

`go test ./internal/ui/tui/...` covers this in `hit/hit_test.go`,
`mouse_test.go` (nav + hint-bar click/hover/press, keyboard re-press, the
screen-body path), and the per-screen `*_mouse_test.go` files
(`dashboard_`/`people_`/`config_`/`recorder_mouse_test.go` — row select, tab
switch, chip replay, hover paint). `cellOf` there converts a substring's **byte**
offset to its **cell** column (frames are full of multi-byte glyphs), which is
the coordinate the mouse speaks.

> Hover needs a terminal that reports motion events; the VS Code integrated
> terminal does **not** (clicks work, hover doesn't) — test in a native
> terminal. The app paints its own canvas (`View.BackgroundColor`) so colours
> match across terminals.

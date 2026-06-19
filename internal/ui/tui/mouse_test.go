package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
)

// Shrink the keyboard press-flash timer so tests that drain it don't sleep.
func init() { pressFlash = time.Millisecond }

func pressKey(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// dispatch resolves a command (recursing into tea.Batch) and feeds each
// resulting message back through Update, as the runtime would.
func dispatch(t *testing.T, m *rootModel, cmd tea.Cmd) *rootModel {
	t.Helper()
	for _, msg := range drain(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(*rootModel)
	}
	return m
}

func drain(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range msg {
			out = append(out, drain(c)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

// clickAt performs a full press+release at (x,y). The action fires on release,
// so callers dispatch the returned (release) command.
func clickAt(t *testing.T, m *rootModel, x, y int) (*rootModel, tea.Cmd) {
	t.Helper()
	updated, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = updated.(*rootModel)
	updated, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return updated.(*rootModel), cmd
}

// pressDown arms a press at (x,y) without releasing — for asserting the
// held/pressed visual.
func pressDown(t *testing.T, m *rootModel, x, y int) *rootModel {
	t.Helper()
	updated, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	return updated.(*rootModel)
}

// navPillColumn renders a frame (populating the click map) and returns a
// screen-space column that falls inside the named pill, by locating the
// pill's title text on the header row. ansi.Strip keeps cell positions, so an
// index into the stripped first line is the on-screen X of that text.
func navPillColumn(t *testing.T, m *rootModel, title string) int {
	t.Helper()
	header := ansi.Strip(m.View().Content) // also rebuilds m.frameHits
	if i := strings.IndexByte(header, '\n'); i >= 0 {
		header = header[:i]
	}
	col := strings.Index(header, title)
	if col < 0 {
		t.Fatalf("pill %q not found in header row %q", title, header)
	}
	return col
}

func moveTo(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y} // no button held = hover
}

// Clicking a nav pill switches to that screen — the headline behaviour. The
// action fires on release and resolves through the frame's hit map to
// navClick, which emits the same switch command the number key does.
func TestNavPillClickSwitchesScreen(t *testing.T) {
	m := testRootModel() // dashboard is active
	col := navPillColumn(t, m, "people")

	m, cmd := clickAt(t, m, col, 0)
	if cmd == nil {
		t.Fatal("releasing on the people pill produced no command")
	}
	m = dispatch(t, m, cmd)

	if got := m.top().id(); got != sPeople {
		t.Errorf("after clicking people pill, top screen = %q; want %q", got, sPeople)
	}
}

// Every numbered pill is clickable and lands on its own screen — guards
// against an off-by-one in the column tracking that would send a click to a
// neighbouring pill.
func TestEveryNavPillClickLandsOnItsScreen(t *testing.T) {
	for _, sc := range topScreens {
		t.Run(string(sc.id), func(t *testing.T) {
			m := testRootModel()
			// Start somewhere else so the click is a real switch.
			if sc.id == sDashboard {
				m.stack = []screen{buildScreen(sPeople)}
			}
			col := navPillColumn(t, m, sc.title)

			m, cmd := clickAt(t, m, col, 0)
			if cmd == nil {
				t.Fatalf("clicking %q pill produced no command", sc.id)
			}
			m = dispatch(t, m, cmd)

			if got := m.top().id(); got != sc.id {
				t.Errorf("clicking %q pill switched to %q", sc.id, got)
			}
		})
	}
}

// Clicking the pill of the screen you're already on must not navigate or
// rebuild the screen (which would clobber in-progress state) — mirrors the
// number key. The press still shows, but releasing issues no screen switch.
func TestNavPillClickOnActiveScreenDoesNotNavigate(t *testing.T) {
	m := testRootModel() // dashboard active
	col := navPillColumn(t, m, "dashboard")

	m, cmd := clickAt(t, m, col, 0)
	m = dispatch(t, m, cmd)

	if got := m.top().id(); got != sDashboard {
		t.Errorf("active screen changed to %q on self-click", got)
	}
}

// Holding a click on the ALREADY-FOCUSED pill animates it in the pressed
// colour (so a repeat-click on the current screen registers), and releasing
// clears it. This is the only case that should animate.
func TestRepressFocusedPillAnimates(t *testing.T) {
	m := testRootModel() // dashboard is active/focused
	col := navPillColumn(t, m, "dashboard")

	m = pressDown(t, m, col, 0)
	if m.pressed != navHitID(sDashboard) {
		t.Fatalf("pressed = %q while held; want %q", m.pressed, navHitID(sDashboard))
	}
	if !strings.Contains(m.renderHeader(), m.styles.RowPressed.Render(" 1 dashboard ")) {
		t.Errorf("re-pressing the focused pill did not animate it in the pressed colour")
	}

	updated, _ := m.Update(tea.MouseReleaseMsg{X: col, Y: 0, Button: tea.MouseLeft})
	m = updated.(*rootModel)
	if m.pressed != "" {
		t.Errorf("pressed = %q after release; want cleared", m.pressed)
	}
}

// Holding a click on a DIFFERENT (unfocused) pill must NOT animate it — the
// focus change on release is the feedback, so there's no pressed colour.
func TestPressUnfocusedPillDoesNotAnimate(t *testing.T) {
	m := testRootModel() // dashboard active; people is NOT focused
	col := navPillColumn(t, m, "people")

	m = pressDown(t, m, col, 0)
	if strings.Contains(m.renderHeader(), m.styles.RowPressed.Render(" 2 people ")) {
		t.Errorf("pressing an unfocused pill animated it; only the focused pill should")
	}
}

// Re-pressing the hotkey for the page you're already on flashes its nav pill
// (the keyboard twin of re-clicking the focused pill) without navigating. The
// flash clears on its timer.
func TestRepressCurrentPageHotkeyAnimates(t *testing.T) {
	m := testRootModel() // dashboard active, nothing pushed

	updated, cmd := m.Update(pressKey("1")) // dashboard's nav key
	m = updated.(*rootModel)

	if m.pressed != navHitID(sDashboard) {
		t.Fatalf("pressed = %q after re-pressing current page key; want %q", m.pressed, navHitID(sDashboard))
	}
	if !strings.Contains(m.renderHeader(), m.styles.RowPressed.Render(" 1 dashboard ")) {
		t.Errorf("re-pressing the focused page hotkey did not animate its pill")
	}

	m = dispatch(t, m, cmd) // drains the flash timer (+ any screen cmd)
	if got := m.top().id(); got != sDashboard {
		t.Errorf("re-pressing current page key navigated to %q; want no-op", got)
	}
	if m.pressed != "" {
		t.Errorf("pressed = %q after flash timer; want cleared", m.pressed)
	}
}

// Pressing the hotkey for a DIFFERENT page navigates and must NOT animate —
// the focus change is the feedback.
func TestPressOtherPageHotkeyDoesNotAnimate(t *testing.T) {
	m := testRootModel() // dashboard active

	updated, cmd := m.Update(pressKey("2")) // people
	m = updated.(*rootModel)
	if m.pressed != "" {
		t.Errorf("pressed = %q when switching pages; want no animation", m.pressed)
	}
	m = dispatch(t, m, cmd)
	if got := m.top().id(); got != sPeople {
		t.Errorf("pressing people hotkey did not navigate; top = %q", got)
	}
}

// Toggle keys (help opens/closes) must never animate a pill — re-pressing them
// isn't a "you're already here" nav, it's a different action each time.
func TestHelpToggleHotkeyDoesNotAnimate(t *testing.T) {
	m := testRootModel()

	updated, _ := m.Update(pressKey("?"))
	m = updated.(*rootModel)
	if m.pressed != "" {
		t.Errorf("help toggle set pressed = %q; toggles must not animate", m.pressed)
	}
	if !m.helpOpen {
		t.Errorf("help did not open on '?'")
	}
}

// Pressing an element then releasing OFF it cancels the action (drag-off),
// just like a web button — and the press is cleared.
func TestPressThenReleaseOffTargetCancels(t *testing.T) {
	m := testRootModel() // dashboard active
	col := navPillColumn(t, m, "people")

	m = pressDown(t, m, col, 0)
	updated, cmd := m.Update(tea.MouseReleaseMsg{X: m.width - 1, Y: 0, Button: tea.MouseLeft})
	m = updated.(*rootModel)
	m = dispatch(t, m, cmd)

	if m.pressed != "" {
		t.Errorf("pressed = %q after off-target release; want cleared", m.pressed)
	}
	if got := m.top().id(); got != sDashboard {
		t.Errorf("off-target release still navigated to %q; want cancel", got)
	}
}

// A click in empty header space (past the pills) resolves to nothing — it must
// not switch screens.
func TestClickOutsideAnyRegionDoesNothing(t *testing.T) {
	m := testRootModel()
	_ = m.View() // populate frameHits

	m, _ = clickAt(t, m, m.width-1, 0)
	if got := m.top().id(); got != sDashboard {
		t.Errorf("click in empty space changed screen to %q", got)
	}
}

// Moving the pointer over a pill records it as hovered and draws it in the
// hover style (the subtle raised fill) — the headline hover behaviour. The
// hover style is width-identical to normal/active so the strip never reflows.
func TestNavPillHoverHighlights(t *testing.T) {
	m := testRootModel() // dashboard active
	col := navPillColumn(t, m, "people")

	updated, _ := m.Update(moveTo(col, 0))
	m = updated.(*rootModel)

	if m.hovered != navHitID(sPeople) {
		t.Fatalf("hovered = %q; want %q", m.hovered, navHitID(sPeople))
	}
	wantPill := m.styles.RowHover.Render(" 2 people ")
	if !strings.Contains(m.renderHeader(), wantPill) {
		t.Errorf("people pill not drawn in hover style while hovered")
	}
	// Hover must not navigate.
	if got := m.top().id(); got != sDashboard {
		t.Errorf("hover changed screen to %q", got)
	}

	// Re-entering the same cell is idempotent (keeps state stable so a still
	// pointer is free).
	updated, _ = m.Update(moveTo(col, 0))
	if updated.(*rootModel).hovered != navHitID(sPeople) {
		t.Errorf("re-hovering same cell changed hovered state")
	}
}

// Moving the pointer off every pill clears the hover so no stale pill stays
// lit.
func TestHoverClearsWhenPointerLeaves(t *testing.T) {
	m := testRootModel()
	col := navPillColumn(t, m, "people")

	updated, _ := m.Update(moveTo(col, 0))
	m = updated.(*rootModel)
	if m.hovered == "" {
		t.Fatal("precondition: pill should be hovered")
	}

	updated, _ = m.Update(moveTo(m.width-1, 0)) // empty space past the pills
	m = updated.(*rootModel)
	if m.hovered != "" {
		t.Errorf("hovered = %q after leaving pills; want cleared", m.hovered)
	}
}

// Selection wins over hover: hovering the active pill keeps the accent
// (selected) look, never the hover look, so "where am I" stays unambiguous.
func TestActivePillHoverKeepsSelectedLook(t *testing.T) {
	m := testRootModel() // dashboard active
	col := navPillColumn(t, m, "dashboard")

	updated, _ := m.Update(moveTo(col, 0))
	m = updated.(*rootModel)

	header := m.renderHeader()
	if !strings.Contains(header, m.styles.RowSelected.Render(" 1 dashboard ")) {
		t.Errorf("active pill lost its selected look while hovered")
	}
	if strings.Contains(header, m.styles.RowHover.Render(" 1 dashboard ")) {
		t.Errorf("active pill drawn in hover style; selection must win over hover")
	}
}

// Hover is purely visual — while a modal overlay owns the screen, the chrome
// behind it must not light up under the pointer.
func TestHoverIgnoredWhileModalOpen(t *testing.T) {
	m := testRootModel()
	col := navPillColumn(t, m, "people")
	m.helpOpen = true

	updated, _ := m.Update(moveTo(col, 0))
	m = updated.(*rootModel)
	if m.hovered != "" {
		t.Errorf("hovered = %q while modal open; want nothing hovered", m.hovered)
	}
}

// The screen-facing interaction path: a screen registers a clickable body
// region via screenCtx.hitRow (body-local coords), and root resolves a click
// at the translated absolute cell and runs the region's action. Guards the
// bodyTop translation that makes screen-body elements clickable.
func TestScreenCtxHitRowMakesBodyRegionClickable(t *testing.T) {
	m := testRootModel()
	m.frameHits = &hit.Map[region]{} // as content() allocates each frame

	ctx := m.screenCtx()
	ctx.bodyTop = 3 // pretend the body starts at row 3 (under the header)

	fired := false
	row := ctx.hitRow(2, 1) // body-local (2,1) -> absolute (2, 4)
	button{
		id:      "test:thing",
		onClick: func() tea.Cmd { fired = true; return nil },
		render:  func(uiState) string { return "[x]" }, // width 3: cols 2..4
	}.place(row, ctx.pointer())
	_ = row.String()

	// Press+release at the translated absolute cell.
	updated, _ := m.Update(tea.MouseClickMsg{X: 3, Y: 4, Button: tea.MouseLeft})
	m = updated.(*rootModel)
	m.Update(tea.MouseReleaseMsg{X: 3, Y: 4, Button: tea.MouseLeft})

	if !fired {
		t.Errorf("body region action did not fire on a click at its translated coords")
	}
}

// The universal hint bar is clickable on every screen: clicking the "menu"
// chip replays its key (:) and opens the palette — the chip and the keystroke
// are one action. Guards the hint row's absolute-y registration + key replay.
func TestHintBarClickOpensPalette(t *testing.T) {
	m := testRootModel()
	x, y := cellOf(t, ansi.Strip(m.View().Content), "menu")

	m, cmd := clickAt(t, m, x, y)
	m = dispatch(t, m, cmd)

	if m.palette == nil {
		t.Errorf("clicking the menu hint chip did not open the palette")
	}
}

// Clicking the "help" hint chip replays ? and opens the help overlay.
func TestHintBarClickOpensHelp(t *testing.T) {
	m := testRootModel()
	x, y := cellOf(t, ansi.Strip(m.View().Content), "help")

	m, cmd := clickAt(t, m, x, y)
	m = dispatch(t, m, cmd)

	if !m.helpOpen {
		t.Errorf("clicking the help hint chip did not open help")
	}
}

// Hovering a hint chip lights it up (so the bar reads as interactive), without
// firing its action.
func TestHintBarChipHovers(t *testing.T) {
	m := testRootModel()
	x, y := cellOf(t, ansi.Strip(m.View().Content), "menu")

	updated, _ := m.Update(moveTo(x, y))
	m = updated.(*rootModel)

	if m.hovered != "hint:0" {
		t.Errorf("hovered = %q over the menu chip; want hint:0", m.hovered)
	}
	if m.palette != nil {
		t.Errorf("hovering opened the palette; hover must be visual only")
	}
}

// While a modal overlay is open it owns the screen; chrome clicks underneath
// must be ignored so a stray click can't navigate behind the palette/help.
func TestNavPillClickIgnoredWhileModalOpen(t *testing.T) {
	m := testRootModel()
	col := navPillColumn(t, m, "people")
	m.helpOpen = true

	m, cmd := clickAt(t, m, col, 0)
	if cmd != nil {
		t.Errorf("nav click produced a command while help overlay open")
	}
	if m.pressed != "" {
		t.Errorf("pressed = %q while modal open; want nothing pressed", m.pressed)
	}
	if got := m.top().id(); got != sDashboard {
		t.Errorf("nav click navigated to %q while modal open", got)
	}
}

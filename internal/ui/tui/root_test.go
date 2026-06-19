package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func testRootModel() *rootModel {
	return &rootModel{
		keys:         keys.New(),
		styles:       theme.NewStyles(),
		width:        150, // >= minTwoPaneW, with headroom above the content floor for the drag tests
		height:       30,
		sidebarWidth: defaultSidebarW,
		stack:        []screen{newDashboardScreen()},
	}
}

// View must declare alt-screen + all-motion mouse on EVERY frame, including
// the too-small-terminal fallback. In v2 these are per-View flags the renderer
// diffs each frame; a frame that left them unset (the old fallback did) would
// make Bubble Tea exit the alternate screen and disable the mouse mid-session
// — e.g. the instant the user shrinks the window below the minimum size, the
// fallback message would spill into their scrollback. All-motion (not just
// cell-motion) is required so hover events arrive with no button held.
// Regression guard.
func TestViewKeepsTerminalModesAtEverySize(t *testing.T) {
	sizes := []struct {
		name string
		w, h int
	}{
		{"normal", 100, 30},
		{"too small", 20, 5},
	}
	for _, sz := range sizes {
		t.Run(sz.name, func(t *testing.T) {
			m := testRootModel()
			m.width, m.height = sz.w, sz.h
			v := m.View()
			if !v.AltScreen {
				t.Errorf("AltScreen = false at %dx%d; want alt-screen on every frame", sz.w, sz.h)
			}
			if v.MouseMode != tea.MouseModeAllMotion {
				t.Errorf("MouseMode = %v at %dx%d; want MouseModeAllMotion on every frame", v.MouseMode, sz.w, sz.h)
			}
		})
	}
}

func TestHelpOverlayEscapeCloses(t *testing.T) {
	m := testRootModel()
	m.helpOpen = true

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(*rootModel)

	if got.helpOpen {
		t.Fatal("helpOpen = true; want Escape to close help overlay")
	}
}

func TestPaletteEscapeCloses(t *testing.T) {
	m := testRootModel()
	m.palette = newPalette(nil, []paletteEntry{{Label: "Dashboard", Action: "goto", Param: string(sDashboard)}})

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	got := updated.(*rootModel)

	if got.palette != nil {
		t.Fatal("palette is still open; want Escape to close command menu")
	}
}

// Top-level screens must be numbered 1..N with no gap. The gap (1/3/4)
// appeared after search folded into the dashboard and is exactly the bug
// the auto-numbering registry guards against: every screen responds to
// its 1-based position in topScreens, and the router maps that key back
// to the same screen.
func TestScreenNavNumberingIsContiguous(t *testing.T) {
	press := func(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }
	for i, sc := range topScreens {
		num := strconv.Itoa(i + 1)
		b := sc.navBinding(i)
		if !key.Matches(press(num), b) {
			t.Errorf("screen %q does not respond to its number %q", sc.id, num)
		}
		if !strings.HasPrefix(b.Help().Key, num) {
			t.Errorf("screen %q help key %q does not start with its number %q", sc.id, b.Help().Key, num)
		}
		if got := screenNavTarget(press(num)); got != sc.id {
			t.Errorf("number %q routed to %q; want %q", num, got, sc.id)
		}
	}
}

// The help overlay must be rendered FROM the bindings, so changing a key
// in keys.New() updates the displayed label automatically. We assert
// every action key's literal appears in the rendered overlay and that no
// stale hand-written numbering survives.
func TestHelpOverlayDerivesKeysFromBindings(t *testing.T) {
	m := testRootModel()
	m.helpOpen = true
	help := ansi.Strip(m.View().Content)

	bindings := append(screenNavBindings(),
		m.keys.Record, m.keys.Stop, m.keys.Marker, m.keys.EditTitle,
		m.keys.OpenAgent, m.keys.Delete, m.keys.ClearSearch,
		m.keys.Transcript, m.keys.Speakers, m.keys.NextMatch, m.keys.PrevMatch,
		m.keys.Test, m.keys.Remove,
	)
	for _, b := range bindings {
		if !strings.Contains(help, b.Help().Key) {
			t.Errorf("help overlay missing key %q (%q) — UI not derived from bindings", b.Help().Key, b.Help().Desc)
		}
	}
	// Stale-numbering guard: People sits at slot 2, so recorder is 3/r and
	// config is 4. If People were dropped, recorder would regress to 2/r and
	// config to 3/, — assert those stale labels never appear.
	if strings.Contains(help, "2/r") || strings.Contains(help, "3/,") {
		t.Errorf("help overlay shows stale numbering:\n%s", help)
	}
}

// TestSidebarDividerDrag exercises the root-owned drag: press on the seam arms
// it, motion tracks the pointer column (clamped to the floor), and release
// clears the press and returns a persist command.
func TestSidebarDividerDrag(t *testing.T) {
	m := testRootModel()
	m.sidebarWidth = defaultSidebarW
	m.top().(*dashboardScreen).loading = false // so the two-pane layout (and divider) renders
	// Render a frame so the divider's hit region is registered in frameHits.
	_ = m.View()
	leftW, _ := layout.SidebarSplit(m.width, m.sidebarWidth, minSidebarW, minContentW)

	// Press on the seam column.
	updated, _ := m.Update(tea.MouseClickMsg{X: leftW, Y: 5, Button: tea.MouseLeft})
	m = updated.(*rootModel)
	if m.pressed != sidebarDividerID {
		t.Fatalf("press on divider didn't arm it: pressed=%q", m.pressed)
	}

	// Drag wider — width follows the pointer (width 130 leaves headroom above the floor).
	updated, _ = m.Update(tea.MouseMotionMsg{X: 60, Y: 5})
	m = updated.(*rootModel)
	if m.sidebarWidth != 60 {
		t.Errorf("drag to x=60: sidebarWidth=%d; want 60", m.sidebarWidth)
	}

	// Drag past the floor — clamped, not crossed.
	updated, _ = m.Update(tea.MouseMotionMsg{X: 2, Y: 5})
	m = updated.(*rootModel)
	if m.sidebarWidth != minSidebarW {
		t.Errorf("drag below floor: sidebarWidth=%d; want %d", m.sidebarWidth, minSidebarW)
	}

	// Release: clears the press, returns the persist command.
	updated, cmd := m.Update(tea.MouseReleaseMsg{X: 2, Y: 5, Button: tea.MouseLeft})
	m = updated.(*rootModel)
	if m.pressed != "" {
		t.Errorf("release didn't clear pressed: %q", m.pressed)
	}
	if cmd == nil {
		t.Error("release on divider should return a persist command")
	}
}

// TestSidebarResizeKeys checks the keyboard-parity bindings adjust the shared
// width within bounds and persist, and that they fire even when a text input is
// focused (they're non-letter ctrl keys).
func TestSidebarResizeKeys(t *testing.T) {
	ctrl := func(code rune) tea.KeyPressMsg {
		return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}
	}
	// Start with headroom on both sides of the floor (testRootModel is 150 wide,
	// so the sidebar range is [minSidebarW, 150-1-minContentW]).
	const start = minSidebarW + 8
	cases := []struct {
		name string
		key  tea.KeyPressMsg
		want int
	}{
		{"wider", ctrl(tea.KeyRight), start + sidebarStep},
		{"narrower", ctrl(tea.KeyLeft), start - sidebarStep},
		{"reset", ctrl('0'), defaultSidebarW},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := testRootModel()
			m.sidebarWidth = start
			updated, cmd := m.Update(c.key)
			m = updated.(*rootModel)
			if m.sidebarWidth != c.want {
				t.Errorf("after %s: sidebarWidth=%d; want %d", c.name, m.sidebarWidth, c.want)
			}
			if cmd == nil {
				t.Errorf("%s should return a persist command", c.name)
			}
		})
	}

	// Fires with a focused text input: focus the dashboard's search, then widen.
	m := testRootModel()
	m.sidebarWidth = defaultSidebarW
	m.stack = []screen{newDashboardScreen()}
	d := m.top().(*dashboardScreen)
	d.inputFocus = true
	d.input.Focus()
	if !m.top().inputActive() {
		t.Fatal("precondition: dashboard input should be active")
	}
	updated, cmd := m.Update(ctrl(tea.KeyRight))
	m = updated.(*rootModel)
	if m.sidebarWidth != defaultSidebarW+sidebarStep {
		t.Errorf("resize key with input focused: sidebarWidth=%d; want %d", m.sidebarWidth, defaultSidebarW+sidebarStep)
	}
	if cmd == nil {
		t.Error("resize key with input focused should still persist")
	}
}

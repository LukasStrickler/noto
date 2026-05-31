package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func testRootModel() *rootModel {
	return &rootModel{
		keys:   keys.New(),
		styles: theme.NewStyles(),
		width:  100,
		height: 30,
		stack:  []screen{newDashboardScreen()},
	}
}

// View must declare alt-screen + cell-motion mouse on EVERY frame, including
// the too-small-terminal fallback. In v2 these are per-View flags the renderer
// diffs each frame; a frame that left them unset (the old fallback did) would
// make Bubble Tea exit the alternate screen and disable the mouse mid-session
// — e.g. the instant the user shrinks the window below the minimum size, the
// fallback message would spill into their scrollback. Regression guard.
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
			if v.MouseMode != tea.MouseModeCellMotion {
				t.Errorf("MouseMode = %v at %dx%d; want MouseModeCellMotion on every frame", v.MouseMode, sz.w, sz.h)
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
	if strings.Contains(help, "3/r") || strings.Contains(help, "4/,") || strings.Contains(help, "4 ") {
		t.Errorf("help overlay shows stale numbering:\n%s", help)
	}
}

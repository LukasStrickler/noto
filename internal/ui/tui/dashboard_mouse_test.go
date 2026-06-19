package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// dashboardRoot builds a root model whose dashboard has n meetings loaded and
// the detail pane bound to the first one (so the tab bar renders). Counts are
// left at zero so category words like "actions" appear ONLY in the tab bar,
// never in a list row's counter strip — keeps the coordinate lookups
// unambiguous.
func dashboardRoot(t *testing.T, n int) (*rootModel, *dashboardScreen) {
	t.Helper()
	d := newDashboardScreen().(*dashboardScreen)
	d.loading = false
	for i := 0; i < n; i++ {
		d.all = append(d.all, notoapi.Meeting{
			ID:        fmt.Sprintf("m%d", i),
			Title:     fmt.Sprintf("Meeting%d", i),
			CreatedAt: time.Now(),
		})
	}
	d.pane.id_ = "m0" // bind the pane so its tab bar renders

	r := testRootModel()
	r.stack = []screen{d}
	// Wide enough that the detail pane fits the FULL tab bar ("summary │ actions
	// │ … │ speakers"), so the tab-click tests can target whole labels.
	r.width, r.height = 150, 30
	return r, d
}

// cellOf returns the (x,y) cell coordinate of the first occurrence of sub in
// the rendered frame. strings.Index gives a BYTE offset, but the mouse speaks
// in cells — and the frame is full of multi-byte glyphs (box borders │, arrows
// ↑↓, ⏎), so the prefix's display width is the real column.
func cellOf(t *testing.T, frame, sub string) (int, int) {
	t.Helper()
	for y, line := range strings.Split(frame, "\n") {
		if b := strings.Index(line, sub); b >= 0 {
			return ansi.StringWidth(line[:b]), y
		}
	}
	t.Fatalf("substring %q not found in frame:\n%s", sub, frame)
	return 0, 0
}

// clickRoot renders a frame (populating the hit map), finds the cell holding
// sub, and performs a full press+release there.
func clickRoot(t *testing.T, r *rootModel, sub string) {
	t.Helper()
	frame := ansi.Strip(r.View().Content)
	x, y := cellOf(t, frame, sub)
	r.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	r.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// Clicking a meeting row selects + loads that meeting and keeps list focus —
// the pointer twin of arrow-key navigation. Guards the per-row coordinate math
// (panel border + title/rule offset + 2-line rows).
func TestDashboardClickMeetingRowSelects(t *testing.T) {
	r, d := dashboardRoot(t, 3)

	clickRoot(t, r, "Meeting2")

	if d.cursor != 2 {
		t.Errorf("cursor = %d after clicking Meeting2; want 2", d.cursor)
	}
	if d.paneOpen {
		t.Errorf("clicking a row focused the pane; want list focus")
	}
	if d.pane.id_ != "m2" {
		t.Errorf("pane bound to %q; want the clicked meeting m2", d.pane.id_)
	}
}

// Clicking a detail tab focuses the pane AND switches to that tab — the
// prioritised behaviour: a click on "actions" navigates, not just changes
// focus. Guards the tab-bar coordinate math through the pane's panel.
func TestDashboardClickTabFocusesAndSwitches(t *testing.T) {
	r, d := dashboardRoot(t, 2)
	if d.paneOpen {
		t.Fatal("precondition: pane should start unfocused")
	}

	clickRoot(t, r, "actions")

	if !d.paneOpen {
		t.Errorf("clicking a tab did not focus the pane")
	}
	if d.pane.tab != tabActions {
		t.Errorf("pane.tab = %v after clicking actions; want tabActions", d.pane.tab)
	}
}

// Clicking the detail pane (off the tabs) just focuses it — the coarse pane
// region, which finer tab/row regions are layered over.
func TestDashboardClickPaneFocuses(t *testing.T) {
	r, d := dashboardRoot(t, 2)

	clickRoot(t, r, "Details") // the right panel's title, inside the pane region

	if !d.paneOpen {
		t.Errorf("clicking the pane did not focus it")
	}
}

// Clicking the filter row focuses the search input (mirrors `/`).
func TestDashboardClickFilterFocusesInput(t *testing.T) {
	r, d := dashboardRoot(t, 2)

	clickRoot(t, r, "search") // the filter row placeholder "search…"

	if !d.inputFocus {
		t.Errorf("clicking the filter row did not focus the search input")
	}
	if d.paneOpen {
		t.Errorf("focusing the filter should drop pane focus")
	}
}

// overlayBG is the SGR the hover fill paints (theme Overlay #17243d).
const overlayBG = "48;2;23;36;61"

// rawLineWith returns the first raw (un-stripped) frame line whose visible text
// contains sub — so a test can assert what styling that line actually carries.
func rawLineWith(t *testing.T, raw, sub string) string {
	t.Helper()
	for _, line := range strings.Split(raw, "\n") {
		if strings.Contains(ansi.Strip(line), sub) {
			return line
		}
	}
	t.Fatalf("no line containing %q in frame", sub)
	return ""
}

// Moving the pointer over a meeting row paints the hover fill across it — the
// thing that was missing (regions were click-only). Guards that body rows wire
// pointer.state → rowFeedback, not just hit registration.
func TestDashboardHoverRowPaints(t *testing.T) {
	r, _ := dashboardRoot(t, 3)

	// Baseline: an un-hovered, un-selected row carries no hover fill.
	before := ansi.Strip(r.View().Content)
	x, y := cellOf(t, before, "Meeting1")
	if strings.Contains(rawLineWith(t, r.View().Content, "Meeting1"), overlayBG) {
		t.Fatal("precondition: Meeting1 row already painted before hover")
	}

	r.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})

	if got := rawLineWith(t, r.View().Content, "Meeting1"); !strings.Contains(got, overlayBG) {
		t.Errorf("hovering Meeting1 did not paint the row; line=%q", got)
	}
}

// brightFG is the SGR LabelHover recolours a tab to (theme Bright #f8fafc).
const brightFG = "38;2;248;250;252"

// Moving the pointer over a detail tab lights it up via the LABEL family —
// FONT COLOUR ONLY, no background fill (a tab's active state is a colour, so a
// box behind the word would be too loud). Hover brightens the text; it must NOT
// paint the Overlay background that full-width rows use.
func TestDashboardHoverTabPaints(t *testing.T) {
	r, _ := dashboardRoot(t, 2)

	before := r.View().Content
	x, y := cellOf(t, ansi.Strip(before), "decisions")
	if strings.Contains(rawLineWith(t, before, "decisions"), brightFG) {
		t.Fatal("precondition: tab already brightened before hover")
	}

	r.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})

	got := rawLineWith(t, r.View().Content, "decisions")
	if !strings.Contains(got, brightFG) {
		t.Errorf("hovering the decisions tab did not brighten its text; line=%q", got)
	}
	if strings.Contains(got, overlayBG) {
		t.Errorf("tab hover must be font-colour only, but painted a background fill; line=%q", got)
	}
}

// After focusing the pane, clicking back on the list returns focus to it.
func TestDashboardClickListReturnsFocus(t *testing.T) {
	r, d := dashboardRoot(t, 3)
	d.paneOpen = true // start focused on the pane

	clickRoot(t, r, "Meeting1") // a row lives in the list region

	if d.paneOpen {
		t.Errorf("clicking a list row left focus on the pane")
	}
	if d.cursor != 1 {
		t.Errorf("cursor = %d; want 1", d.cursor)
	}
}

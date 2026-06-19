package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
)

// The resizable sidebar/content divider is shared by all three two-pane screens
// (dashboard, people, config): one seam, one persisted width, one hit id. The
// divider is root-owned because dragging needs motion events, which never reach
// screens (see root.Update's motion short-circuit) — the screens only draw the
// seam and register its region; root does the drag math against m.sidebarWidth.
const (
	sidebarDividerID = "sidebar:divider"

	// panelChromeCells is what a layout.Panel spends on its frame: 2 border + 2
	// padding (the same `Width - 4` the Panel uses for its inner area). Used to
	// turn a content's inner-width requirement into the panel's outer width.
	panelChromeCells = 4

	// minSidebarW is the meeting-list floor — wide enough that titles, dates and
	// the counter strip read comfortably without compacting to their tightest
	// form. The detail pane carries its own (larger, derived) floor (minContentW),
	// so the two-pane minimum is simply the sum: we'd rather demand a wider
	// terminal (minTwoPaneW → the too-small guard) than starve either pane.
	minSidebarW = 52

	// Per-keystroke resize step for the keyboard-parity bindings.
	sidebarStep = 2

	// defaultSidebarW is the starting width (also the reset target). It sits at the
	// sidebar floor so the detail pane gets the surplus; the user widens from there.
	defaultSidebarW = minSidebarW
)

// minContentW is DERIVED from the detail pane's widest FIXED row — the full tab
// bar (every label spelled out; tabs are never abbreviated) plus its worst-case
// attention badge and the panel's border+padding. Everything else in the pane
// adapts to width (the timeline spans it; people/summary/items wrap;
// transcript/speakers clip), so the tab bar alone decides how narrow the content
// pane may get: SidebarSplit never sizes it below this, and when the terminal
// can't grant minSidebarW + a divider + minContentW the root shows the too-small
// guard. Single-sourced from detailTabLabels so a tab rename/addition keeps the
// floor honest (TestMinContentWidthFitsFullTabBar locks it in).
var minContentW = detailTabBarFullCells() + panelChromeCells

// minTwoPaneW is the smallest terminal width that fits both panes plus the
// 1-cell divider gap. Below it the two floors can't both be honored, so the
// root shows the too-small guard rather than dropping a pane (see minTermW).
var minTwoPaneW = minSidebarW + 1 + minContentW

// sidebarPref resolves the sidebar width a two-pane screen should lay out with:
// the shared persisted value, or defaultSidebarW when it's still unset (0 before
// the config load returns). layout.SidebarSplit then clamps it to the terminal.
func sidebarPref(ctx screenCtx) int {
	if ctx.sidebarWidth <= 0 {
		return defaultSidebarW
	}
	return ctx.sidebarWidth
}

// joinSidebar joins the pre-rendered left (sidebar) and right (content) panels
// of a two-pane screen, drawing the resize divider in the 1-cell gap between
// them and registering it as a hit region so root can drag it. The seam is
// blank at rest, shows a neutral rule on hover, and glows while dragging
// (pressed). leftW is the sidebar width; height is the panel height. It replaces
// layout.HStack(left, right) at each two-pane site so the seam looks and behaves
// identically everywhere.
func joinSidebar(ctx screenCtx, left, right string, leftW, height int) string {
	// The seam occupies the gap column at x=leftW, spanning the panel height.
	ctx.hits.Add(hit.Rect{X: leftW, Y: ctx.bodyTop, W: 1, H: height},
		region{id: sidebarDividerID})

	s := ctx.styles
	glyph := " "
	switch {
	case ctx.pressed == sidebarDividerID:
		glyph = s.DividerActive.Render("│")
	case ctx.hovered == sidebarDividerID:
		glyph = s.DividerHover.Render("│")
	}

	lines := make([]string, height)
	for i := range lines {
		lines[i] = glyph
	}
	col := strings.Join(lines, "\n")
	return lipgloss.JoinHorizontal(lipgloss.Top, left, col, right)
}

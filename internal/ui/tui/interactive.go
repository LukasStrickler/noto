package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// interactive.go is the small component layer that sits on top of the hit
// package. It gives every clickable element one shared vocabulary —
// normal / hover / clicked — and wires pointer tracking and click routing
// automatically, so a view declares how an element looks in each state in
// exactly one place and never touches coordinates or event plumbing.

// uiState is the visual state of an interactive element for one frame.
type uiState int

const (
	uiNormal  uiState = iota // idle
	uiHover                  // the pointer is over it
	uiActive                 // selected — the persistent "clicked into" state
	uiPressed                // momentary click flash (:active), highest priority
)

// pointer is the live interaction state place() resolves a button against:
// which element the cursor is over (hovered) and which is mid click-flash
// (pressed). Both are region ids; "" means none.
type pointer struct {
	hovered string
	pressed string
}

// state resolves the visual state of an element from the live pointer. active
// reports whether this element is the selected/focused one. It is the SINGLE
// rule every clickable thing shares — a nav pill (via button.place), a detail
// tab, a list row — so they all light up by the same logic instead of each
// reinventing "am I hovered / pressed / selected?".
//
// Priority — re-press-focused > active > hover > normal: the pressed flash is
// shown only when the held element is also the focused one (re-clicking what
// you're already on); clicking a *different* element changes focus, and that
// focus change is its own feedback, so it doesn't also animate.
func (p pointer) state(id string, active bool) uiState {
	switch {
	case active && id != "" && id == p.pressed:
		return uiPressed
	case active:
		return uiActive
	case id != "" && id == p.hovered:
		return uiHover
	}
	return uiNormal
}

// region is the payload stored per clickable rect: a stable id (so the
// element under the cursor can be tracked and compared frame to frame) and
// the action a click runs.
type region struct {
	id      string
	onClick clickAction
}

// button is a reusable clickable element. A view declares how it looks in
// each state (render) and what a click does (onClick); place() resolves the
// live state from the current hover/press, draws it, and registers the hit
// region. Defining a clickable thing is therefore one literal:
//
//	button{
//	    id:      "nav:" + id,        // unique across the whole frame
//	    active:  id == current,      // is this the selected/focused one?
//	    onClick: m.navClick(id),     // a clickAction (func() tea.Cmd)
//	    render:  func(st uiState) string { return pill(st) },
//	}.place(row, m.pointer())        // chrome; screens use ctx.pointer()
//
// The row comes from a hit.Row builder — m.frameHits-based in chrome, or
// ctx.hitRow(x, y) in a screen body. render must produce the SAME width in
// every state (hover/press/active recolour, never resize) so a row never
// reflows as the pointer moves.
type button struct {
	id      string               // stable hover/click identity; "" = inert
	active  bool                 // currently selected → renders uiActive
	onClick clickAction          // nil = no-op on click
	render  func(uiState) string // look per state
}

// place draws b in its pointer-resolved state (see pointer.state) and
// registers it as a hit region in row.
func (b button) place(row *hit.Row[region], p pointer) {
	row.Hit(b.render(p.state(b.id, b.active)), region{id: b.id, onClick: b.onClick})
}

// hitsIf returns the frame's hit map when an element should be clickable, else
// nil — so a render pass that must register nothing (a sub-mode owns input)
// builds its hit.Row with a nil map and the same code path serves both.
func hitsIf(ctx screenCtx, clickable bool) *hit.Map[region] {
	if clickable {
		return ctx.hits
	}
	return nil
}

// paintRow lays a CONTINUOUS background under an already-styled, multi-segment
// line and pads it to width. lipgloss can't do this directly: every segment
// ends in a reset (\x1b[m) that also clears the background, so a naive outer
// wrap only tints up to the first reset. paintRow re-arms the background after
// each reset (and under the trailing pad), so the whole row carries one fill
// while each segment keeps its own foreground.
//
// It is the body-row counterpart to the button component's recolour: a list
// row is too rich (title + date + badges + gutter) to re-style segment by
// segment, so we tint the composed line instead.
func paintRow(line string, bg lipgloss.Style, width int) string {
	open := bg.Render("\x00")
	open, _, _ = strings.Cut(open, "\x00") // the leading "set background" SGR
	if open == "" {
		return line
	}
	if w := ansi.StringWidth(line); w < width {
		line += strings.Repeat(" ", width-w)
	}
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+open)
	return open + line + "\x1b[m"
}

// rowFeedback is the FILL feedback family for full-width body rows (the
// dashboard meeting list, the people directory, the config sections). It's the
// single place a rich list row expresses every pointer state, so rows light up
// the same way everywhere and read like the nav pills at the top:
//
//   - active  — the SELECTED row: a solid accent bar across its whole width
//     (the calm Primary fill, same as a lit nav pill), so the active meeting is
//     unmistakable instead of relying on a one-cell ▸ marker. The line is
//     flattened (ansi.Strip) onto the fill, exactly like the press flash, so
//     the bar is uniform rather than a patchwork of segment colours;
//   - press   — the bright re-click flash (PrimaryBright), flattened the same
//     way; shown only while holding a click on the row you're already on, one
//     rung above the selected fill so the repeat-click visibly registers;
//   - hover   — a subtle raised fill laid UNDER the row, each segment keeping
//     its own colour (paintRow), so the pointer's target stands out without
//     reading as selected;
//   - normal  — untouched.
//
// protect is a count of leading cells the fill must leave alone — the amber
// attention bar (attnGutter's ▍) lives in column 0, and flattening it into the
// selection would bury the "this still needs you" flag exactly when you move
// onto the row. Those rows pass protect=1 so the bar keeps its colour beside the
// fill; rows with no gutter pass 0 so the bar reaches the left edge.
//
// (Inline text buttons — detail tabs, chips — deliberately do NOT use this; a
// fill is too loud for a glyph whose active state is just a colour. They use
// the LABEL family instead: see tabSeg and the theme's Label* styles.)
func rowFeedback(line string, st uiState, s theme.Styles, width, protect int) string {
	switch st {
	case uiActive:
		return fillRow(line, s.RowSelected, width, protect)
	case uiPressed:
		return fillRow(line, s.RowPressed, width, protect)
	case uiHover:
		return paintRow(line, s.RowHoverBg, width)
	default:
		return line
	}
}

// fillRow flattens a row onto a solid accent bar (uniform, like a lit nav pill),
// keeping the first protect cells — the amber attention bar — untouched to the
// left of the fill. With protect<=0 the fill spans the whole width.
func fillRow(line string, fill lipgloss.Style, width, protect int) string {
	if protect <= 0 {
		return fill.Width(width).Render(ansi.Strip(line))
	}
	prefix := ansi.Truncate(line, protect, "") // styled leading cells (the ▍ bar), kept as-is
	plain := []rune(ansi.Strip(line))
	if protect > len(plain) {
		protect = len(plain)
	}
	return prefix + fill.Width(width-protect).Render(string(plain[protect:]))
}

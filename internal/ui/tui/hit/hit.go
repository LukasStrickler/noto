// Package hit maps a terminal mouse coordinate back to the UI element drawn
// at that cell.
//
// The TUI renders by composing styled strings (lipgloss JoinHorizontal/
// JoinVertical), a process that throws away where each element lands. This
// package reintroduces that information the only place it is cheaply known:
// at render time. As a view draws a clickable element it records the
// element's screen rectangle together with a value describing what a click
// there should do. After the frame is drawn the resulting [Map] answers one
// question — "what is under cell (x,y)?" — for the next mouse event.
//
// The payload type T is deliberately generic so this package stays free of
// any UI or framework dependency: the caller decides what a click *means*.
// The TUI instantiates Map with a func() tea.Cmd, so a region literally
// carries the command to run when it is clicked.
//
// # Usage
//
// Build a fresh Map each frame (it is just a pair of slices) and register
// regions with the [Row] builder, which tracks column offsets for you so no
// view ever hand-computes a click target:
//
//	m := &hit.Map[Action]{}
//	row := hit.NewRow(m, originX, y)
//	row.Add(logo)            // not clickable
//	row.Hit(pill, onSelect)  // clickable: registers its rect automatically
//	line := row.String()
//
// Then on the next mouse event:
//
//	if act, ok := m.At(mouse.X, mouse.Y); ok { return m, act() }
//
// A region registered this frame is queried on the next event, so payloads
// are at most one frame stale — fine, because handlers re-derive from
// current state.
package hit

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Rect is a rectangle of terminal cells. The origin (0,0) is the top-left
// cell of the area the rect is expressed in; X grows right, Y grows down.
type Rect struct{ X, Y, W, H int }

// Contains reports whether cell (x,y) lies inside r. The right and bottom
// edges are exclusive, so adjacent rects never both claim a cell.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// Map collects the clickable regions of one rendered frame and resolves a
// clicked cell to the value registered for the region on top of it. The
// zero value is an empty, ready-to-use Map; a nil *Map is safe for every
// method, so callers never have to guard.
type Map[T any] struct {
	rects []Rect
	vals  []T
}

// Add registers a clickable region paired with v. Zero-area rects and a nil
// receiver are ignored so the [Row] builder and measure-only render passes
// need no special-casing.
func (m *Map[T]) Add(r Rect, v T) {
	if m == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	m.rects = append(m.rects, r)
	m.vals = append(m.vals, v)
}

// At returns the value of the topmost region containing (x,y). Regions added
// later win, so a child or overlay drawn after its parent correctly takes
// the hit. ok is false when no region covers the cell.
func (m *Map[T]) At(x, y int) (v T, ok bool) {
	if m == nil {
		return v, false
	}
	for i := len(m.rects) - 1; i >= 0; i-- {
		if m.rects[i].Contains(x, y) {
			return m.vals[i], true
		}
	}
	return v, false
}

// Len reports how many regions are registered.
func (m *Map[T]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.rects)
}

// Translate shifts every registered region by (dx,dy). Use it to lift a Map
// built in a sub-view's local coordinates (origin at the sub-view's
// top-left) into its parent's coordinate space before merging or querying.
func (m *Map[T]) Translate(dx, dy int) {
	if m == nil {
		return
	}
	for i := range m.rects {
		m.rects[i].X += dx
		m.rects[i].Y += dy
	}
}

// Merge copies child's regions into m, offset by (dx,dy). It is the
// composition counterpart to lipgloss placing a child string at (dx,dy):
// the child registers in its own local coordinates, the parent merges it at
// the offset it drew the child. child is left unmodified.
func (m *Map[T]) Merge(child *Map[T], dx, dy int) {
	if m == nil || child == nil {
		return
	}
	for i, r := range child.rects {
		m.Add(Rect{X: r.X + dx, Y: r.Y + dy, W: r.W, H: r.H}, child.vals[i])
	}
}

// Row renders a single horizontal line of segments while tracking the X
// offset of each one, so a clickable segment can register its own hit rect
// without the caller doing any column arithmetic. It is the recommended way
// to build a row of clickable chrome (nav pills, hint chips, a tab bar).
//
// Segment widths are measured with lipgloss so styled (ANSI-coloured)
// segments register their true on-screen width.
type Row[T any] struct {
	m    *Map[T]
	x, y int
	b    strings.Builder
}

// NewRow starts a row whose first segment is drawn at column originX on row
// y, registering clickable segments into m. m may be nil — segments still
// render and nothing is registered — so the same code path can serve a
// measure-only pass.
func NewRow[T any](m *Map[T], originX, y int) *Row[T] {
	return &Row[T]{m: m, x: originX, y: y}
}

// Add appends a non-clickable segment (logo, separators, padding) and
// advances the column cursor past it.
func (r *Row[T]) Add(s string) *Row[T] {
	r.b.WriteString(s)
	r.x += lipgloss.Width(s)
	return r
}

// Hit appends a clickable segment, registering its rect → v at the current
// column, then advances the cursor past it.
func (r *Row[T]) Hit(s string, v T) *Row[T] {
	w := lipgloss.Width(s)
	r.m.Add(Rect{X: r.x, Y: r.y, W: w, H: 1}, v)
	r.b.WriteString(s)
	r.x += w
	return r
}

// String returns the assembled line.
func (r *Row[T]) String() string { return r.b.String() }

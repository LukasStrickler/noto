// Package layout owns responsive breakpoints + Panel primitive.
// Centralizing this here means no screen has to remember the magic
// numbers and no panel is rendered without consistent borders.
package layout

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// Breakpoint represents one of three terminal-width regimes.
type Breakpoint int

const (
	Narrow  Breakpoint = iota // <90 cols  — list-only, palette-driven nav
	Regular                   // 90..119  — 2 panes
	Wide                      // 120+     — 3 panes
)

// BreakpointFor returns the regime for the given width.
func BreakpointFor(width int) Breakpoint {
	switch {
	case width < 90:
		return Narrow
	case width < 120:
		return Regular
	default:
		return Wide
	}
}

// Panel renders a bordered panel with optional title/subtitle.
type Panel struct {
	Title    string
	Subtitle string
	// Right is an opt-in, PRE-STYLED right-aligned header segment, rendered
	// as-is (NOT re-wrapped in Muted the way Subtitle is) so a caller can place
	// its own colored content there — e.g. a meeting's status flag whose colour
	// the Muted wrap would otherwise clobber. When set it owns the right-aligned
	// slot (in place of Subtitle); empty leaves the header byte-for-byte as before.
	Right   string
	Width   int
	Height  int
	Focused bool
	Body    string
}

// Render draws the panel. Width/Height include the border; Body is
// clipped to fit inside. Empty Body gets a single blank line so the
// border still draws.
func (p Panel) Render(s theme.Styles) string {
	style := s.Panel
	if p.Focused {
		style = s.PanelFocused
	}
	if p.Width <= 4 {
		return ""
	}
	innerW := p.Width - 4 // 2 border + 2 padding
	header := ""
	if p.Title != "" {
		title := s.PanelTitle.Render(p.Title)
		if p.Right != "" {
			// Pre-styled segment: place it as-is at the far right.
			pad := innerW - lipgloss.Width(title) - lipgloss.Width(p.Right)
			if pad < 1 {
				pad = 1
			}
			title += strings.Repeat(" ", pad) + p.Right
		} else if p.Subtitle != "" {
			pad := innerW - lipgloss.Width(title) - lipgloss.Width(p.Subtitle)
			if pad < 1 {
				pad = 1
			}
			title += strings.Repeat(" ", pad) + s.Muted.Render(p.Subtitle)
		}
		rule := s.Muted.Render(strings.Repeat("─", max(0, innerW)))
		header = title + "\n" + rule + "\n"
	}
	body := p.Body
	if body == "" {
		body = " "
	}
	rendered := header + body
	// lipgloss v2 uses a border-box model: Width/Height set the TOTAL
	// rendered size, border and padding included (v1 added them outside
	// the given size). So pass the full panel dimensions — the content
	// area then works out to innerW (p.Width - 2 border - 2 padding).
	boxH := p.Height
	if boxH < 3 {
		boxH = 3
	}
	// Clip the content to the inner height (box height minus the two border
	// rows) so an over-tall Body can't push the panel past the height the
	// layout solver handed it and misalign sibling panels — lipgloss treats
	// Height as a floor, not a ceiling, so without this a tall Body overflows
	// the border. MaxWidth/MaxHeight backstop any width-wrap expansion. All
	// three are no-ops when the content already fits, so well-sized panels
	// render byte-for-byte as before.
	if innerH := boxH - 2; len(rendered) > 0 {
		if lines := strings.Split(rendered, "\n"); len(lines) > innerH {
			rendered = strings.Join(lines[:max(1, innerH)], "\n")
		}
	}
	return style.Width(p.Width).Height(boxH).MaxWidth(p.Width).MaxHeight(boxH).Render(rendered)
}

// BodyOffset returns the (x, y) offset from a panel's top-left corner to the
// first cell of its Body — past the border and padding, and past the title +
// rule rows when a Title is set. Use it to map a body-relative position (e.g. a
// clicked list row or tab) back to an absolute screen cell for hit-testing, so
// the math stays in lock-step with Render instead of being guessed at the call
// site.
func (p Panel) BodyOffset() (x, y int) {
	x, y = 2, 1 // left border + left padding ; top border
	if p.Title != "" {
		y += 2 // title row + rule row (see Render)
	}
	return x, y
}

// HStack lays children side-by-side with a single-cell gap. The gap here
// matches the gap a sibling Split(..., gap=1, ...) reserves, so a row's
// rendered width lines up with the widths the solver handed out.
func HStack(children ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(children, " ")...)
}

// VStack lays children top-to-bottom with no gap, matching a sibling
// Split(..., gap=0, ...).
func VStack(children ...string) string {
	return lipgloss.JoinVertical(lipgloss.Left, children...)
}

// --- 1-D flex solver ----------------------------------------------------
//
// Split is the single place that turns a total number of cells into per-
// section sizes. Screens describe a row/column as a list of Slots — fixed
// or weighted — and let the solver do the arithmetic, instead of each
// screen hand-rolling `width/3`, `total - other - 1`, and `if x < min`.
// One solver means one place to get the gap accounting and the min
// clamping right, and one place to test it.

// Slot is one section of a Split. Build it with Fixed, Flex, or FlexMin —
// the zero value is a 0-cell fixed slot.
type Slot struct {
	fixed  int // exact size, used when weight == 0
	weight int // share of leftover space when > 0 (flexible)
	min    int // floor applied to a flexible slot's size
}

// Fixed is a section of an exact size (e.g. a status bar row, a sidebar of
// a known width). It is clamped to the space that remains.
func Fixed(n int) Slot { return Slot{fixed: n} }

// Flex is a section that grows to fill leftover space, in proportion to
// weight against the other flexible sections (weight < 1 is treated as 1).
func Flex(weight int) Slot { return FlexMin(weight, 0) }

// FlexMin is a Flex section that never shrinks below min cells. When the
// proportional share would dip under min, the slot is pinned at min and the
// remaining slots re-share what's left.
func FlexMin(weight, min int) Slot {
	if weight < 1 {
		weight = 1
	}
	if min < 0 {
		min = 0
	}
	return Slot{weight: weight, min: min}
}

// SidebarSplit divides total cells into a [sidebar, content] pair separated by
// the usual 1-cell gap, the way every two-pane screen (dashboard, people,
// config) lays out its left/right columns. pref is the desired sidebar width in
// cells; it is clamped so the sidebar never drops below minSidebar and the
// content never drops below minContent. The two returned widths sum to total-1.
//
// It exists so the clamp logic lives in one tested place instead of being
// re-derived (and drifting) in each screen's view; the screens pass their
// shared, persisted sidebar preference straight through.
func SidebarSplit(total, pref, minSidebar, minContent int) (sidebar, content int) {
	maxSidebar := total - 1 - minContent
	if maxSidebar < minSidebar {
		maxSidebar = minSidebar
	}
	if pref < minSidebar {
		pref = minSidebar
	}
	if pref > maxSidebar {
		pref = maxSidebar
	}
	cols := Split(total, 1, Fixed(pref), FlexMin(1, minContent))
	return cols[0], cols[1]
}

// Split divides total cells among slots, reserving gap cells between each
// adjacent pair. Fixed slots take their size; flexible slots share what's
// left by weight, honoring mins, with the integer-division remainder handed
// to the last flexible slot so the result sums to exactly total-gaps. The
// returned slice is index-aligned with slots and never contains negatives.
func Split(total, gap int, slots ...Slot) []int {
	n := len(slots)
	out := make([]int, n)
	if n == 0 {
		return out
	}
	avail := total - gap*(n-1)
	if avail < 0 {
		avail = 0
	}

	pinned := make([]bool, n)
	remaining := avail
	weightSum := 0

	// Fixed slots claim their size up front (clamped to what's left).
	for i, s := range slots {
		if s.weight <= 0 {
			out[i] = clampNonNeg(s.fixed, remaining)
			remaining -= out[i]
			pinned[i] = true
			continue
		}
		weightSum += s.weight
	}

	// Pin any flexible slot whose proportional share falls below its min,
	// then re-share among the rest. Each pass pins at most one slot, so
	// this settles in at most n passes (n is tiny in practice).
	for {
		changed := false
		for i, s := range slots {
			if pinned[i] || weightSum == 0 {
				continue
			}
			if remaining*s.weight/weightSum < s.min {
				out[i] = clampNonNeg(s.min, remaining)
				remaining -= out[i]
				weightSum -= s.weight
				pinned[i] = true
				changed = true
				break
			}
		}
		if !changed {
			break
		}
	}

	// Distribute the remainder across the still-flexible slots by weight;
	// the last one absorbs the rounding so the row sums exactly.
	if weightSum > 0 {
		used, last := 0, -1
		for i, s := range slots {
			if pinned[i] {
				continue
			}
			out[i] = remaining * s.weight / weightSum
			used += out[i]
			last = i
		}
		if last >= 0 {
			out[last] += remaining - used
		}
	}
	return out
}

func clampNonNeg(n, hi int) int {
	if n < 0 {
		return 0
	}
	if n > hi {
		return hi
	}
	return n
}

func joinWithGap(items []string, gap string) []string {
	if len(items) <= 1 {
		return items
	}
	out := make([]string, 0, len(items)*2-1)
	for i, it := range items {
		if i > 0 {
			out = append(out, gap)
		}
		out = append(out, it)
	}
	return out
}

// Clamp keeps n inside [lo, hi].
func Clamp(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

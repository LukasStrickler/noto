// Package layout owns responsive breakpoints + Panel primitive.
// Centralizing this here means no screen has to remember the magic
// numbers and no panel is rendered without consistent borders.
package layout

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// Breakpoint represents one of three terminal-width regimes.
type Breakpoint int

const (
	Narrow  Breakpoint = iota // <90 cols  — list-only, palette-driven nav
	Regular                    // 90..119  — 2 panes
	Wide                       // 120+     — 3 panes
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
	Width    int
	Height   int
	Focused  bool
	Body     string
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
		if p.Subtitle != "" {
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
	h := p.Height - 2
	if h < 1 {
		h = 1
	}
	return style.Width(p.Width - 2).Height(h).Render(rendered)
}

// HStack lays children side-by-side with a single-cell gap.
func HStack(children ...string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, joinWithGap(children, " ")...)
}

// VStack lays children top-to-bottom.
func VStack(children ...string) string {
	return lipgloss.JoinVertical(lipgloss.Left, children...)
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

package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/scroll"
)

// rowList is the ONE path every vertical, scrollable list of full-width rows
// goes through — the meeting directory and the people directory — the same way
// layout.Split owns sizing, scroll owns the gutter math, and hit owns click
// geometry. It owns the MACHINERY (line geometry, windowing, the scrollbar
// column, click-region registration) and hands the CONTENT back to the caller:
// each screen keeps its own row look + pointer feedback via the render callback,
// so a list's bespoke styling (the meeting counters, the people merge/review
// states) is untouched while the scroll plumbing is shared.
//
// Efficient by construction: it renders ONLY the rows whose line span intersects
// the viewport, so a 500-person directory costs the same per frame as a 5-person
// one (the window, not O(all rows)). That demands lineCount(i) be cheap and
// width-independent — the same contract the meeting list's rowLineCount already
// honours — so the window is computed without rendering a single row.
//
// Scroll STATE lives in the caller (its own offset field, advanced by
// scroll.Follow on the arrows and panned by the wheel), exactly like the meeting
// list already does; rowList only WINDOWS at the offset it's handed (clamping
// purely for display, so a resize self-corrects without a stale write).
type rowList struct {
	ctx       screenCtx
	width     int // inner width available to rows; the bar, when needed, claims one column of it
	height    int // viewport height in lines (the row budget)
	originX   int // absolute screen x of a row's first cell
	originY   int // absolute screen y of the first row's first line (header already accounted for)
	count     int // number of rows
	cursor    int // the row pointer.state treats as active/selected
	offset    int // free-scroll line offset; clamped here for display
	clickable bool

	lineCount func(i int) int                            // cheap, width-independent row height in lines
	rowID     func(i int) string                         // unique click id; "" → row not clickable
	onClick   func(i int) tea.Cmd                        // click action for row i
	prepare   func(contentW int)                         // optional one-shot setup once the content width is known
	render    func(i, contentW int, st uiState) []string // row i's lines at contentW, in pointer state st
}

// view renders the windowed list block (height lines tall, scrollbar joined when
// the content overflows) and registers a click region per visible row.
func (l rowList) view() string {
	s := l.ctx.styles
	if l.height < 1 || l.count == 0 {
		return strings.Join(make([]string, max(0, l.height)), "\n")
	}

	// Line geometry — accumulate each row's start WITHOUT rendering, so the window
	// and the scrollbar decision are known before any row is built.
	starts := make([]int, l.count)
	total := 0
	for i := 0; i < l.count; i++ {
		starts[i] = total
		total += l.lineCount(i)
	}

	off := scroll.Clamp(l.offset, total, l.height)
	barNeeded := scroll.Needed(total, l.height)
	contentW := l.width
	if barNeeded {
		contentW = l.width - 1 // reserve the scrollbar column, then build rows to it
	}
	if l.prepare != nil {
		l.prepare(contentW)
	}

	end := off + l.height
	rows := make([]string, l.height)
	for i := 0; i < l.count; i++ {
		top, bot := starts[i], starts[i]+l.lineCount(i)
		if bot <= off || top >= end {
			continue // row entirely above or below the viewport — never built
		}
		id := ""
		if l.rowID != nil {
			id = l.rowID(i)
		}
		st := uiNormal
		if l.clickable && id != "" {
			st = l.ctx.pointer().state(id, i == l.cursor)
		}
		lines := l.render(i, contentW, st)

		visTop, visBot := max(top, off), min(bot, end)
		for y := visTop; y < visBot; y++ {
			if li := y - top; li >= 0 && li < len(lines) {
				rows[y-off] = lines[li]
			}
		}
		if l.clickable && id != "" && l.onClick != nil {
			i := i
			l.ctx.hits.Add(
				hit.Rect{X: l.originX, Y: l.originY + (visTop - off), W: contentW, H: visBot - visTop},
				region{id: id, onClick: func() tea.Cmd { return l.onClick(i) }})
		}
	}

	if barNeeded {
		return attachScrollbar(rows, contentW, total, off, s)
	}
	return strings.Join(rows, "\n")
}

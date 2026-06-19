package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func rowListTestCtx() screenCtx {
	return screenCtx{
		ctx:    context.Background(),
		keys:   keys.New(),
		styles: theme.NewStyles(),
		hits:   &hit.Map[region]{},
	}
}

// The list must render ONLY the rows in the viewport — a long directory costs
// the window, not O(all rows). With 200 single-line rows in a 10-line viewport,
// at most a window's worth of render calls may fire.
func TestRowListRendersOnlyVisible(t *testing.T) {
	const n, h = 200, 10
	rendered := map[int]bool{}
	l := rowList{
		ctx: rowListTestCtx(), width: 40, height: h, count: n, cursor: 0, offset: 0, clickable: true,
		lineCount: func(int) int { return 1 },
		rowID:     func(i int) string { return fmt.Sprintf("r:%d", i) },
		onClick:   func(int) tea.Cmd { return nil },
		render: func(i, contentW int, _ uiState) []string {
			rendered[i] = true
			return []string{fmt.Sprintf("row-%d", i)}
		},
	}
	out := l.view()

	if len(rendered) > h {
		t.Fatalf("rendered %d rows for a %d-line viewport; want ≤ window", len(rendered), h)
	}
	if !rendered[0] || rendered[h] {
		t.Fatalf("window is wrong: row0=%v rowH=%v (want top rows only)", rendered[0], rendered[h])
	}
	if got := strings.Count(out, "\n") + 1; got != h {
		t.Fatalf("block is %d lines, want exactly the budget %d", got, h)
	}
	if !strings.Contains(out, "row-0") || strings.Contains(out, "row-50") {
		t.Fatal("windowed content is wrong")
	}
}

// At a non-zero offset only the offset window renders, and a click region maps to
// the row actually shown at that screen position.
func TestRowListWindowsAtOffsetAndRegistersHits(t *testing.T) {
	const n, h, off = 100, 5, 20
	ctx := rowListTestCtx()
	clicked := -1
	l := rowList{
		ctx: ctx, width: 30, height: h, count: n, cursor: off, offset: off, originX: 0, originY: 0, clickable: true,
		lineCount: func(int) int { return 1 },
		rowID:     func(i int) string { return fmt.Sprintf("r:%d", i) },
		onClick:   func(i int) tea.Cmd { clicked = i; return nil },
		render:    func(i, _ int, _ uiState) []string { return []string{fmt.Sprintf("p-%d", i)} },
	}
	out := l.view()
	if !strings.Contains(out, "p-20") || !strings.Contains(out, "p-24") || strings.Contains(out, "p-25") {
		t.Fatalf("offset window wrong:\n%s", out)
	}
	// Row shown at the top screen line (y=0) must be item 20.
	r, ok := ctx.hits.At(0, 0)
	if !ok {
		t.Fatal("no hit region at the first row line")
	}
	r.onClick()
	if clicked != off {
		t.Fatalf("top row click hit item %d, want %d", clicked, off)
	}
}

// A list that fits needs no scrollbar; one that overflows joins the gutter.
func TestRowListScrollbarOnlyWhenOverflowing(t *testing.T) {
	mk := func(n int) string {
		return rowList{
			ctx: rowListTestCtx(), width: 20, height: 8, count: n,
			lineCount: func(int) int { return 1 },
			render:    func(i, w int, _ uiState) []string { return []string{strings.Repeat("x", w)} },
		}.view()
	}
	fits := mk(5)
	overflow := mk(40)
	if w := lineWidth(fits); w != 20 {
		t.Fatalf("fitting list should use full width 20, got %d", w)
	}
	if w := lineWidth(overflow); w != 20 {
		t.Fatalf("overflowing list should still total width 20 (content 19 + bar), got %d", w)
	}
	if fits == overflow {
		t.Fatal("overflowing list should differ (scrollbar gutter) from a fitting one")
	}
}

func lineWidth(block string) int {
	first := strings.SplitN(block, "\n", 2)[0]
	return lipgloss.Width(first)
}

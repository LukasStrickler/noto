package layout

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func TestSplit_SumsToAvailableMinusGaps(t *testing.T) {
	cases := []struct {
		name  string
		total int
		gap   int
		slots []Slot
	}{
		{"two flex even", 100, 1, []Slot{Flex(1), Flex(1)}},
		{"one third / two thirds", 120, 1, []Slot{Flex(1), Flex(2)}},
		{"fixed plus flex", 80, 0, []Slot{Fixed(20), Flex(1)}},
		{"three flex", 99, 2, []Slot{Flex(1), Flex(1), Flex(1)}},
		{"flex with min, roomy", 200, 1, []Slot{FlexMin(1, 28), Flex(2)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sum(Split(c.total, c.gap, c.slots...))
			want := c.total - c.gap*(len(c.slots)-1)
			if got != want {
				t.Fatalf("sizes sum to %d; want %d (total %d, gaps %d)", got, want, c.total, c.gap*(len(c.slots)-1))
			}
		})
	}
}

func TestSplit_OneThirdTwoThirds(t *testing.T) {
	got := Split(120, 1, Flex(1), Flex(2))
	// avail = 119; 119/3 = 39 r2 -> left 39, right gets the remainder.
	if got[0] != 39 || got[1] != 80 {
		t.Fatalf("Split(120,1,Flex1,Flex2) = %v; want [39 80]", got)
	}
}

func TestSplit_FixedTakesExactSize(t *testing.T) {
	got := Split(30, 0, Flex(1), Fixed(6))
	if got[1] != 6 {
		t.Fatalf("fixed slot = %d; want 6", got[1])
	}
	if got[0] != 24 {
		t.Fatalf("flex slot = %d; want 24", got[0])
	}
}

func TestSplit_FixedZeroGivesFlexEverything(t *testing.T) {
	got := Split(30, 0, FlexMin(1, 6), Fixed(0))
	if got[0] != 30 || got[1] != 0 {
		t.Fatalf("Split(30,0,FlexMin(1,6),Fixed(0)) = %v; want [30 0]", got)
	}
}

func TestSplit_MinHonoredWhenProportionWouldUndercut(t *testing.T) {
	// Left's proportional share (90/3 = 30) clears its min, so no pin.
	if got := Split(90, 0, FlexMin(1, 28), Flex(2)); got[0] < 28 {
		t.Fatalf("left = %d; want >= 28", got[0])
	}
	// Tighter: 1:2 over avail 60 gives left 20, below its 28 min, so the
	// solver must pin it at 28 and let the right column shrink.
	got := Split(60, 0, FlexMin(1, 28), Flex(2))
	if got[0] != 28 {
		t.Fatalf("left = %d; want 28 (pinned at min)", got[0])
	}
	if got[0]+got[1] != 60 {
		t.Fatalf("sizes %v sum to %d; want 60", got, got[0]+got[1])
	}
}

func TestSplit_OverflowShrinksGracefully(t *testing.T) {
	// Fixed slots want 50 cells from a 10-cell budget; nothing goes
	// negative and the result never exceeds the budget.
	got := Split(10, 0, Fixed(30), Fixed(20))
	for i, v := range got {
		if v < 0 {
			t.Fatalf("slot %d negative: %v", i, got)
		}
	}
	if sum(got) > 10 {
		t.Fatalf("sizes %v sum to %d; exceeds total 10", got, sum(got))
	}
}

func TestSplit_EmptyAndSingle(t *testing.T) {
	if got := Split(50, 1); len(got) != 0 {
		t.Fatalf("no slots = %v; want empty", got)
	}
	if got := Split(50, 1, Flex(1)); len(got) != 1 || got[0] != 50 {
		t.Fatalf("single flex = %v; want [50]", got)
	}
}

// Panel must never exceed the cell budget the solver hands it: lipgloss
// treats Height as a floor, so an over-tall or over-wide Body would otherwise
// overflow the border and misalign sibling panels. Render must clip to exactly
// Width x Height. (Pre-fix, a Height=6 panel with a 50-line body rendered 54
// lines.) Guards the border-box clipping in Render.
func TestPanel_RenderClipsToBudget(t *testing.T) {
	st := theme.NewStyles()
	tallBody := strings.TrimRight(strings.Repeat("line\n", 50), "\n")
	wideBody := strings.Repeat("x", 200)

	cases := []struct {
		name         string
		panel        Panel
		wantW, wantH int
	}{
		{"over-tall body with title", Panel{Width: 24, Height: 6, Title: "T", Body: tallBody}, 24, 6},
		{"over-tall body no title", Panel{Width: 24, Height: 6, Body: tallBody}, 24, 6},
		{"over-wide body", Panel{Width: 24, Height: 5, Body: wideBody}, 24, 5},
		{"fits exactly", Panel{Width: 24, Height: 5, Body: "a\nb"}, 24, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := c.panel.Render(st)
			if got := lipgloss.Width(out); got != c.wantW {
				t.Errorf("rendered width = %d; want %d (must not overflow panel width)", got, c.wantW)
			}
			if got := lipgloss.Height(out); got != c.wantH {
				t.Errorf("rendered height = %d; want %d (must not overflow panel height)", got, c.wantH)
			}
		})
	}
}

func TestSplit_NeverNegativeOnTinyTotals(t *testing.T) {
	for total := 0; total <= 8; total++ {
		got := Split(total, 1, FlexMin(1, 6), Fixed(6))
		for i, v := range got {
			if v < 0 {
				t.Fatalf("total %d slot %d negative: %v", total, i, got)
			}
		}
	}
}

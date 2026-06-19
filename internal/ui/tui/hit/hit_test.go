package hit

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRectContainsEdges(t *testing.T) {
	r := Rect{X: 2, Y: 1, W: 3, H: 2} // cols 2..4, rows 1..2
	in := [][2]int{{2, 1}, {4, 1}, {2, 2}, {4, 2}, {3, 1}}
	for _, c := range in {
		if !r.Contains(c[0], c[1]) {
			t.Errorf("Contains(%d,%d) = false; want inside", c[0], c[1])
		}
	}
	// Right and bottom edges are exclusive; everything past them is outside.
	out := [][2]int{{1, 1}, {5, 1}, {2, 0}, {2, 3}, {5, 3}}
	for _, c := range out {
		if r.Contains(c[0], c[1]) {
			t.Errorf("Contains(%d,%d) = true; want outside", c[0], c[1])
		}
	}
}

func TestMapAtTopmostWins(t *testing.T) {
	m := &Map[string]{}
	m.Add(Rect{X: 0, Y: 0, W: 10, H: 1}, "background")
	m.Add(Rect{X: 3, Y: 0, W: 4, H: 1}, "button") // overlaps, added later

	if v, ok := m.At(5, 0); !ok || v != "button" {
		t.Errorf("At(5,0) = %q,%v; want \"button\",true (later region on top)", v, ok)
	}
	if v, ok := m.At(1, 0); !ok || v != "background" {
		t.Errorf("At(1,0) = %q,%v; want \"background\",true", v, ok)
	}
	if _, ok := m.At(0, 5); ok {
		t.Errorf("At(0,5) = ok; want miss outside any region")
	}
}

func TestMapZeroAreaAndNilSafe(t *testing.T) {
	m := &Map[int]{}
	m.Add(Rect{X: 0, Y: 0, W: 0, H: 1}, 1) // zero width — ignored
	m.Add(Rect{X: 0, Y: 0, W: 2, H: 0}, 2) // zero height — ignored
	if m.Len() != 0 {
		t.Errorf("Len = %d; want zero-area rects ignored", m.Len())
	}

	var nilMap *Map[int]
	nilMap.Add(Rect{X: 0, Y: 0, W: 1, H: 1}, 9) // must not panic
	if _, ok := nilMap.At(0, 0); ok {
		t.Errorf("nil Map.At = ok; want miss")
	}
	if nilMap.Len() != 0 {
		t.Errorf("nil Map.Len = %d; want 0", nilMap.Len())
	}
}

func TestMapTranslateAndMerge(t *testing.T) {
	child := &Map[string]{}
	child.Add(Rect{X: 0, Y: 0, W: 2, H: 1}, "a") // local coords

	parent := &Map[string]{}
	parent.Merge(child, 5, 3) // child was drawn at column 5, row 3

	if v, ok := parent.At(5, 3); !ok || v != "a" {
		t.Errorf("merged At(5,3) = %q,%v; want \"a\",true", v, ok)
	}
	if _, ok := parent.At(0, 0); ok {
		t.Errorf("merged region leaked to origin; merge offset not applied")
	}

	child.Translate(1, 0)
	if v, ok := child.At(1, 0); !ok || v != "a" {
		t.Errorf("after Translate, At(1,0) = %q,%v; want \"a\",true", v, ok)
	}
}

// The Row builder must place each registered rect at the running column,
// measuring styled segments by their on-screen width — this is the property
// that keeps hit rects aligned with what's actually drawn.
func TestRowTracksColumnsAcrossStyledSegments(t *testing.T) {
	style := lipgloss.NewStyle().Bold(true) // adds ANSI but no extra cells
	m := &Map[string]{}
	row := NewRow(m, 4, 2) // start at column 4, row 2

	row.Add("  ")                        // 2 non-clickable cells -> cursor 6
	row.Hit(style.Render("ab"), "first") // width 2 at col 6 -> cursor 8
	row.Add(" ")                         // gap -> cursor 9
	row.Hit("cde", "second")             // width 3 at col 9 -> cursor 12

	if got := lipgloss.Width(row.String()); got != 8 {
		t.Errorf("row width = %d; want 8 (2+2+1+3)", got)
	}

	// "first" occupies cols 6..7 on row 2.
	if v, ok := m.At(6, 2); !ok || v != "first" {
		t.Errorf("At(6,2) = %q,%v; want \"first\"", v, ok)
	}
	if v, ok := m.At(7, 2); !ok || v != "first" {
		t.Errorf("At(7,2) = %q,%v; want \"first\"", v, ok)
	}
	// Column 8 is the gap between the two hits — no region.
	if _, ok := m.At(8, 2); ok {
		t.Errorf("At(8,2) = ok; want gap between segments to be unclaimed")
	}
	// "second" occupies cols 9..11 on row 2.
	if v, ok := m.At(11, 2); !ok || v != "second" {
		t.Errorf("At(11,2) = %q,%v; want \"second\"", v, ok)
	}
	// Wrong row never matches.
	if _, ok := m.At(6, 1); ok {
		t.Errorf("At(6,1) = ok; want miss on wrong row")
	}
}

// A nil map passed to NewRow must still render every segment (measure-only
// pass) without registering or panicking.
func TestRowNilMapRendersWithoutRegistering(t *testing.T) {
	row := NewRow[string](nil, 0, 0)
	row.Add("logo ").Hit("pill", "x")
	if row.String() != "logo pill" {
		t.Errorf("row.String() = %q; want %q", row.String(), "logo pill")
	}
}

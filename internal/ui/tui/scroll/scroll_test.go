package scroll

import (
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func TestFollowKeepsCursorVisible(t *testing.T) {
	cases := []struct {
		name                              string
		total, height, cursorTop, cursorH int
		prefer, want                      int
	}{
		{"fits entirely", 5, 10, 4, 1, 0, 0},
		{"cursor above window pulls up", 100, 10, 3, 1, 20, 3},
		{"cursor below window pushes down", 100, 10, 25, 1, 0, 16},
		{"multi-line cursor tail must show", 100, 10, 18, 2, 0, 10},
		{"cursor inside window does not move", 100, 10, 5, 1, 3, 3},
		{"clamped to end", 100, 10, 99, 1, 0, 90},
		{"never negative", 100, 10, 0, 1, 50, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Follow(c.total, c.height, c.cursorTop, c.cursorH, c.prefer); got != c.want {
				t.Errorf("Follow(total=%d h=%d top=%d ch=%d prefer=%d) = %d; want %d",
					c.total, c.height, c.cursorTop, c.cursorH, c.prefer, got, c.want)
			}
			// Whatever it returns must keep the cursor span visible.
			off := Follow(c.total, c.height, c.cursorTop, c.cursorH, c.prefer)
			if c.cursorTop < off || c.cursorTop+c.cursorH > off+c.height {
				if c.total > c.height { // only when scrolling is even possible
					t.Errorf("cursor span [%d,%d) not visible in window [%d,%d)",
						c.cursorTop, c.cursorTop+c.cursorH, off, off+c.height)
				}
			}
		})
	}
}

func TestClampRange(t *testing.T) {
	if got := Clamp(999, 100, 10); got != 90 {
		t.Errorf("Clamp over-scroll = %d; want 90", got)
	}
	if got := Clamp(-5, 100, 10); got != 0 {
		t.Errorf("Clamp under-scroll = %d; want 0", got)
	}
	if got := Clamp(7, 5, 10); got != 0 {
		t.Errorf("Clamp when everything fits = %d; want 0", got)
	}
}

func TestNeeded(t *testing.T) {
	if Needed(5, 10) {
		t.Error("Needed should be false when content fits")
	}
	if !Needed(11, 10) {
		t.Error("Needed should be true when content overflows")
	}
}

func TestBarEmptyWhenFits(t *testing.T) {
	if got := Bar(10, 5, 0, theme.NewStyles()); got != "" {
		t.Errorf("Bar should be empty when content fits; got %q", got)
	}
}

func TestBarThumbTracksOffset(t *testing.T) {
	s := theme.NewStyles()
	const height, total = 10, 100

	// Thumb and track share the full-block glyph and differ ONLY by colour, so
	// compare the fully-rendered (styled) cell — exactly what Bar writes per line
	// — rather than the bare glyph, which can no longer tell them apart.
	thumbCell := s.ScrollThumb.Render(thumbGlyph)
	trackCell := s.ScrollTrack.Render(trackGlyph)
	if thumbCell == trackCell {
		t.Fatal("thumb and track must stay visually distinct (different colour)")
	}

	top := strings.Split(Bar(height, total, 0, s), "\n")
	bot := strings.Split(Bar(height, total, 90, s), "\n")

	if top[0] != thumbCell {
		t.Errorf("at offset 0 the thumb must touch the top; first cell = %q", top[0])
	}
	if bot[height-1] != thumbCell {
		t.Errorf("at the end the thumb must touch the bottom; last cell = %q", bot[height-1])
	}
	if top[height-1] != trackCell {
		t.Error("at offset 0 the bottom must be track, not thumb (it would imply no scroll room)")
	}
}

func TestThumbSpanMonotonic(t *testing.T) {
	// As offset grows, the thumb's start never moves up.
	prev := -1
	for off := 0; off <= 90; off += 10 {
		start, _ := thumb(10, 100, off)
		if start < prev {
			t.Errorf("thumb start went backwards at offset %d: %d < %d", off, start, prev)
		}
		prev = start
	}
}

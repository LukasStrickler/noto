// Package scroll is the ONE place vertical scrolling is reasoned about, the
// same way layout.Split owns sizing and hit.Map owns click geometry. A screen
// never hand-rolls "first visible line = cursor - height/2" or a bespoke
// scrollbar: it describes its content as lines and a focus point, and this
// package answers two questions — where does the window start, and what does the
// indicator look like.
//
// Two models, one primitive:
//
//   - cursor-follow (the meeting list, item lists): there is a selected row and
//     the window must keep it visible. Offset is DERIVED from the cursor every
//     frame via Follow — no scroll state to persist, so the cursor is the single
//     source of truth and View never has to mutate the model. Wheeling such a
//     list moves the cursor; the view follows for free.
//   - free-scroll (the transcript): there is no cursor, the user scrolls a long
//     body directly. The screen keeps an offset in its own state and clamps it
//     with Clamp; the wheel and up/down change that offset.
//
// Both render the same indicator via Bar, so every scrollable region in the app
// looks and behaves identically.
package scroll

import (
	"strings"

	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// Follow returns the first-visible-line index for a window of height lines over
// total lines, scrolled the MINIMUM amount needed to keep the line span
// [cursorTop, cursorTop+cursorH) fully visible, starting from prefer (the
// previous frame's offset, so a cursor moving inside the window doesn't scroll —
// only crossing an edge does). The result is always clamped to [0, max(0,
// total-height)], so it is safe to slice with directly.
//
// Pass prefer=0 for a stateless cursor-follow (the list rebuilds every frame and
// has no persisted offset): the window then sits as high as possible while still
// showing the cursor, which is the natural "selection pins to the bottom edge as
// you walk past the fold" behaviour.
func Follow(total, height, cursorTop, cursorH, prefer int) int {
	if height <= 0 {
		return 0
	}
	off := prefer
	// Cursor above the window → pull the window up to it.
	if cursorTop < off {
		off = cursorTop
	}
	// Cursor (or its tail, for a multi-line row) below the window → push down.
	if bottom := cursorTop + cursorH; bottom > off+height {
		off = bottom - height
	}
	return clamp(off, total, height)
}

// Clamp pins a free-scroll offset into the valid range [0, max(0,
// total-height)] — the transcript uses it so an over- or under-scroll can never
// show blank space past the end or hide the first line.
func Clamp(offset, total, height int) int {
	return clamp(offset, total, height)
}

func clamp(off, total, height int) int {
	max := total - height
	if max < 0 {
		max = 0
	}
	if off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	return off
}

// thumb returns the [start, end) line span of the scrollbar thumb within a
// gutter of height cells, representing a window of height lines scrolled to
// offset over total lines. The thumb is sized proportionally (never smaller than
// one cell) and pinned to the top/bottom when the content is at an edge, so the
// indicator reads as "you are at the start / end" exactly.
func thumb(height, total, offset int) (start, end int) {
	if total <= height || height <= 0 {
		return 0, height // full bar (caller decides whether to draw it)
	}
	size := height * height / total
	if size < 1 {
		size = 1
	}
	maxOff := total - height
	maxStart := height - size
	start = 0
	if maxOff > 0 {
		start = offset * maxStart / maxOff
	}
	// Pin to the edges so the thumb visibly touches top/bottom at the limits
	// even when integer rounding would leave a one-cell gap.
	if offset <= 0 {
		start = 0
	} else if offset >= maxOff {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	if start > maxStart {
		start = maxStart
	}
	return start, start + size
}

// The bar is drawn as a single column of SPACES coloured by their cell
// BACKGROUND (ScrollThumb / ScrollTrack) — NOT a foreground glyph. This is the
// crux of the cross-terminal fix: a foreground glyph (█, ▕, ░ — every variant we
// tried) only paints the font's glyph box, so when a terminal's line height
// exceeds 1.0 the inter-line leading shows through and a vertical run looks
// gappy/ragged. macOS Terminal.app does exactly this (it renders block elements
// straight from the font instead of redrawing them to fill the cell, the way
// Ghostty/iTerm2/kitty/WezTerm do). A cell BACKGROUND, by contrast, is painted
// across the whole cell including the leading, so a vertical run of
// background-filled spaces is gapless in EVERY terminal — the same reason the
// app's multi-line selection bars never show seams. The glyph is therefore a
// space; thumb vs track is purely a background-colour difference.
const (
	thumbGlyph = " " // space; ScrollThumb BACKGROUND paints the (gapless) thumb cell
	trackGlyph = " " // space; ScrollTrack BACKGROUND paints the faint gutter cell
)

// Bar renders the scrollbar gutter: height lines, ONE cell wide, the thumb
// styled with ScrollThumb over a ScrollTrack rail. It returns "" when every line
// fits (total <= height) so a caller can cheaply decide whether to reserve the
// gutter column at all:
//
//	if bar := scroll.Bar(h, total, off, s); bar != "" {
//	    body = lipgloss.JoinHorizontal(lipgloss.Top, rows, bar) // rows drawn at width-1
//	}
//
// Keeping the "needed?" test inside Bar means a screen never compares total to
// height itself — one rule, one place.
func Bar(height, total, offset int, s theme.Styles) string {
	if total <= height || height <= 0 {
		return ""
	}
	start, end := thumb(height, total, offset)
	var b strings.Builder
	for i := 0; i < height; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i >= start && i < end {
			b.WriteString(s.ScrollThumb.Render(thumbGlyph))
		} else {
			b.WriteString(s.ScrollTrack.Render(trackGlyph))
		}
	}
	return b.String()
}

// Needed reports whether a scrollbar is warranted (content overflows the
// window). Use it to reserve the gutter column BEFORE rendering rows, so row
// width and the bar agree:
//
//	w := innerW
//	if scroll.Needed(total, height) { w-- }
func Needed(total, height int) bool { return total > height && height > 0 }

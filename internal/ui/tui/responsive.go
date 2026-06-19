package tui

// responsive.go holds the small shared primitive for "make this label fit a
// width": shortenTo. The top nav pills and the list's counter strip render-to-
// measure (cell widths, not guesses) and step down to a shorter form when space
// is tight. The detail tab bar deliberately does NOT shorten — its pane is sized
// wide enough for the full strip (minContentW), so tabs always spell out in full.

// shortenTo drops trailing runes from s until it fits width cells — a compact
// "short form" (Dashboard → Dash), NOT a "Dash…": the truncation is the
// compression, so an ellipsis would just cost a cell it is trying to save. Runs
// on runes so multi-byte labels shorten by character, not byte. width <= 0 → "".
func shortenTo(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	return string(r[:width])
}

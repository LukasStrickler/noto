package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// The palette can hold more entries than fit (the action set plus meeting
// titles), so its viewport must FOLLOW the cursor — a bare filtered[:12] used to
// let the selection scroll off the bottom and disappear. Walking the cursor to an
// entry well past the first window must keep that entry's row on screen.
func TestPaletteWindowFollowsCursor(t *testing.T) {
	entries := make([]paletteEntry, 30)
	for i := range entries {
		entries[i] = paletteEntry{Label: fmt.Sprintf("Command-%02d", i), Action: "goto"}
	}
	p := newPalette(nil, entries)
	p.cursor = 22 // past the first 12-row window

	view := p.view(100, 40, theme.NewStyles(), "")
	if !strings.Contains(view, "Command-22") {
		t.Fatalf("selected entry (cursor=22) is not visible in the palette;\n%s", view)
	}
	// The selection marker rides the cursor's row, so it must be present too.
	if !strings.Contains(view, "▸") {
		t.Fatal("no selection marker rendered for the followed cursor")
	}
}

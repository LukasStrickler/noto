package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// replay.go lets a click stand in for a keystroke. Action chips already name a
// key binding (chip(s, k.Edit) draws "e edit"); making the chip clickable
// shouldn't duplicate the handler, so a click simply REPLAYS the bound key —
// the screen's existing key.Matches branch runs, and the behaviour stays
// defined exactly once. This is the same single-source principle the nav pills
// follow, applied to every action hint.

// keyPressFor builds the tea.KeyPressMsg a binding's key string denotes, so a
// replayed click is indistinguishable from the real keypress (key.Matches sees
// the same msg.String()). It covers the keys our chips actually use — the named
// editing/navigation keys plus any single rune; multi-key combos we don't put
// on a clickable chip return ok=false.
func keyPressFor(s string) (tea.KeyPressMsg, bool) {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}, true
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}, true
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, true
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}, true
	case "space", " ":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, true
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}, true
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}, true
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}, true
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}, true
	}
	if r := []rune(s); len(r) == 1 {
		return tea.KeyPressMsg{Code: r[0], Text: s}, true
	}
	return tea.KeyPressMsg{}, false
}

// replayKey is the clickAction that re-injects a binding's first key as a key
// press, routing it back through the root's Update so the matching handler
// fires. Returns nil when the binding has no replayable key (so the chip is
// inert rather than wrong).
func replayKey(b key.Binding) clickAction {
	ks := b.Keys()
	if len(ks) == 0 {
		return nil
	}
	msg, ok := keyPressFor(ks[0])
	if !ok {
		return nil
	}
	return func() tea.Cmd {
		return func() tea.Msg { return msg }
	}
}

// chipButton places a key-binding chip as a clickable button on row: it renders
// the binding's normal chip (key + description), lifts onto a subtle fill while
// hovered, and replays the bound key when clicked — so the chip and its
// keystroke are one action defined once. id must be unique within the frame
// (callers prefix by context, e.g. "hint:" / "people:act:"); the hover tint
// keeps the chip's exact width so a row never reflows under the pointer.
func chipButton(row *hit.Row[region], p pointer, s theme.Styles, id string, b key.Binding) {
	placeChip(row, p, s, id, chip(s, b), replayKey(b))
}

// chipButtonAs is chipButton with a context-specific label (chipAs), for spots
// where the binding's generic description doesn't fit (e.g. Tab as "meetings").
func chipButtonAs(row *hit.Row[region], p pointer, s theme.Styles, id string, b key.Binding, label string) {
	placeChip(row, p, s, id, chipAs(s, b, label), replayKey(b))
}

// placeChip places a clickable chip: render text, tint it (width-stable) while
// hovered, and run onClick on release. The chip-button helpers pass
// replayKey(b) as onClick (the common "click == keystroke" case); a caller that
// must do more first — e.g. focus a pane before the key fires — passes its own
// clickAction here.
func placeChip(row *hit.Row[region], p pointer, s theme.Styles, id, text string, onClick clickAction) {
	button{
		id:      id,
		onClick: onClick,
		render: func(st uiState) string {
			if st == uiHover {
				return paintRow(text, s.RowHoverBg, lipgloss.Width(text))
			}
			return text
		},
	}.place(row, p)
}

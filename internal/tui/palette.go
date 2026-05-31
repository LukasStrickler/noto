package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

type paletteEntry struct {
	Label  string
	Action string
	Param  string
	Hint   string
}

// palette is the modal command bar shown on `:`. Fuzzy-filters a fixed
// list of actions plus, optionally, meeting titles loaded from the API.
type palette struct {
	input    textinput.Model
	all      []paletteEntry
	filtered []paletteEntry
	cursor   int
}

func newPalette(_ notoapi.Client, entries []paletteEntry) *palette {
	ti := textinput.New()
	ti.Placeholder = "type to filter…"
	ti.Focus()
	ti.CharLimit = 100
	p := &palette{input: ti, all: entries}
	p.filter("")
	return p
}

func (p *palette) update(msg tea.Msg, _ theme.Styles) (*palette, tea.Cmd) {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.String() {
		case "esc":
			return nil, nil
		case "enter":
			if len(p.filtered) > 0 {
				e := p.filtered[p.cursor]
				return nil, func() tea.Msg {
					return commandMsg{Action: e.Action, Param: e.Param}
				}
			}
			return nil, nil
		case "up", "ctrl+p":
			if p.cursor > 0 {
				p.cursor--
			}
			return p, nil
		case "down", "ctrl+n":
			if p.cursor < len(p.filtered)-1 {
				p.cursor++
			}
			return p, nil
		}
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)
	p.filter(p.input.Value())
	return p, cmd
}

func (p *palette) filter(q string) {
	q = strings.ToLower(strings.TrimSpace(q))
	p.cursor = 0
	if q == "" {
		p.filtered = p.all
		return
	}
	out := p.filtered[:0]
	for _, e := range p.all {
		if fuzzyMatch(strings.ToLower(e.Label), q) {
			out = append(out, e)
		}
	}
	p.filtered = make([]paletteEntry, len(out))
	copy(p.filtered, out)
}

func (p *palette) view(width, height int, s theme.Styles, under string) string {
	w := min(width-6, 70)
	if w < 30 {
		w = max(30, width-2)
	}
	var rows []string
	rows = append(rows, s.HeaderEm.Render("◇ command menu"))
	rows = append(rows, s.ChipKey.Render(":")+" "+p.input.View())
	rows = append(rows, "")
	visible := 12
	if visible > len(p.filtered) {
		visible = len(p.filtered)
	}
	for i := 0; i < visible; i++ {
		e := p.filtered[i]
		labelRow := e.Label
		if e.Hint != "" {
			labelRow = e.Label + "  " + s.Muted.Render("("+e.Hint+")")
		}
		if i == p.cursor {
			rows = append(rows, s.RowSelected.Width(w-4).Render(" ▸ "+labelRow))
		} else {
			rows = append(rows, "   "+labelRow)
		}
	}
	if len(p.filtered) == 0 {
		rows = append(rows, s.Muted.Render("  no matches"))
	}
	rows = append(rows, "")
	rows = append(rows, s.HintBar.Render(s.ChipKey.Render("↑/↓")+" select   "+s.ChipKey.Render("⏎")+" run   "+s.ChipKey.Render("esc")+" close"))

	box := s.OverlayBox.Width(w).Render(strings.Join(rows, "\n"))
	return overlayCenter(under, box, width, height)
}

func fuzzyMatch(haystack, needle string) bool {
	// substring is fine for V1; bubbles/list does proper fuzzy but
	// we want our own for the action palette to mix in meeting titles
	// later without an extra dependency.
	return strings.Contains(haystack, needle)
}

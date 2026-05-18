// Package keys holds the central key map. Every screen consults the
// same bindings so users learn one set, not twelve.
package keys

import "github.com/charmbracelet/bubbles/key"

type Map struct {
	Quit          key.Binding
	Back          key.Binding
	Help          key.Binding
	Palette       key.Binding
	Search        key.Binding
	Up            key.Binding
	Down          key.Binding
	Left          key.Binding
	Right         key.Binding
	Enter         key.Binding
	Tab           key.Binding
	ShiftTab      key.Binding
	Refresh       key.Binding

	GoDashboard key.Binding
	GoMeetings  key.Binding
	GoRecord    key.Binding
	GoProviders key.Binding
	GoStorage   key.Binding
	GoSettings  key.Binding
	GoSpeakers  key.Binding

	Record  key.Binding
	Stop    key.Binding
	Marker  key.Binding

	CopyCitation key.Binding
	CopyPath     key.Binding
	OpenAgent    key.Binding
	Delete       key.Binding
	Verify       key.Binding
}

func New() Map {
	return Map{
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Back:    key.NewBinding(key.WithKeys("esc", "backspace"), key.WithHelp("esc", "back")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Palette: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
		Search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		// Arrow keys only. Vim aliases (h/j/k/l) collide with typing
		// into search/filter/title inputs — leave those to screens
		// that explicitly opt in when no input is focused.
		Up:    key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
		Down:  key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		Left:  key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "left")),
		Right: key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "right")),
		Enter:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "open")),
		Tab:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "next pane")),
		ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇤", "prev pane")),
		Refresh: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "refresh")),

		GoDashboard: key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "dashboard")),
		GoMeetings:  key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "meetings")),
		GoRecord:    key.NewBinding(key.WithKeys("3", "r"), key.WithHelp("r", "record")),
		// Providers and Settings both route to the unified config
		// screen so users can find the surface from either entry
		// point; the screen itself splits routing/paths from API
		// keys via tab-switchable panes.
		GoProviders: key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "config")),
		GoStorage:   key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "storage")),
		GoSettings:  key.NewBinding(key.WithKeys("6", ","), key.WithHelp(",", "config")),
		GoSpeakers:  key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "speakers")),

		Record: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "record")),
		Stop:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		Marker: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "marker")),

		CopyCitation: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "copy citation")),
		CopyPath:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "copy path")),
		OpenAgent:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "agent handoff")),
		Delete:       key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Verify:       key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "verify")),
	}
}

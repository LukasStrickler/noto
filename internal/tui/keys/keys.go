// Package keys holds the central key map. Every screen consults the
// same bindings so users learn one set, not twelve — and every chip,
// hint bar, and help line in the UI is rendered from these bindings
// (via Binding.Help()), so changing a key here updates the displayed
// label everywhere. There are no hand-written key strings in the views.
package keys

import "charm.land/bubbles/v2/key"

type Map struct {
	// Universal — available on every screen (unless a text input has
	// focus, in which case the root router stops intercepting letters).
	Quit     key.Binding
	Back     key.Binding
	Help     key.Binding
	Palette  key.Binding
	Search   key.Binding
	Refresh  key.Binding
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Enter    key.Binding
	Tab      key.Binding
	ShiftTab key.Binding

	// NOTE: top-level screen nav keys (the 1/2/3 digits + r/, aliases)
	// are NOT here. They are auto-numbered from the screen registry in
	// screen.go (topScreens) so adding a screen can't create a gap. This
	// map holds only universal and per-screen action keys.

	// Detail-pane tabs. TabNext/TabPrev cycle; Transcript/Speakers jump
	// straight to those two tabs. NextMatch/PrevMatch step transcript
	// search hits. These fire from both the meetings list and the
	// focused pane, so the experience is identical either way.
	TabNext    key.Binding
	TabPrev    key.Binding
	Transcript key.Binding
	Speakers   key.Binding
	NextMatch  key.Binding
	PrevMatch  key.Binding

	// Recorder.
	EditTitle key.Binding
	Record    key.Binding
	Stop      key.Binding
	Marker    key.Binding

	// Meeting actions (dashboard list + focused pane).
	OpenAgent   key.Binding
	Delete      key.Binding
	ClearSearch key.Binding

	// Config — API-keys section actions.
	Edit   key.Binding
	Test   key.Binding
	Remove key.Binding
}

func New() Map {
	return Map{
		Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Back:    key.NewBinding(key.WithKeys("esc", "backspace"), key.WithHelp("esc", "back")),
		Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Palette: key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "menu")),
		Search:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Refresh: key.NewBinding(key.WithKeys("ctrl+r"), key.WithHelp("ctrl+r", "refresh")),
		// Arrow keys are the canonical movement bindings; vim aliases
		// (h/l) ride along only on the pane's tab-cycle bindings below,
		// where no text input can be focused.
		Up:       key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "up")),
		Down:     key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "down")),
		Left:     key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "left")),
		Right:    key.NewBinding(key.WithKeys("right"), key.WithHelp("→", "right")),
		Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("⏎", "open")),
		Tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "next pane")),
		ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇤", "prev pane")),

		TabNext:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "next tab")),
		TabPrev:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "prev tab")),
		Transcript: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "transcript")),
		Speakers:   key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "speakers")),
		NextMatch:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next hit")),
		PrevMatch:  key.NewBinding(key.WithKeys("N", "shift+n"), key.WithHelp("N", "prev hit")),

		EditTitle: key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "title")),
		Record:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "record")),
		Stop:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "stop")),
		Marker:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "marker")),

		OpenAgent:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "agent")),
		Delete:      key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		ClearSearch: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "clear")),

		Edit:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Test:   key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "test")),
		Remove: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "remove")),
	}
}

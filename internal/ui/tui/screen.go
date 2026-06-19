package tui

import (
	"context"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// screenID is a short identifier used by router/palette.
type screenID string

const (
	sDashboard screenID = "dashboard"
	sPeople    screenID = "people"
	sRecorder  screenID = "recorder"
	sAgent     screenID = "agent"
	sConfig    screenID = "config"
	sHelp      screenID = "help"
)

// --- top-level screen registry ------------------------------------
//
// topScreens is the ONE source of truth for number-addressable screens.
// A screen's nav number is its 1-based position in this slice — reorder
// the slice and the numbers renumber themselves, with no gaps. The key
// binding, the global router's number handling, the command palette, and
// the help overlay are all DERIVED from this table (none of them hardcode
// a digit), so adding a screen is a single line here.
//
// To add a numbered screen:
//  1. add its screenID const above,
//  2. add its constructor to this slice in the position you want its
//     number to be,
//  3. done — `go test ./internal/tui/...` will verify the numbering.
//
// Screens that are NOT number-addressable (e.g. the agent handoff, which
// is only reachable contextually via `a`) are deliberately left out of
// this table; buildScreen() handles them in its fallback.
type screenReg struct {
	id      screenID
	title   string   // short label for nav chips + help
	palette string   // descriptive label for the command menu
	aliases []string // mnemonic alias keys, shown in help (e.g. "r")
	hidden  []string // alias keys bound but hidden from help (e.g. ",")
	new     func() screen
}

var topScreens = []screenReg{
	{id: sDashboard, title: "dashboard", palette: "Dashboard (meetings + search)", new: newDashboardScreen},
	{id: sPeople, title: "people", palette: "People (speaker profiles)", aliases: []string{"p"}, new: newPeopleScreen},
	{id: sRecorder, title: "recorder", palette: "Recorder", aliases: []string{"r"}, new: newRecorderScreen},
	{id: sConfig, title: "config", palette: "Config (routing, API keys, storage, paths)", hidden: []string{","}, new: newConfigScreen},
}

// navBinding builds the key.Binding for the screen at position i: the
// auto-assigned digit (i+1), plus any aliases/hidden aliases. Help text
// shows the digit and visible aliases ("2/r"); hidden aliases bind but
// stay out of the help string.
func (r screenReg) navBinding(i int) key.Binding {
	num := strconv.Itoa(i + 1)
	bound := append([]string{num}, r.aliases...)
	bound = append(bound, r.hidden...)
	help := num
	if len(r.aliases) > 0 {
		help = num + "/" + strings.Join(r.aliases, "/")
	}
	return key.NewBinding(key.WithKeys(bound...), key.WithHelp(help, r.title))
}

// screenNavBindings returns one binding per numbered screen, index-aligned
// with topScreens.
func screenNavBindings() []key.Binding {
	out := make([]key.Binding, len(topScreens))
	for i, sc := range topScreens {
		out[i] = sc.navBinding(i)
	}
	return out
}

// screenNavTarget returns the numbered screen a key selects, or "" if the
// key isn't a screen-nav key.
func screenNavTarget(msg tea.KeyPressMsg) screenID {
	for i, sc := range topScreens {
		if key.Matches(msg, sc.navBinding(i)) {
			return sc.id
		}
	}
	return ""
}

// screenCtx is read-only state the root model passes to each screen.
type screenCtx struct {
	ctx    context.Context
	client notoapi.Client
	keys   keys.Map
	styles theme.Styles
	width  int
	height int

	// sidebarWidth is the user's persisted left-pane width in cells, shared by
	// every two-pane screen. Pass it through layout.SidebarSplit (which clamps
	// it to the current terminal) when laying out the two columns.
	sidebarWidth int

	// Interaction wiring (see interactive.go). A screen makes body elements
	// clickable/hoverable exactly the way the chrome does — build a row with
	// hitRow and place buttons into it. hits is the current frame's click map;
	// bodyTop is the body's y-offset under the header, so hitRow can translate
	// body-local coordinates into the absolute ones root resolves clicks
	// against. hovered/pressed are the live pointer state. All zero/nil outside
	// the view() path, where registering hits is meaningless anyway.
	hits    *hit.Map[region]
	bodyTop int
	hovered string
	pressed string
}

// hitRow starts a clickable row for body content. x is an absolute column
// (the body spans the full width); y is relative to the top of the body. Place
// buttons into the returned row just like the chrome does:
//
//	row := ctx.hitRow(0, lineIdx)
//	button{id: "people:row:" + id, active: id == selected, onClick: ..., render: ...}.
//	    place(row, ctx.pointer())
//	line := row.String()
//
// Region ids must be unique across the whole frame (chrome + every screen), so
// prefix them with the screen/element, e.g. "people:row:<id>".
func (c screenCtx) hitRow(x, y int) *hit.Row[region] {
	return hit.NewRow(c.hits, x, c.bodyTop+y)
}

// pointer is the live hover/press state to hand to button.place.
func (c screenCtx) pointer() pointer {
	return pointer{hovered: c.hovered, pressed: c.pressed}
}

// screen is what every page implements. Stateful; lives in the
// router stack.
type screen interface {
	id() screenID
	title() string
	enter(ctx screenCtx, param string) tea.Cmd
	leave(ctx screenCtx) tea.Cmd
	update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd)
	view(ctx screenCtx) string

	// inputActive reports whether a text input on this screen has
	// focus. When true the root router stops intercepting letter keys
	// (so typing "r" into search doesn't jump to the recorder).
	inputActive() bool
}

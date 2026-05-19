package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// screenID is a short identifier used by router/palette.
type screenID string

const (
	sDashboard screenID = "dashboard"
	sMeetings  screenID = "meetings"
	sDetail    screenID = "detail"
	sTranscript screenID = "transcript"
	sRecorder  screenID = "recorder"
	sProviders screenID = "providers"
	sStorage   screenID = "storage"
	sSettings  screenID = "settings"
	sAgent     screenID = "agent"
	sSpeakers  screenID = "speakers"
	sConfig    screenID = "config"
	sHelp      screenID = "help"
)

// screenCtx is read-only state the root model passes to each screen.
type screenCtx struct {
	ctx    context.Context
	client notoapi.Client
	keys   keys.Map
	styles theme.Styles
	width  int
	height int
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
	helpKeys() []keys.Map // returns full key map; help overlay uses it

	// inputActive reports whether a text input on this screen has
	// focus. When true the root router stops intercepting letter keys
	// (so typing "r" into search doesn't jump to the recorder).
	inputActive() bool
}

package tui

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// minTermW/minTermH are the floor below which noto shows the too-small guard
// instead of a screen. The width floor is the combined two-pane minimum
// (minTwoPaneW, derived from the detail pane's full tab bar): noto always draws
// both panes, so when the terminal can't fit both floors it reports the
// shortfall rather than collapsing to one column or clipping the tab bar.
var minTermW = minTwoPaneW

const minTermH = 30

// clickAction is what a clickable region does when the mouse hits it: it
// yields a command to run (or nil for a no-op). Rendering code registers one
// per clickable element via the hit.Row builder, so the behaviour lives next
// to the element that draws it instead of in a central dispatch table.
type clickAction func() tea.Cmd

// rootModel is the top-level tea.Model. It owns the screen stack, the
// global status bar, the SSE subscription, and the command palette.
// The persistent frame it draws around each screen (top nav bar, status
// bar, hint row, banner) lives in chrome.go; the help/overlay machinery
// lives in help_overlay.go.
type rootModel struct {
	ctx    context.Context
	client notoapi.Client
	keys   keys.Map
	styles theme.Styles

	width  int
	height int

	// sidebarWidth is the persisted left-pane width (cells), shared across the
	// dashboard/people/config two-pane layouts. Root owns it because dragging
	// the divider rides on mouse motion, which root handles itself (it never
	// reaches screens). Seeded from config on startup; persisted on drag-release
	// and on the keyboard resize bindings. 0 until the config load returns, at
	// which point it takes the stored (or default) value.
	sidebarWidth int

	// stack[0] is dashboard; pushing pushes detail/transcript/etc.
	stack []screen

	statusBar notoapi.StatusBar
	banner    *bannerMsg

	palette  *palette
	helpOpen bool

	recordingActive  bool
	recordingElapsed int

	// frameHits holds the clickable regions of the most recently rendered
	// frame, in absolute screen coordinates. content() rebuilds it every
	// render; Update queries it on the next mouse event. nil until the first
	// frame is drawn (queries are nil-safe).
	frameHits *hit.Map[region]

	// hovered is the id of the region under the pointer, resolved on the last
	// motion event against the displayed frame. Rendering reads it to draw
	// that element in its hover state; "" means nothing is hovered.
	hovered string

	// pressed is the id of the region with the mouse button currently held
	// down on it (armed on press, cleared on release). The focused element
	// renders its pressed look while it equals this; the action fires on
	// release. "" means none.
	pressed string

	// viewCache is the last frame content() produced; dirty says whether the
	// model has changed since, so View can return the cache untouched when it
	// hasn't. This matters because AllMotion mouse tracking delivers a motion
	// event per cell the pointer crosses, and the runtime calls View after
	// every event — without the cache, a resting or sweeping pointer would
	// rebuild the whole frame (a ~1379-alloc, ~hundreds-of-µs job) hundreds of
	// times a second. Every state-changing path in Update sets dirty; the
	// motion handler sets it only when the hovered element actually changes.
	viewCache string
	dirty     bool
}

func newRootModel(ctx context.Context, client notoapi.Client) tea.Model {
	r := &rootModel{
		ctx:          ctx,
		client:       client,
		keys:         keys.New(),
		styles:       theme.NewStyles(),
		width:        100,
		height:       30,
		sidebarWidth: defaultSidebarW,
	}
	r.stack = []screen{newDashboardScreen()}
	return r
}

// Init kicks off background tasks: subscribe to events, prime status bar,
// run the active screen's enter().
func (m *rootModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		subscribeEvents(m.ctx, m.client),
		// Seed the shared sidebar width from the persisted config; the result
		// arrives as configLoadedMsg (handled below) and replaces the default.
		fetchConfig(m.screenCtx()),
		m.top().enter(m.screenCtx(), ""),
	}
	return tea.Batch(cmds...)
}

func (m *rootModel) top() screen {
	if len(m.stack) == 0 {
		return newDashboardScreen()
	}
	return m.stack[len(m.stack)-1]
}

func (m *rootModel) screenCtx() screenCtx {
	return screenCtx{
		ctx:          m.ctx,
		client:       m.client,
		keys:         m.keys,
		styles:       m.styles,
		width:        m.width,
		height:       m.contentHeight(),
		sidebarWidth: m.sidebarWidth,
		hits:         m.frameHits,
		hovered:      m.hovered,
		pressed:      m.pressed,
	}
}

// pointer is the live hover/press state for button.place in chrome rendering.
// (Screens get the same via screenCtx.pointer().)
func (m *rootModel) pointer() pointer {
	return pointer{hovered: m.hovered, pressed: m.pressed}
}

// regionAt resolves the clickable region at a screen cell, applying the one
// rule every pointer handler shares: a modal overlay owns the screen, so
// nothing behind it is hit. nil-safe before the first frame is drawn.
func (m *rootModel) regionAt(mouse tea.Mouse) (region, bool) {
	if m.helpOpen || m.palette != nil {
		return region{}, false
	}
	return m.frameHits.At(mouse.X, mouse.Y)
}

func (m *rootModel) contentHeight() int {
	// Reserve 3 rows of chrome (header + hint + status); the body takes the
	// rest, never below 5 rows.
	return layout.Split(m.height, 0, layout.Fixed(3), layout.FlexMin(1, 5))[1]
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Mouse motion is the firehose: AllMotion tracking delivers one event per
	// cell the pointer crosses, and nothing about a motion changes the frame
	// except which element is highlighted. Handle it here and return before the
	// rest of Update — no screen reacts to raw motion (the architecture forbids
	// it), so forwarding it would just burn a screen update per cell. Mark the
	// frame dirty only when the hovered element actually changed; an unchanged
	// hover leaves dirty as-is so View serves the cached frame for free.
	if msg, ok := msg.(tea.MouseMotionMsg); ok {
		// Dragging the sidebar divider: while it's the pressed element, motion
		// resizes the sidebar to follow the pointer's column (clamped to the
		// pane floors). This is the one place raw motion drives state — the
		// divider is root-owned precisely because motion never reaches screens.
		if m.pressed == sidebarDividerID {
			if w := m.clampSidebarWidth(msg.Mouse().X); w != m.sidebarWidth {
				m.sidebarWidth = w
				m.dirty = true
			}
			m.hovered = sidebarDividerID
			return m, nil
		}
		prev := m.hovered
		m.hovered = ""
		if r, ok := m.regionAt(msg.Mouse()); ok {
			m.hovered = r.id
		}
		if m.hovered != prev {
			m.dirty = true
		}
		return m, nil
	}

	// The wheel scrolls whatever the pointer is over. Resolve the region under
	// the cursor (the same map clicks use) and hand the active screen a semantic
	// scroll intent tagged with that region id, so a screen scrolls the right
	// sub-area — the detail pane vs the meeting list — without it first having to
	// be focused. Modal overlays own the screen (regionAt returns nothing), so a
	// wheel can't leak to the chrome behind them.
	if msg, ok := msg.(tea.MouseWheelMsg); ok {
		if m.helpOpen || m.palette != nil {
			return m, nil
		}
		over := ""
		if r, ok := m.regionAt(msg.Mouse()); ok {
			over = r.id
		}
		m.dirty = true
		updated, cmd := m.top().update(m.screenCtx(), mouseWheelMsg{
			over: over,
			up:   msg.Mouse().Button == tea.MouseWheelUp,
		})
		m.stack[len(m.stack)-1] = updated
		return m, cmd
	}

	// Every other message may change visible state, so rebuild the frame on the
	// next View. (These are all low-frequency next to motion — keys, events,
	// ticks, clicks — so an occasional redundant rebuild costs nothing.)
	m.dirty = true

	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.MouseClickMsg:
		// Press (button DOWN): arm the element under the pointer. It only
		// *animates* (the pressed colour) when it's the already-focused element
		// — see button.place — so re-clicking the current selection gives
		// feedback while clicking a different element just lets the focus change
		// speak for itself. The action fires on release. No timer.
		if msg.Mouse().Button == tea.MouseLeft {
			if r, ok := m.regionAt(msg.Mouse()); ok {
				m.pressed = r.id
			}
		}

	case tea.MouseReleaseMsg:
		// Release (button UP): run the action if the pointer is still over the
		// element we pressed (drag-off cancels, like a web button), then clear
		// the press so the animation ends exactly as the action runs.
		if msg.Mouse().Button == tea.MouseLeft {
			pressed := m.pressed
			m.pressed = ""
			// Drag-end of the sidebar divider: the width was tracked live on
			// motion; persist it once here so disk is written per drag, not per
			// cell. (The divider has no onClick — it's a drag, not a click.)
			if pressed == sidebarDividerID {
				return m, persistSidebarWidth(m.screenCtx(), m.sidebarWidth)
			}
			if pressed != "" {
				if r, ok := m.regionAt(msg.Mouse()); ok && r.id == pressed && r.onClick != nil {
					return m, r.onClick()
				}
			}
		}

	case tea.KeyPressMsg:
		// Help overlay takes everything.
		if m.helpOpen {
			if isEscapeKey(msg) || key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
				m.helpOpen = false
				return m, nil
			}
			return m, nil
		}
		// Palette is modal — only it sees keys while open.
		if m.palette != nil {
			if isEscapeKey(msg) {
				m.palette = nil
				return m, nil
			}
			updated, cmd := m.palette.update(msg, m.styles)
			m.palette = updated
			return m, cmd
		}

		// If the active screen has a focused text input, the only
		// global keys that fire are non-letter ones: ctrl+c, esc, the
		// palette colon. Everything else routes to the screen so users
		// can type freely into search/filter/title boxes.
		inputActive := m.top().inputActive()

		switch {
		case key.Matches(msg, m.keys.Quit):
			// Quit binding includes ctrl+c. With an input active we
			// keep ctrl+c as a quit signal but let plain "q" pass
			// through to the input.
			if inputActive && msg.String() != "ctrl+c" {
				break
			}
			if m.recordingActive {
				m.banner = &bannerMsg{Kind: "warn", Text: "stop the recording before quitting (s)"}
				return m, nil
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.SidebarWider):
			// Keyboard parity for the divider drag. Non-letter (ctrl+…) so it
			// fires even with an input focused; root-owned like the drag.
			return m, m.resizeSidebar(m.sidebarWidth + sidebarStep)
		case key.Matches(msg, m.keys.SidebarNarrower):
			return m, m.resizeSidebar(m.sidebarWidth - sidebarStep)
		case key.Matches(msg, m.keys.SidebarReset):
			return m, m.resizeSidebar(defaultSidebarW)
		case !inputActive && key.Matches(msg, m.keys.Palette):
			m.palette = newPalette(m.client, m.paletteEntries())
			return m, nil
		case !inputActive && key.Matches(msg, m.keys.Help):
			m.helpOpen = true
			return m, nil
		case key.Matches(msg, m.keys.Back):
			if inputActive {
				break
			}
			if len(m.stack) > 1 {
				m.stack[len(m.stack)-1].leave(m.screenCtx())
				m.stack = m.stack[:len(m.stack)-1]
				return m, m.top().enter(m.screenCtx(), "")
			}
		case !inputActive && screenNavTarget(msg) != "":
			// Number/alias keys select a top-level screen. The mapping is
			// derived from topScreens (see screen.go), so this one branch
			// covers every numbered screen — no per-screen cases to keep
			// in sync. Pressing the key for the screen you're already on
			// is a no-op rather than a rebuild (which would clear a
			// half-typed title or an active search); for the recorder that
			// lets `r` fall through to its own Record handler.
			id := screenNavTarget(msg)
			if m.top().id() == id {
				// Already on this page: re-pressing its nav key doesn't
				// navigate, so give the same feedback re-clicking the focused
				// pill does — briefly flash that pill (button.place shows the
				// pressed look only because this pill is the focused one). Then
				// break so the key still reaches the active screen (e.g. `r` on
				// the recorder records). Toggle keys like `?` never reach here.
				m.pressed = navHitID(id)
				cmds = append(cmds, clearPressCmd(navHitID(id)))
				break
			}
			return m, pushOrReplaceTo(id, "")
		case !inputActive && key.Matches(msg, m.keys.Speakers):
			// Speakers used to be a top-level screen; now it's a tab in
			// the detail pane. The dashboard/detail screens handle `k`
			// themselves to switch the active tab — fall through so the
			// active screen sees the key.
			break
		case !inputActive && key.Matches(msg, m.keys.Search):
			// `/` is no longer a separate screen. Route to the dashboard
			// which owns both list + FTS filter; if we're already
			// there, forward the key so the screen focuses its input.
			if m.top().id() == sDashboard {
				break
			}
			return m, pushOrReplaceTo(sDashboard, "filter")
		}

	case configLoadedMsg:
		// Seed/refresh the shared sidebar width from persisted config. A drag or
		// a resize key issues a PatchConfig whose echoed result also lands here,
		// keeping the value in sync. Falls through so the active screen (config)
		// still receives the message for its own cfg copy.
		if msg.Err == nil && msg.Cfg.UI.SidebarWidth > 0 {
			m.sidebarWidth = msg.Cfg.UI.SidebarWidth
		}

	case eventStreamMsg:
		if msg.Closed {
			// Re-subscribe so the stream survives transient disconnects.
			cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return reSubscribeMsg{}
			}))
		} else {
			switch msg.Event.Kind {
			case notoapi.EventStatusBar:
				if msg.Event.StatusBar != nil {
					m.statusBar = *msg.Event.StatusBar
				}
			case notoapi.EventRecorder:
				if msg.Event.Recorder != nil {
					m.recordingActive = msg.Event.Recorder.Active
					m.recordingElapsed = msg.Event.Recorder.ElapsedSec
				}
			}
			// Resubscribe by issuing another receive command.
			cmds = append(cmds, recvEventCmd())
		}

	case reSubscribeMsg:
		cmds = append(cmds, subscribeEvents(m.ctx, m.client))

	case bannerMsg:
		m.banner = &msg
		cmds = append(cmds, tea.Tick(4*time.Second, func(time.Time) tea.Msg {
			return clearBannerMsg{}
		}))

	case clearBannerMsg:
		m.banner = nil

	case clearPressMsg:
		// End a keyboard press flash, unless a newer press took its place.
		if m.pressed == msg.id {
			m.pressed = ""
		}

	case switchScreenMsg:
		s := buildScreen(msg.ID)
		if len(m.stack) > 0 {
			m.stack[len(m.stack)-1].leave(m.screenCtx())
		}
		m.stack = []screen{s}
		cmds = append(cmds, s.enter(m.screenCtx(), msg.Param))

	case pushScreenMsg:
		s := buildScreen(msg.ID)
		m.stack = append(m.stack, s)
		cmds = append(cmds, s.enter(m.screenCtx(), msg.Param))

	case popScreenMsg:
		if len(m.stack) > 1 {
			m.stack[len(m.stack)-1].leave(m.screenCtx())
			m.stack = m.stack[:len(m.stack)-1]
		}

	case commandMsg:
		m.palette = nil
		switch msg.Action {
		case "goto":
			cmds = append(cmds, pushOrReplaceTo(screenID(msg.Param), ""))
		case "open-meeting", "open-speakers", "open-transcript":
			// All "open the meeting" verbs route to the dashboard and
			// let the embedded detail pane handle which tab to show.
			// The dashboard's enter() honors a "<id>" or "speakers:<id>"
			// / "transcript:<id>" param.
			cmds = append(cmds, pushOrReplaceTo(sDashboard, paneParamFor(msg.Action, msg.Param)))
		case "open-agent":
			cmds = append(cmds, push(sAgent, msg.Param))
		case "quit":
			return m, tea.Quit
		}

	case quitMsg:
		return m, tea.Quit
	}

	// Forward to active screen.
	updated, cmd := m.top().update(m.screenCtx(), msg)
	m.stack[len(m.stack)-1] = updated
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *rootModel) View() tea.View {
	// v2 moves terminal feature flags from program options onto the View,
	// and the renderer diffs them every frame — so they must be set on EVERY
	// path View returns. We set them here, once, and let content() decide
	// only *what* to draw. If a return path left these unset (as the
	// too-small-terminal fallback used to), Bubble Tea would leave the
	// alternate screen and disable the mouse on that frame, dumping the
	// fallback into the user's scrollback until the next normal-sized frame.
	// Serve the cached frame when nothing has changed since it was built. The
	// runtime calls View after every message, but most messages (above all the
	// AllMotion firehose) leave the frame identical; rebuilding it then would
	// re-render every screen, chip and hit region for no visible change. dirty
	// is set by every state-changing path in Update; an empty cache forces the
	// first build.
	content := m.viewCache
	if m.dirty || content == "" {
		content = m.content()
		m.viewCache = content
		m.dirty = false
	}
	v := tea.NewView(content)
	v.AltScreen = true
	// Paint our own canvas so the theme looks identical across terminals.
	// Without this the UI is drawn as foreground colors over whatever each
	// terminal's default background happens to be (VS Code vs Ghostty vs …),
	// which is why the same screen reads differently between them.
	v.BackgroundColor = m.styles.T.Background
	// All-motion (not just cell-motion) so we receive pointer movement with
	// no button held — that's what drives hover highlighting.
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// renderTooSmall draws the guard shown when the terminal is below the absolute
// floor. It always reports BOTH dimensions — the current size and the required
// minimum — so the user knows exactly which way (and how far) to resize, rather
// than guessing from a generic "too small" line.
func (m *rootModel) renderTooSmall() string {
	s := m.styles
	cur := fmt.Sprintf("%d×%d", m.width, m.height)
	need := fmt.Sprintf("%d×%d", minTermW, minTermH)
	return s.Danger.Render("Terminal too small: "+cur) + "\n" +
		s.Muted.Render("noto needs at least "+need+" (width×height) — enlarge the window.") + "\n"
}

// content renders the frame body. It returns only the string to draw; the
// terminal feature flags are owned by View so they can't drift between the
// normal and fallback paths.
func (m *rootModel) content() string {
	// Rebuild the click map for this frame; rendering registers regions into
	// it (chrome here, screen body as views adopt hit.Row). The too-small
	// fallback below draws nothing clickable, so it leaves the map empty.
	m.frameHits = &hit.Map[region]{}

	if m.width < minTermW || m.height < minTermH {
		return m.renderTooSmall()
	}
	header := m.renderHeader()
	// The body is drawn directly under the header; tell the screen where that
	// is so its hitRow can translate body-local rows into absolute click cells.
	ctx := m.screenCtx()
	ctx.bodyTop = lipgloss.Height(header)
	body := m.top().view(ctx)
	status := m.renderStatusBar()

	// The hint row sits directly under the body. Register its clickable chips on
	// that absolute frame row — but only when it's actually shown; a banner
	// replaces it, and then nothing there is clickable.
	var row string
	if m.banner != nil {
		row = m.renderBanner()
	} else {
		row = m.renderHintBar(m.frameHits, lipgloss.Height(header)+lipgloss.Height(body))
	}

	content := lipgloss.JoinVertical(lipgloss.Left, header, body, row, status)
	if m.helpOpen {
		content = m.renderHelpOverlay(content)
	}
	if m.palette != nil {
		content = m.palette.view(m.width, m.height, m.styles, content)
	}
	return content
}

// --- helpers ---

func isEscapeKey(msg tea.KeyPressMsg) bool {
	return msg.Code == tea.KeyEscape
}

// --- screen factory ---

func buildScreen(id screenID) screen {
	// Numbered screens come from the registry (see screen.go).
	for _, sc := range topScreens {
		if sc.id == id {
			return sc.new()
		}
	}
	// Screens reachable only contextually (not by number) live here.
	switch id {
	case sAgent:
		return newAgentScreen()
	default:
		return newDashboardScreen()
	}
}

// persistSidebarWidth writes the shared sidebar width to config. The echoed
// Config comes back as configLoadedMsg so the value (and the config screen's
// view) stay in sync; PatchConfig only sets SidebarWidth when > 0, so this
// never disturbs the theme.
func persistSidebarWidth(ctx screenCtx, width int) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		cfg, err := ctx.client.PatchConfig(c, notoapi.ConfigPatch{
			UI: &notoapi.ConfigUI{SidebarWidth: width},
		})
		return configLoadedMsg{Cfg: cfg, Err: err}
	}
}

// resizeSidebar sets the sidebar to the requested width (clamped to the pane
// floors) and returns the command that persists it. Used by the keyboard
// resize bindings; the mouse drag persists on release the same way. A no-op
// when the clamp leaves the width unchanged (already at a floor).
func (m *rootModel) resizeSidebar(want int) tea.Cmd {
	w := m.clampSidebarWidth(want)
	if w == m.sidebarWidth {
		return nil
	}
	m.sidebarWidth = w
	return persistSidebarWidth(m.screenCtx(), w)
}

// clampSidebarWidth pins the sidebar preference into the range the current
// terminal allows: at least minSidebarW, and small enough to leave the content
// pane minContentW cells. Mirrors layout.SidebarSplit's clamp so the stored
// value matches what's rendered.
func (m *rootModel) clampSidebarWidth(w int) int {
	max := m.width - 1 - minContentW
	if max < minSidebarW {
		max = minSidebarW
	}
	if w < minSidebarW {
		w = minSidebarW
	}
	if w > max {
		w = max
	}
	return w
}

func pushOrReplaceTo(id screenID, param string) tea.Cmd {
	return func() tea.Msg {
		return switchScreenMsg{ID: id, Param: param}
	}
}

func push(id screenID, param string) tea.Cmd {
	return func() tea.Msg {
		return pushScreenMsg{ID: id, Param: param}
	}
}

// paneParamFor maps palette actions to the dashboard's enter() param
// convention: "speakers:<id>" / "transcript:<id>" / "<id>".
func paneParamFor(action, id string) string {
	switch action {
	case "open-speakers":
		return "speakers:" + id
	case "open-transcript":
		return "transcript:" + id
	default:
		return "meeting:" + id
	}
}

// reSubscribeMsg / clearBannerMsg are private to this file.
type reSubscribeMsg struct{}
type clearBannerMsg struct{}

// clearPressMsg ends a keyboard-triggered press flash for the region it names;
// the id guards against a newer press being cleared by an older timer. Mouse
// presses clear on button release instead, so they need no timer — this is
// only for the keyboard, which has no "release" to key off.
type clearPressMsg struct{ id string }

// pressFlash is how long re-pressing the current page's hotkey flashes its nav
// pill. A var (not const) only so tests can shrink it.
var pressFlash = 120 * time.Millisecond

func clearPressCmd(id string) tea.Cmd {
	return tea.Tick(pressFlash, func(time.Time) tea.Msg { return clearPressMsg{id} })
}

// activeMeetingID peeks at the screen stack for a screen that has a
// meeting context, so global keys like "s" (speakers) can apply to it.
func (m *rootModel) activeMeetingID() string {
	for i := len(m.stack) - 1; i >= 0; i-- {
		if mid, ok := m.stack[i].(interface{ meetingID() string }); ok {
			if id := mid.meetingID(); id != "" {
				return id
			}
		}
	}
	return ""
}

// --- command palette entries ---

func (m *rootModel) paletteEntries() []paletteEntry {
	k := m.keys
	// One palette entry per numbered screen, derived from the registry so
	// the menu and its hint keys can never drift from the nav bindings.
	out := []paletteEntry{}
	for i, sc := range topScreens {
		label := sc.palette
		if label == "" {
			label = sc.title
		}
		out = append(out, paletteEntry{Label: label, Action: "goto", Param: string(sc.id), Hint: sc.navBinding(i).Help().Key})
	}
	out = append(out, paletteEntry{Label: "Search meetings", Action: "goto", Param: string(sDashboard), Hint: k.Search.Help().Key})
	if id := m.activeMeetingID(); id != "" {
		out = append(out,
			paletteEntry{Label: "Speakers for current meeting", Action: "open-speakers", Param: id, Hint: k.Speakers.Help().Key},
			paletteEntry{Label: "Transcript for current meeting", Action: "open-transcript", Param: id, Hint: k.Transcript.Help().Key},
			paletteEntry{Label: "Agent handoff for current meeting", Action: "open-agent", Param: id, Hint: k.OpenAgent.Help().Key},
		)
	}
	out = append(out, paletteEntry{Label: "Quit noto", Action: "quit", Hint: k.Quit.Help().Key})
	return out
}

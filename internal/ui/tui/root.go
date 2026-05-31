package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// rootModel is the top-level tea.Model. It owns the screen stack, the
// global status bar, the SSE subscription, and the command palette.
type rootModel struct {
	ctx    context.Context
	client notoapi.Client
	keys   keys.Map
	styles theme.Styles

	width  int
	height int

	// stack[0] is dashboard; pushing pushes detail/transcript/etc.
	stack []screen

	statusBar notoapi.StatusBar
	banner    *bannerMsg

	palette  *palette
	helpOpen bool

	recordingActive  bool
	recordingElapsed int
}

func newRootModel(ctx context.Context, client notoapi.Client) tea.Model {
	r := &rootModel{
		ctx:    ctx,
		client: client,
		keys:   keys.New(),
		styles: theme.NewStyles(),
		width:  100,
		height: 30,
	}
	r.stack = []screen{newDashboardScreen()}
	return r
}

// Init kicks off background tasks: subscribe to events, prime status bar,
// run the active screen's enter().
func (m *rootModel) Init() tea.Cmd {
	cmds := []tea.Cmd{
		subscribeEvents(m.ctx, m.client),
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
		ctx:    m.ctx,
		client: m.client,
		keys:   m.keys,
		styles: m.styles,
		width:  m.width,
		height: m.contentHeight(),
	}
}

func (m *rootModel) contentHeight() int {
	// Reserve 3 rows of chrome (header + hint + status); the body takes the
	// rest, never below 5 rows.
	return layout.Split(m.height, 0, layout.Fixed(3), layout.FlexMin(1, 5))[1]
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

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
	v := tea.NewView(m.content())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// content renders the frame body. It returns only the string to draw; the
// terminal feature flags are owned by View so they can't drift between the
// normal and fallback paths.
func (m *rootModel) content() string {
	if m.width < 30 || m.height < 10 {
		return m.styles.Muted.Render("noto needs a wider terminal\n")
	}
	header := m.renderHeader()
	body := m.top().view(m.screenCtx())
	hint := m.renderHintBar()
	status := m.renderStatusBar()

	// Banner overrides the hint row when active.
	row := hint
	if m.banner != nil {
		row = m.renderBanner()
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

func (m *rootModel) renderHeader() string {
	s := m.styles
	logo := s.Header.Render("◉ noto")
	bread := strings.Builder{}
	for i, sc := range m.stack {
		if i > 0 {
			bread.WriteString(s.Muted.Render(" › "))
		}
		if i == len(m.stack)-1 {
			bread.WriteString(s.HeaderEm.Render(string(sc.id())))
		} else {
			bread.WriteString(s.Muted.Render(string(sc.id())))
		}
	}
	left := logo + s.Muted.Render("  ·  ") + bread.String()
	right := ""
	if m.recordingActive {
		right = s.Recording.Render(fmt.Sprintf("● REC %s", formatDuration(m.recordingElapsed)))
	}
	gap := max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2)
	return left + strings.Repeat(" ", gap) + right
}

func (m *rootModel) renderStatusBar() string {
	s := m.styles
	parts := []string{}
	if m.recordingActive {
		parts = append(parts, s.Recording.Render("● REC")+" "+s.HeaderEm.Render(formatDuration(m.recordingElapsed)))
	} else {
		parts = append(parts, s.Muted.Render("○ idle"))
	}
	parts = append(parts, s.Muted.Render(fmt.Sprintf("⦿ %d meetings", m.statusBar.MeetingCount)))
	idxLabel := def(m.statusBar.IndexState, "clean")
	switch idxLabel {
	case "clean":
		parts = append(parts, s.Success.Render("✓ index "+idxLabel))
	case "indexing":
		parts = append(parts, s.Info.Render("⟳ index "+idxLabel))
	default:
		parts = append(parts, s.Warning.Render("△ index "+idxLabel))
	}
	switch {
	case m.statusBar.JobsRunning > 0:
		parts = append(parts, s.Info.Render(fmt.Sprintf("⚙ %d running", m.statusBar.JobsRunning)))
	case m.statusBar.JobsQueued > 0:
		parts = append(parts, s.Muted.Render(fmt.Sprintf("⌛ %d queued", m.statusBar.JobsQueued)))
	default:
		parts = append(parts, s.Muted.Render("⚙ jobs idle"))
	}
	left := strings.Join(parts, s.Muted.Render("  ·  "))
	return s.StatusBar.Render(left)
}

func (m *rootModel) renderHintBar() string {
	// Hints are screen-local: each screen exposes its own action chips
	// (via the rendered body). The global hint row keeps just the
	// universal modes the user needs everywhere.
	hints := []string{
		chip(m.styles, m.keys.Palette),
		chip(m.styles, m.keys.Search),
		chip(m.styles, m.keys.Help),
		chip(m.styles, m.keys.Back),
		chip(m.styles, m.keys.Quit),
	}
	return m.styles.HintBar.Render(strings.Join(hints, "   "))
}

func hint(s theme.Styles, k, label string) string {
	return s.ChipKey.Render(k) + " " + s.Hint.Render(label)
}

// chip renders a key binding as a hint using the binding's OWN help text
// (key + description). This is the single path the UI uses to show a key,
// so the label can never drift from what's actually bound — change the
// key in keys.New() and every chip follows.
func chip(s theme.Styles, b key.Binding) string {
	h := b.Help()
	return hint(s, h.Key, h.Desc)
}

// chipAs renders a binding's key with a context-specific label, for spots
// where the generic description doesn't fit (e.g. Tab means "focus
// details" on the dashboard but "switch pane" in config).
func chipAs(s theme.Styles, b key.Binding, label string) string {
	return hint(s, b.Help().Key, label)
}

// chipPair renders two bindings as one "a/b label" chip, for paired
// movement keys (↑/↓, ←/→) shown as a single hint.
func chipPair(s theme.Styles, a, b key.Binding, label string) string {
	return hint(s, a.Help().Key+"/"+b.Help().Key, label)
}

func (m *rootModel) renderBanner() string {
	if m.banner == nil {
		return ""
	}
	s := m.styles
	var style = s.BadgeInfo
	switch m.banner.Kind {
	case "warn":
		style = s.BadgeWarn
	case "error":
		style = s.BadgeDanger
	}
	return style.Render(m.banner.Text)
}

// renderHelpOverlay is built entirely from the central key map: every
// key shown is read from its binding via chip(), so this overlay can
// never disagree with what the screens actually handle.
func (m *rootModel) renderHelpOverlay(under string) string {
	s := m.styles
	k := m.keys

	// Every span inside the box is rendered through a style that carries
	// the overlay's Surface background. lipgloss resets the background to
	// the terminal default at the end of each styled span, so any cell not
	// explicitly backed — including the plain spaces between chips — would
	// otherwise show through as black. Backing keys, descriptions, titles,
	// AND the separators/indents keeps the panel one continuous surface.
	// (Same Surface color as before; no palette change — just applied to
	// the styled spans so they blend instead of punching black holes.)
	surf := s.T.Surface
	bg := lipgloss.NewStyle().Background(surf)
	keyS := s.ChipKey.Background(surf) // bright body text for labels, so the
	descS := s.Row.Background(surf)    // overlay reads clearly on the dark surface
	titleS := s.PanelTitle.Background(surf)
	headS := s.HeaderEm.Background(surf)
	mutedS := s.Muted.Background(surf)

	sep := bg.Render("   ")
	gap := bg.Render(" ")
	row := func(chips ...string) string { return bg.Render("  ") + strings.Join(chips, sep) }
	hc := func(b key.Binding) string {
		h := b.Help()
		return keyS.Render(h.Key) + gap + descS.Render(h.Desc)
	}
	hcAs := func(b key.Binding, label string) string {
		return keyS.Render(b.Help().Key) + gap + descS.Render(label)
	}
	hcPair := func(a, b key.Binding, label string) string {
		return keyS.Render(a.Help().Key+"/"+b.Help().Key) + gap + descS.Render(label)
	}

	navChips := make([]string, 0, len(topScreens))
	for _, b := range screenNavBindings() {
		navChips = append(navChips, hc(b))
	}

	lines := []string{
		headS.Render("noto — keys"),
		"",
		titleS.Render("Global"),
		row(navChips...),
		row(hc(k.Search), hc(k.Palette), hc(k.Help), hc(k.Back), hc(k.Quit)),
		"",
		titleS.Render("Meetings list"),
		row(hcPair(k.Up, k.Down, "select"), hcAs(k.Enter, "focus details"), hcAs(k.Tab, "focus details")),
		row(hc(k.OpenAgent), hc(k.Delete), hc(k.ClearSearch)),
		"",
		titleS.Render("Search (/ focused)"),
		row(hcAs(k.Tab, "next match"), hcAs(k.ShiftTab, "prev match"), hcAs(k.Enter, "open at hit"), hcAs(k.Back, "to list")),
		"",
		titleS.Render("Details pane"),
		row(hcPair(k.TabPrev, k.TabNext, "switch tab"), hc(k.Transcript), hc(k.Speakers), hc(k.OpenAgent)),
		row(hcPair(k.Up, k.Down, "scroll/select"), hc(k.NextMatch), hc(k.PrevMatch), hcAs(k.Edit, "rename speaker")),
		"",
		titleS.Render("Recorder"),
		row(hc(k.EditTitle), hc(k.Record), hc(k.Stop), hc(k.Marker)),
		"",
		titleS.Render("Config"),
		row(hcAs(k.Tab, "switch pane"), hcPair(k.Up, k.Down, "move"), hcAs(k.Enter, "set route/key"), hc(k.Test), hc(k.Remove)),
		"",
		mutedS.Render("press ? or esc to close"),
	}
	box := s.OverlayBox.Width(min(m.width-6, 78)).Render(strings.Join(lines, "\n"))
	return overlayCenter(under, box, m.width, m.height)
}

func formatDuration(seconds int) string {
	h := seconds / 3600
	rem := seconds % 3600
	mn := rem / 60
	sc := rem % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mn, sc)
	}
	return fmt.Sprintf("%02d:%02d", mn, sc)
}

func def(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// --- helpers ---

func isEscapeKey(msg tea.KeyPressMsg) bool {
	return msg.Code == tea.KeyEscape
}

// overlayCenter composites a pre-styled box centered over a
// terminal-sized view. The background is dimmed (ANSI-stripped, then
// re-rendered faint) so the box reads as the foreground; the box keeps
// its own border/colors.
//
// lipgloss v2 does the compositing: a Compositor positions the box layer
// at (left, top) on top of the full-size background layer, drawn onto an
// explicit w×h Canvas. (Canvas.Compose alone ignores a layer's X/Y — only
// a Compositor applies per-layer offsets — so the box goes through one.)
func overlayCenter(under, box string, w, h int) string {
	bw := lipgloss.Width(box)
	bh := lipgloss.Height(box)
	if bw > w {
		bw = w
	}
	if bh > h {
		bh = h
	}
	left := max(0, (w-bw)/2)
	top := max(0, (h-bh)/2)

	dimStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("#475569"))
	dimmed := dimStyle.Render(ansi.Strip(under))

	return lipgloss.NewCanvas(w, h).
		Compose(lipgloss.NewCompositor(
			lipgloss.NewLayer(dimmed),                  // z=0: dimmed background
			lipgloss.NewLayer(box).X(left).Y(top).Z(1), // z=1: centered box on top
		)).
		Render()
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

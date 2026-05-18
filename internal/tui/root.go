package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// Avoid unused 'pop' warning if no screen calls it.
var _ = pop

// rootModel is the top-level tea.Model. It owns the screen stack, the
// global status bar, the SSE subscription, and the command palette.
type rootModel struct {
	ctx     context.Context
	client  notoapi.Client
	keys    keys.Map
	styles  theme.Styles

	width  int
	height int

	// stack[0] is dashboard; pushing pushes detail/transcript/etc.
	stack []screen

	statusBar notoapi.StatusBar
	banner    *bannerMsg

	palette  *palette
	helpOpen bool

	recordingActive bool
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
	// Reserve 1 row header + 1 row status bar + 1 row hint bar.
	h := m.height - 3
	if h < 5 {
		h = 5
	}
	return h
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		// Help overlay takes everything.
		if m.helpOpen {
			if key.Matches(msg, m.keys.Help) || key.Matches(msg, m.keys.Back) || key.Matches(msg, m.keys.Quit) {
				m.helpOpen = false
				return m, nil
			}
			return m, nil
		}
		// Palette is modal — only it sees keys while open.
		if m.palette != nil {
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
		case !inputActive && key.Matches(msg, m.keys.GoDashboard):
			return m, pushOrReplaceTo(sDashboard, "")
		case !inputActive && key.Matches(msg, m.keys.GoMeetings):
			return m, pushOrReplaceTo(sMeetings, "")
		case !inputActive && key.Matches(msg, m.keys.GoRecord):
			return m, pushOrReplaceTo(sRecorder, "")
		case !inputActive && key.Matches(msg, m.keys.GoProviders):
			return m, pushOrReplaceTo(sProviders, "")
		case !inputActive && key.Matches(msg, m.keys.GoStorage):
			return m, pushOrReplaceTo(sStorage, "")
		case !inputActive && key.Matches(msg, m.keys.GoSettings):
			return m, pushOrReplaceTo(sSettings, "")
		case !inputActive && key.Matches(msg, m.keys.GoSpeakers):
			if id := m.activeMeetingID(); id != "" {
				return m, push(sSpeakers, id)
			}
		case !inputActive && key.Matches(msg, m.keys.Search):
			return m, pushOrReplaceTo(sSearch, "")
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
		case "open-meeting":
			cmds = append(cmds, push(sDetail, msg.Param))
		case "open-speakers":
			cmds = append(cmds, push(sSpeakers, msg.Param))
		case "open-transcript":
			cmds = append(cmds, push(sTranscript, msg.Param))
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

func (m *rootModel) View() string {
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

	view := lipgloss.JoinVertical(lipgloss.Left, header, body, row, status)
	if m.helpOpen {
		view = m.renderHelpOverlay(view)
	}
	if m.palette != nil {
		view = m.palette.view(m.width, m.height, m.styles, view)
	}
	return view
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
		hint(m.styles, ":", "menu"),
		hint(m.styles, "/", "search"),
		hint(m.styles, "?", "help"),
		hint(m.styles, "esc", "back"),
		hint(m.styles, "q", "quit"),
	}
	return m.styles.HintBar.Render(strings.Join(hints, "   "))
}

func hint(s theme.Styles, k, label string) string {
	return s.ChipKey.Render(k) + " " + s.Hint.Render(label)
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

func (m *rootModel) renderHelpOverlay(under string) string {
	s := m.styles
	lines := []string{
		s.HeaderEm.Render("noto — keys"),
		"",
		s.PanelTitle.Render("Navigate"),
		"  :   command menu (everywhere)",
		"  /   search transcripts",
		"  1   dashboard",
		"  2   meetings",
		"  r   recorder",
		"  4   config (providers, routing, API keys)",
		"  5   storage",
		"  ,   config (same as 4)",
		"  esc back · tab next pane",
		"",
		s.PanelTitle.Render("Recorder"),
		"  i   edit title",
		"  r   start recording",
		"  s   stop",
		"  n   add marker",
		"",
		s.PanelTitle.Render("Meeting"),
		"  ⏎   open detail",
		"  t   open transcript",
		"  k   open speakers",
		"  a   agent handoff",
		"  v   verify checksums",
		"  d   delete",
		"  c   copy citation · p   copy path",
		"",
		s.Muted.Render("press ? or esc to close"),
	}
	box := s.OverlayBox.Width(min(m.width-6, 70)).Render(strings.Join(lines, "\n"))
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

// overlayCenter composites a box over a terminal-sized view by:
//   - dimming the underlying view (ANSI-strip + faint muted re-render)
//   - centering the box and pasting its rows over the dimmed under
//     such that any cells outside the box keep the dimmed under text
//
// The box itself comes pre-styled (with its own border, colors, etc.)
// and is rendered as-is so its content stays readable on top of the
// muted background.
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
	boxLines := strings.Split(box, "\n")
	underLines := strings.Split(under, "\n")

	out := make([]string, len(underLines))
	for i, line := range underLines {
		stripped := ansi.Strip(line)
		// Pad to terminal width so cell math works.
		if cw := lipgloss.Width(stripped); cw < w {
			stripped += strings.Repeat(" ", w-cw)
		}
		dimmed := dimStyle.Render(stripped)
		if i < top || i >= top+bh {
			out[i] = dimmed
			continue
		}
		// Box row: keep dimmed under on the sides, box content in the
		// middle. ansi.Cut takes a cell range over a string that may
		// contain ANSI.
		boxRow := boxLines[i-top]
		leftPart := dimStyle.Render(ansi.Cut(stripped, 0, left))
		rightStart := left + bw
		rightPart := dimStyle.Render(ansi.Cut(stripped, rightStart, w))
		out[i] = leftPart + boxRow + rightPart
	}
	return strings.Join(out, "\n")
}

// --- screen factory ---

func buildScreen(id screenID) screen {
	switch id {
	case sDashboard:
		return newDashboardScreen()
	case sMeetings:
		return newMeetingsScreen()
	case sDetail:
		return newDetailScreen()
	case sTranscript:
		return newTranscriptScreen()
	case sSearch:
		return newSearchScreen()
	case sRecorder:
		return newRecorderScreen()
	case sProviders, sSettings, sConfig:
		return newConfigScreen()
	case sStorage:
		return newStorageScreen()
	case sAgent:
		return newAgentScreen()
	case sSpeakers:
		return newSpeakersScreen()
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

// pop emits a popScreenMsg.
func pop() tea.Cmd {
	return func() tea.Msg { return popScreenMsg{} }
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
	out := []paletteEntry{
		{Label: "Dashboard", Action: "goto", Param: string(sDashboard), Hint: "1"},
		{Label: "Meetings", Action: "goto", Param: string(sMeetings), Hint: "2"},
		{Label: "Recorder", Action: "goto", Param: string(sRecorder), Hint: "r"},
		{Label: "Search transcripts", Action: "goto", Param: string(sSearch), Hint: "/"},
		{Label: "Config (active provider, paths, API keys)", Action: "goto", Param: string(sConfig), Hint: ",  or  4"},
		{Label: "Storage health", Action: "goto", Param: string(sStorage), Hint: "5"},
	}
	if id := m.activeMeetingID(); id != "" {
		out = append(out,
			paletteEntry{Label: "Speakers for current meeting", Action: "open-speakers", Param: id, Hint: "k"},
			paletteEntry{Label: "Transcript for current meeting", Action: "open-transcript", Param: id, Hint: "t"},
			paletteEntry{Label: "Agent handoff for current meeting", Action: "open-agent", Param: id, Hint: "a"},
		)
	}
	out = append(out, paletteEntry{Label: "Quit noto", Action: "quit", Hint: "q"})
	return out
}

func max(a, b int) int { if a > b { return a }; return b }
func min(a, b int) int { if a < b { return a }; return b }

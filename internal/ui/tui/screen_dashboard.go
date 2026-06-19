package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/scroll"
)

// dashboardScreen is the unified home: meetings list + search on the
// left, detail pane on the right, with a dynamic-height bottom strip
// (jobs + active recording) attached to the bottom of the left column
// when there's anything to show.
//
// Layout (wide):
//
//	┌── meetings + search (1/3) ──┬── detail (2/3) ──────────────┐
//	│ /filter                     │ title · date · status        │
//	│ ▸ row 1                     │ attendees: …                 │
//	│   row 2                     │ [Summary | Actions | …]      │
//	│   row 3                     │ active tab body              │
//	│                             │                              │
//	├── jobs ────────┬── rec ─────┤                              │
//	│ transcribe ... │ ● REC ...  │                              │
//	└─────────────────┴──────────┴──────────────────────────────┘
//
// The bottom strip is hidden entirely when there are no jobs and no
// recording, so the meetings list takes the full left column when
// nothing else is going on.
//
// `/` focuses the filter input. Typing fires an FTS search; empty
// query falls back to "recent first" listing. n/N jumps the active
// transcript tab to the next/previous segment that matched.
type dashboardScreen struct {
	loading bool
	err     error

	// Default listing — populated by ListMeetings.
	all []notoapi.Meeting
	// FTS-driven view — populated by Search.
	matched []notoapi.MeetingHits

	input                   textinput.Model
	inputFocus              bool
	query                   string
	lastQuery               string
	preserveSearchSelection bool

	cursor int
	// listScroll is the top visible LINE of the meeting list — the view's own
	// scroll position, kept SEPARATE from the cursor. The wheel moves this and
	// nothing else (scroll ≠ select); arrow keys move the cursor and then nudge
	// listScroll just enough to keep the selection visible (followCursor).
	listScroll int

	// Detail pane embedded as the right column. There is no separate
	// detail screen — Enter shifts focus into the pane instead of
	// pushing a new screen.
	pane     *detailPane
	paneOpen bool // pane has focus: arrows/h/l/n/N drive it, Tab/Esc returns to list

	// Pending pane param from enter() (e.g. "speakers:<id>"). Applied
	// after the meetings list loads so we can resolve <id> to a cursor.
	pendingPaneTab detailTab
	pendingFocus   bool
	pendingMeeting string

	// Bottom strip state.
	jobs []notoapi.Job
	rec  notoapi.RecordingState
}

func newDashboardScreen() screen {
	ti := textinput.New()
	ti.Placeholder = "search…"
	ti.CharLimit = 200
	return &dashboardScreen{loading: true, input: ti, pane: newDetailPane()}
}

func (m *dashboardScreen) id() screenID  { return sDashboard }
func (m *dashboardScreen) title() string { return "dashboard" }
func (m *dashboardScreen) inputActive() bool {
	return m.inputFocus || m.pane.inputActive()
}

func (m *dashboardScreen) meetingID() string {
	return m.currentMeetingID()
}

func (m *dashboardScreen) enter(ctx screenCtx, param string) tea.Cmd {
	// param convention:
	//   ""                  — no preload
	//   "filter"            — focus the filter input on entry
	//   "meeting:<id>"      — preselect meeting, focus pane on Summary tab
	//   "speakers:<id>"     — preselect meeting, focus pane on Speakers tab
	//   "transcript:<id>"   — preselect meeting, focus pane on Transcript tab
	//   anything else       — treat as a pre-filled query
	switch {
	case param == "":
	case param == "filter":
		m.inputFocus = true
		m.input.Focus()
	case strings.HasPrefix(param, "meeting:"),
		strings.HasPrefix(param, "speakers:"),
		strings.HasPrefix(param, "transcript:"):
		colon := strings.IndexByte(param, ':')
		kind, id := param[:colon], param[colon+1:]
		m.pendingMeeting = id
		m.pendingFocus = true
		switch kind {
		case "speakers":
			m.pendingPaneTab = tabSpeakers
		case "transcript":
			m.pendingPaneTab = tabTranscript
		default:
			m.pendingPaneTab = tabSummary
		}
	default:
		m.input.SetValue(param)
		m.query = strings.TrimSpace(param)
		m.lastQuery = m.query
		m.inputFocus = true
		m.input.Focus()
	}
	return tea.Batch(
		fetchMeetings(ctx),
		fetchJobs(ctx),
		fetchRecording(ctx),
	)
}

// applyPending resolves a pending pane-focus request once the meetings
// list arrives. Idempotent — called from meetingsLoadedMsg.
func (m *dashboardScreen) applyPending() {
	if m.pendingMeeting == "" {
		return
	}
	for i, mt := range m.all {
		if mt.ID == m.pendingMeeting {
			m.cursor = i
			break
		}
	}
	if m.pendingFocus {
		m.paneOpen = true
		m.pane.tab = m.pendingPaneTab
	}
	m.pendingMeeting = ""
	m.pendingFocus = false
}

func (m *dashboardScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (m *dashboardScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {

	case meetingsLoadedMsg:
		m.loading = false
		m.err = v.Err
		m.all = v.Result.Meetings
		m.clampCursor()
		m.applyPending()
		return m, m.loadSelectedCmd(ctx)

	case searchResultMsg:
		// Discard stale results — query may have moved on.
		if v.Query == m.query {
			keepID := ""
			if m.preserveSearchSelection {
				keepID = m.currentMeetingID()
			}
			m.preserveSearchSelection = false
			m.err = v.Err
			m.matched = v.Result.Meetings
			m.selectMeetingID(keepID)
			m.clampCursor()
			return m, m.loadSelectedCmd(ctx)
		}
		return m, nil

	case jobsLoadedMsg:
		m.jobs = v.Jobs
		return m, nil

	case recordingStateMsg:
		m.rec = v.State
		return m, nil

	case eventStreamMsg:
		// Job state changes drive both the bottom strip and the
		// meetings list (status badge transitions when transcription
		// or summarization completes).
		if v.Event.Kind == notoapi.EventJob && v.Event.Job != nil {
			switch v.Event.Job.Status {
			case notoapi.JobSucceeded, notoapi.JobFailed, notoapi.JobCanceled, notoapi.JobInterrupted:
				return m, tea.Batch(fetchMeetings(ctx), fetchJobs(ctx))
			}
			return m, fetchJobs(ctx)
		}
		if v.Event.Kind == notoapi.EventRecorder && v.Event.Recorder != nil {
			m.rec = *v.Event.Recorder
		}
		return m, nil

	case meetingLoadedMsg, summaryLoadedMsg, transcriptLoadedMsg, filesLoadedMsg,
		speakerRenamedMsg, speakerMappingsLoadedMsg, speakerIdentityMsg, speakerPersonOpenMsg,
		profilesLoadedMsg:
		// All right-pane fetches and pane-emitted messages flow through the
		// detail pane. (speakerPersonOpenMsg comes back out as a navigation cmd
		// the root router handles; profilesLoadedMsg feeds the assign dialog's
		// directory — only the active screen receives it, so no clash with the
		// People screen.)
		return m, m.pane.update(ctx, msg)

	case tea.KeyPressMsg:
		return m.handleKey(ctx, v)

	case mouseWheelMsg:
		return m.handleWheel(ctx, v)
	}
	return m, nil
}

// handleWheel scrolls whatever the pointer is over. Over the detail pane (its
// body or tab bar) it scrolls the pane's active tab WITHOUT stealing focus —
// hover-to-scroll — by replaying the canonical up/down key into the pane, so the
// per-tab scroll logic stays defined once. Anywhere else (the list, a row, the
// filter) it scrolls the LIST VIEW only — it moves listScroll, never the cursor,
// so the wheel pans the viewport while the selection (and detail pane) stay put.
// Clicking a row is what changes the selection; scrolling never does.
func (m *dashboardScreen) handleWheel(ctx screenCtx, w mouseWheelMsg) (screen, tea.Cmd) {
	if w.over == "dash:pane" || strings.HasPrefix(w.over, "dash:tab:") {
		if !m.pane.inputActive() {
			k := tea.KeyPressMsg{Code: tea.KeyDown}
			if w.up {
				k.Code = tea.KeyUp
			}
			m.pane.handleKey(ctx, k)
		}
		return m, nil
	}
	const wheelStep = 3 // lines per notch — a touch more than one 2-line row
	_, _, total := m.listLineLayout()
	if w.up {
		m.listScroll -= wheelStep
	} else {
		m.listScroll += wheelStep
	}
	m.listScroll = scroll.Clamp(m.listScroll, total, m.listRowBudget(ctx))
	return m, nil
}

// listLineLayout reports the line geometry of the meeting list WITHOUT rendering
// it, so Update can reconcile listScroll against the exact same geometry the view
// windows with. Rows are a fixed height per mode (browse = title + status line;
// a search hit = title + optional snippet), so the line count is derivable from
// the data alone.
func (m *dashboardScreen) listLineLayout() (cursorTop, cursorH, total int) {
	for i := 0; i < m.visibleCount(); i++ {
		h := m.rowLineCount(i)
		if i == m.cursor {
			cursorTop, cursorH = total, h
		}
		total += h
	}
	if cursorH == 0 {
		cursorH = 1
	}
	return
}

// rowLineCount is how many lines meeting i occupies — kept in lock-step with
// renderListRowDefault (always 2) / renderListRowSearch (1 + snippet).
func (m *dashboardScreen) rowLineCount(i int) int {
	if m.query == "" {
		return 2
	}
	if i < len(m.matched) && m.matched[i].Snippet != "" {
		return 2
	}
	return 1
}

// listRowBudget is how many list rows fit in the panel — the same arithmetic the
// view uses (listH − panel chrome − filter/blank/blank/hint), so Update and View
// agree on the window size.
func (m *dashboardScreen) listRowBudget(ctx screenCtx) int {
	stripH := m.bottomStripHeight()
	listH := layout.Split(ctx.height, 0, layout.FlexMin(1, 6), layout.Fixed(stripH))[0]
	b := listH - 8
	if b < 1 {
		b = 1
	}
	return b
}

// followCursor nudges listScroll the minimum needed to keep the selected row
// visible — called after the ARROW keys move the cursor (selection drives the
// view), never on a wheel scroll (which pans without selecting).
func (m *dashboardScreen) followCursor(ctx screenCtx) {
	top, h, total := m.listLineLayout()
	m.listScroll = scroll.Follow(total, m.listRowBudget(ctx), top, h, m.listScroll)
}

func (m *dashboardScreen) handleKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	// Filter has focus: text input + a few escape-hatch keys.
	if m.inputFocus {
		switch v.String() {
		case "esc":
			m.inputFocus = false
			m.input.Blur()
			return m, nil
		case "enter":
			m.inputFocus = false
			m.input.Blur()
			if m.currentMeetingID() != "" {
				m.paneOpen = true
				m.applyCurrentSearchTarget(true)
			}
			return m, m.loadSelectedCmd(ctx)
		case "tab":
			return m, m.advanceSearchHit(ctx)
		case "shift+tab":
			return m, m.retreatSearchHit(ctx)
		case "/":
			return m, m.refreshFocusedSearch(ctx)
		case "up":
			if m.cursor > 0 {
				m.cursor--
			}
			m.followCursor(ctx)
			return m, m.loadSelectedCmd(ctx)
		case "down":
			if m.cursor < m.visibleCount()-1 {
				m.cursor++
			}
			m.followCursor(ctx)
			return m, m.loadSelectedCmd(ctx)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(v)
		newQ := strings.TrimSpace(m.input.Value())
		if newQ != m.query {
			m.query = newQ
			return m, tea.Batch(cmd, m.runQueryCmd(ctx))
		}
		return m, cmd
	}

	// Pane has focus: all navigation keys go to it. Tab or Esc returns
	// focus to the meetings list so the user can move up/down again.
	if m.paneOpen {
		// While the pane captures input (rename editor / assign picker), let
		// it consume keys first — esc/tab close the sub-mode, digits assign —
		// before the focus-management shortcuts below would steal them.
		if m.pane.inputActive() {
			if handled, cmd := m.pane.handleKey(ctx, v); handled {
				return m, cmd
			}
		}
		switch v.String() {
		case "esc", "tab", "shift+tab":
			m.paneOpen = false
			return m, nil
		}
		// `/` focuses the filter input from anywhere on the dashboard,
		// including while the pane holds focus — handled before forwarding
		// so the pane can never swallow it (the list-focus branch below
		// does the same).
		if key.Matches(v, ctx.keys.Search) {
			m.paneOpen = false
			m.inputFocus = true
			m.input.Focus()
			return m, m.refreshFocusedSearch(ctx)
		}
		// Forward the key. If the pane handled it, swallow; otherwise
		// fall through so global bindings like `q` still work.
		if handled, cmd := m.pane.handleKey(ctx, v); handled {
			return m, cmd
		}
		// While focused, even unhandled keys shouldn't drive the list
		// (no accidental cursor moves) — except a few escape hatches
		// that act on the bound meeting regardless of focus.
		switch {
		case key.Matches(v, ctx.keys.Delete):
			if id := m.currentMeetingID(); id != "" {
				return m, deleteMeetingCmd(ctx, id)
			}
		case key.Matches(v, ctx.keys.OpenAgent):
			if id := m.currentMeetingID(); id != "" {
				return m, push(sAgent, id)
			}
		}
		return m, nil
	}

	switch {
	case key.Matches(v, ctx.keys.Search):
		m.inputFocus = true
		m.input.Focus()
		return m, m.refreshFocusedSearch(ctx)
	case key.Matches(v, ctx.keys.Up):
		if m.cursor > 0 {
			m.cursor--
		}
		m.followCursor(ctx)
		return m, m.loadSelectedCmd(ctx)
	case key.Matches(v, ctx.keys.Down):
		if m.cursor < m.visibleCount()-1 {
			m.cursor++
		}
		m.followCursor(ctx)
		return m, m.loadSelectedCmd(ctx)
	case key.Matches(v, ctx.keys.Enter), key.Matches(v, ctx.keys.Tab):
		// Enter (or Tab) shifts focus into the right pane instead of
		// pushing a new screen. The pane owns the meeting and the user
		// drives tabs / scrolling there.
		if m.currentMeetingID() != "" {
			m.paneOpen = true
			return m, nil
		}
	case key.Matches(v, ctx.keys.Delete):
		if id := m.currentMeetingID(); id != "" {
			return m, deleteMeetingCmd(ctx, id)
		}
	case key.Matches(v, ctx.keys.OpenAgent):
		if id := m.currentMeetingID(); id != "" {
			return m, push(sAgent, id)
		}
	case key.Matches(v, ctx.keys.TabNext), key.Matches(v, ctx.keys.TabPrev),
		key.Matches(v, ctx.keys.Transcript), key.Matches(v, ctx.keys.Speakers),
		key.Matches(v, ctx.keys.NextMatch), key.Matches(v, ctx.keys.PrevMatch):
		// Tab / match-jump nav stays usable from the list too so users
		// who don't focus the pane can still flip tabs while skimming.
		// All of these forward to the pane so list- and pane-focus modes
		// stay in sync (see detailPane.handleKey).
		if handled, cmd := m.pane.handleKey(ctx, v); handled {
			return m, cmd
		}
	case key.Matches(v, ctx.keys.ClearSearch):
		// Clear query without re-entering the input.
		if m.query != "" {
			m.input.SetValue("")
			m.query = ""
			m.lastQuery = ""
			m.matched = nil
			m.clampCursor()
			return m, m.loadSelectedCmd(ctx)
		}
	case key.Matches(v, ctx.keys.Refresh):
		return m, fetchMeetings(ctx)
	}
	return m, nil
}

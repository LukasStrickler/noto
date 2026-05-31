package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/layout"
	"github.com/lukasstrickler/noto/internal/tui/theme"
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

	case meetingLoadedMsg, summaryLoadedMsg, transcriptLoadedMsg, filesLoadedMsg, speakerRenamedMsg:
		// All right-pane fetches and pane-emitted messages flow
		// through the detail pane.
		return m, m.pane.update(ctx, msg)

	case tea.KeyMsg:
		return m.handleKey(ctx, v)
	}
	return m, nil
}

func (m *dashboardScreen) handleKey(ctx screenCtx, v tea.KeyMsg) (screen, tea.Cmd) {
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
			return m, m.loadSelectedCmd(ctx)
		case "down":
			if m.cursor < m.visibleCount()-1 {
				m.cursor++
			}
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
		switch v.String() {
		case "esc", "tab", "shift+tab":
			m.paneOpen = false
			return m, nil
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
		return m, m.loadSelectedCmd(ctx)
	case key.Matches(v, ctx.keys.Down):
		if m.cursor < m.visibleCount()-1 {
			m.cursor++
		}
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

// runQueryCmd kicks an FTS request (or clears the matched list if the
// query just emptied).
func (m *dashboardScreen) runQueryCmd(ctx screenCtx) tea.Cmd {
	if m.query == "" {
		m.matched = nil
		m.cursor = 0
		m.lastQuery = ""
		m.preserveSearchSelection = false
		return m.loadSelectedCmd(ctx)
	}
	if m.query == m.lastQuery {
		return nil
	}
	m.lastQuery = m.query
	m.cursor = 0
	m.preserveSearchSelection = false
	return runSearch(ctx, m.query)
}

// loadSelectedCmd asks the detail pane to bind to the row currently
// under the cursor. Cheap to call repeatedly: identical IDs no-op.
func (m *dashboardScreen) loadSelectedCmd(ctx screenCtx) tea.Cmd {
	id := m.currentMeetingID()
	m.pane.setQuery(m.query)
	cmd := m.pane.load(ctx, id)
	m.applyCurrentSearchTarget(false)
	return cmd
}

func (m *dashboardScreen) refreshFocusedSearch(ctx screenCtx) tea.Cmd {
	if m.query == "" {
		return nil
	}
	m.preserveSearchSelection = true
	return runSearch(ctx, m.query)
}

func (m *dashboardScreen) advanceSearchHit(ctx screenCtx) tea.Cmd {
	return m.stepSearchHit(ctx, +1)
}

func (m *dashboardScreen) retreatSearchHit(ctx screenCtx) tea.Cmd {
	return m.stepSearchHit(ctx, -1)
}

func (m *dashboardScreen) stepSearchHit(ctx screenCtx, direction int) tea.Cmd {
	if m.query == "" || len(m.matched) == 0 {
		return nil
	}
	if m.pane.meetingID() == m.currentMeetingID() && len(m.pane.matchedSegs) > 0 {
		wrapped := m.pane.advanceMatch(direction)
		m.pane.tab = tabTranscript
		if !wrapped {
			return nil
		}
	} else if m.stepPendingSearchHit(direction) {
		m.pane.tab = tabTranscript
		return nil
	}
	if direction < 0 {
		m.cursor = (m.cursor - 1 + len(m.matched)) % len(m.matched)
	} else {
		m.cursor = (m.cursor + 1) % len(m.matched)
	}
	m.applyCurrentSearchTarget(true)
	return m.loadSelectedCmd(ctx)
}

func (m *dashboardScreen) stepPendingSearchHit(direction int) bool {
	ids := m.currentTranscriptHitSegmentIDs()
	if len(ids) <= 1 {
		return false
	}
	current := m.pane.requestedMatchSegmentID
	if current == "" {
		current = ids[0]
	}
	idx := 0
	for i, id := range ids {
		if id == current {
			idx = i
			break
		}
	}
	next := idx + direction
	if next < 0 || next >= len(ids) {
		return false
	}
	m.pane.setActiveMatchSegmentID(ids[next])
	return true
}

func (m *dashboardScreen) applyCurrentSearchTarget(showTranscript bool) {
	ids := m.currentTranscriptHitSegmentIDs()
	if len(ids) == 0 {
		return
	}
	segmentID := ids[0]
	for _, id := range ids {
		if id == m.pane.requestedMatchSegmentID {
			segmentID = id
			break
		}
	}
	if showTranscript {
		m.pane.tab = tabTranscript
	}
	m.pane.setActiveMatchSegmentID(segmentID)
}

func (m *dashboardScreen) currentTranscriptHitSegmentIDs() []string {
	if m.query == "" || m.cursor < 0 || m.cursor >= len(m.matched) {
		return nil
	}
	out := []string{}
	for _, hit := range m.matched[m.cursor].TopHits {
		if hit.ResultType == "transcript" && hit.SegmentID != "" {
			out = append(out, hit.SegmentID)
		}
	}
	return out
}

func (m *dashboardScreen) selectMeetingID(id string) {
	if id == "" || m.query == "" {
		return
	}
	for i, hit := range m.matched {
		if hit.MeetingID == id {
			m.cursor = i
			return
		}
	}
}

func (m *dashboardScreen) clampCursor() {
	n := m.visibleCount()
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *dashboardScreen) visibleCount() int {
	if m.query != "" {
		return len(m.matched)
	}
	return len(m.all)
}

func (m *dashboardScreen) currentMeetingID() string {
	if m.query != "" {
		if m.cursor >= 0 && m.cursor < len(m.matched) {
			return m.matched[m.cursor].MeetingID
		}
		return ""
	}
	if m.cursor >= 0 && m.cursor < len(m.all) {
		return m.all[m.cursor].ID
	}
	return ""
}

func (m *dashboardScreen) currentMatchedRow() (notoapi.MeetingHits, bool) {
	if m.query == "" {
		return notoapi.MeetingHits{}, false
	}
	if m.cursor < 0 || m.cursor >= len(m.matched) {
		return notoapi.MeetingHits{}, false
	}
	return m.matched[m.cursor], true
}

// --- view ---------------------------------------------------------

func (m *dashboardScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if m.loading {
		return panelEmpty(ctx, "dashboard", s.Muted.Render("loading meetings…"))
	}
	if m.err != nil {
		return panelEmpty(ctx, "dashboard", s.Danger.Render("✗ "+m.err.Error()))
	}

	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		return m.viewNarrow(ctx)
	}
	return m.viewWide(ctx)
}

// bottomStripHeight is dynamic: hidden when idle, ~6 rows when there's
// activity, ~8 rows when there are multiple concurrent jobs. The
// border alone eats 2 rows so the inner body lands at 4 / 6 rows.
func (m *dashboardScreen) bottomStripHeight() int {
	if !m.rec.Active && len(m.jobs) == 0 {
		return 0
	}
	if len(m.jobs) >= 3 {
		return 8
	}
	return 6
}

func (m *dashboardScreen) viewWide(ctx screenCtx) string {
	s := ctx.styles
	// Left meetings column is ~1/3 (never below 28), details takes the
	// rest; HStack reserves the same 1-cell gap the solver accounts for.
	cols := layout.Split(ctx.width, 1, layout.FlexMin(1, 28), layout.Flex(2))
	leftW, rightW := cols[0], cols[1]

	// Left column splits vertically into the meetings list (flex) and the
	// bottom strip (fixed; 0 collapses it entirely).
	stripH := m.bottomStripHeight()
	listH := layout.Split(ctx.height, 0, layout.FlexMin(1, 6), layout.Fixed(stripH))[0]

	listPanel := layout.Panel{
		Title:    "dashboard",
		Subtitle: m.listSubtitle(),
		Width:    leftW,
		Height:   listH,
		Focused:  !m.paneOpen,
		Body:     m.renderList(ctx, leftW),
	}.Render(s)

	leftCol := listPanel
	if stripH > 0 {
		leftCol = layout.VStack(listPanel, m.renderBottomStrip(ctx, leftW, stripH))
	}

	rightPanel := layout.Panel{
		Title:    "details",
		Subtitle: m.previewSubtitle(),
		Width:    rightW,
		Height:   ctx.height,
		Focused:  m.paneOpen,
		Body:     m.pane.view(ctx, rightW-4, ctx.height-2),
	}.Render(s)

	return layout.HStack(leftCol, rightPanel)
}

func (m *dashboardScreen) viewNarrow(ctx screenCtx) string {
	s := ctx.styles
	stripH := m.bottomStripHeight()
	listH := layout.Split(ctx.height, 0, layout.FlexMin(1, 6), layout.Fixed(stripH))[0]
	listPanel := layout.Panel{
		Title:    "dashboard",
		Subtitle: m.listSubtitle(),
		Width:    ctx.width,
		Height:   listH,
		Focused:  true,
		Body:     m.renderList(ctx, ctx.width),
	}.Render(s)
	if stripH == 0 {
		return listPanel
	}
	return layout.VStack(listPanel, m.renderBottomStrip(ctx, ctx.width, stripH))
}

// renderBottomStrip lays out jobs + active recording as a 50/50
// horizontal split. Hidden when both are idle.
func (m *dashboardScreen) renderBottomStrip(ctx screenCtx, width, height int) string {
	s := ctx.styles
	cols := layout.Split(width, 1, layout.Flex(1), layout.Flex(1))
	jobsW, recW := cols[0], cols[1]
	jobsPanel := layout.Panel{
		Title:    "jobs",
		Subtitle: m.jobsSubtitle(),
		Width:    jobsW,
		Height:   height,
		Body:     m.renderJobsBody(ctx, jobsW-4),
	}.Render(s)
	recPanel := layout.Panel{
		Title:    "recording",
		Subtitle: m.recordingSubtitle(),
		Width:    recW,
		Height:   height,
		Body:     m.renderRecordingBody(ctx, recW-4),
	}.Render(s)
	return layout.HStack(jobsPanel, recPanel)
}

func (m *dashboardScreen) jobsSubtitle() string {
	if len(m.jobs) == 0 {
		return ""
	}
	running := 0
	for _, j := range m.jobs {
		if j.Status == notoapi.JobRunning {
			running++
		}
	}
	if running > 0 {
		return fmt.Sprintf("%d running", running)
	}
	return fmt.Sprintf("%d recent", len(m.jobs))
}

func (m *dashboardScreen) recordingSubtitle() string {
	if !m.rec.Active {
		return ""
	}
	return formatDuration(m.rec.ElapsedSec)
}

func (m *dashboardScreen) renderJobsBody(ctx screenCtx, width int) string {
	s := ctx.styles
	if len(m.jobs) == 0 {
		return s.Muted.Render("idle")
	}
	out := []string{}
	maxRows := 4
	if width > 60 {
		maxRows = 6
	}
	for i, j := range m.jobs {
		if i >= maxRows {
			break
		}
		bar := jobBar(j.Progress, 10)
		var status string
		switch j.Status {
		case notoapi.JobRunning:
			status = s.Info.Render("running")
		case notoapi.JobSucceeded:
			status = s.Success.Render("done")
		case notoapi.JobFailed:
			status = s.Danger.Render("failed")
		case notoapi.JobInterrupted:
			status = s.Warning.Render("interrupted")
		default:
			status = s.Muted.Render(string(j.Status))
		}
		line := fmt.Sprintf("%s %s %s", jobKindStyled(s, j.Kind), bar, status)
		out = append(out, clipLine(line, width))
	}
	return strings.Join(out, "\n")
}

func (m *dashboardScreen) renderRecordingBody(ctx screenCtx, width int) string {
	s := ctx.styles
	if !m.rec.Active {
		return strings.Join([]string{
			s.Muted.Render("○ idle"),
			s.Muted.Render("press ") + s.ChipKey.Render(ctx.keys.Record.Help().Key) + s.Muted.Render(" to record"),
		}, "\n")
	}
	lines := []string{
		s.Recording.Render(fmt.Sprintf("● REC  %s", formatDuration(m.rec.ElapsedSec))),
		s.HeaderEm.Render(def(m.rec.Title, "Untitled meeting")),
		renderMeter(s, "mic ", m.rec.MicDB),
		renderMeter(s, "sys ", m.rec.ParticipantDB),
	}
	for i, l := range lines {
		lines[i] = clipLine(l, width)
	}
	return strings.Join(lines, "\n")
}

func (m *dashboardScreen) previewSubtitle() string {
	if !m.pane.hasID() {
		return ""
	}
	mh, ok := m.currentMatchedRow()
	if !ok {
		return ""
	}
	parts := []string{}
	if mh.TranscriptCount > 0 {
		parts = append(parts, fmt.Sprintf("%d in transcript", mh.TranscriptCount))
	}
	if mh.SummaryCount > 0 {
		parts = append(parts, fmt.Sprintf("%d in summary", mh.SummaryCount))
	}
	return strings.Join(parts, " · ")
}

// --- list rendering -----------------------------------------------

func (m *dashboardScreen) renderList(ctx screenCtx, width int) string {
	s := ctx.styles
	// Panel padding+border consumes 4 cols; clip everything we draw to
	// what's actually visible inside. Otherwise long titles, snippets,
	// or the hint bar spray past the right border.
	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}

	// Filter and hints share the same left-edge as list rows (3-cell
	// marker gutter) so titles, the search chip, and the hint bar all
	// start at the same column. Without this the title text appears
	// shifted relative to the search input.
	header := clipLine("   "+m.renderFilterRow(ctx), innerW)
	hintBar := s.HintBar.Render(clipLine("   "+m.renderListHints(ctx, innerW-3), innerW))

	if m.visibleCount() == 0 {
		empty := s.Muted.Render("no meetings yet — record one with r, or run `make seed`")
		if m.query != "" {
			empty = s.Muted.Render(fmt.Sprintf("no matches for %q", m.query))
		}
		return header + "\n\n" + clipLine(empty, innerW) + "\n\n" + hintBar
	}

	tokens := queryTokens(m.query)
	rowInnerW := innerW - 3 // " ▸ " / "   " prefix
	if rowInnerW < 14 {
		rowInnerW = 14
	}

	var rows []string
	for i := 0; i < m.visibleCount(); i++ {
		var lines []string
		if m.query == "" {
			lines = m.renderListRowDefault(s, m.all[i], rowInnerW)
		} else {
			lines = m.renderListRowSearch(s, m.matched[i], rowInnerW, tokens)
		}
		// Both prefix branches are exactly 3 visible cells so subsequent
		// rows stay column-aligned. The styled cursor prefix only shows
		// on the first line of multi-line search rows; snippet/wrap
		// lines keep the plain 3-space indent.
		prefix := "   "
		if i == m.cursor {
			prefix = s.RowSelected.Render(" ▸ ")
		}
		for j, line := range lines {
			indent := prefix
			if j > 0 {
				indent = "   "
			}
			rows = append(rows, indent+clipLine(line, rowInnerW))
		}
	}
	return header + "\n\n" + strings.Join(rows, "\n") + "\n\n" + hintBar
}

// renderListHints picks a hint set that fits the available width.
// Below ~50 cols (left panel on a narrow terminal) the long list of
// hints overflows; we collapse to the essential three.
func (m *dashboardScreen) renderListHints(ctx screenCtx, width int) string {
	s, k := ctx.styles, ctx.keys
	full := []string{
		chip(s, k.Search),
		chipPair(s, k.Up, k.Down, "navigate"),
		chipAs(s, k.Enter, "open"),
		chip(s, k.NextMatch),
		chip(s, k.ClearSearch),
	}
	joined := strings.Join(full, "  ")
	if lipgloss.Width(joined) <= width {
		return joined
	}
	short := []string{
		chip(s, k.Search),
		chipPair(s, k.Up, k.Down, "nav"),
		chipAs(s, k.Enter, "open"),
	}
	return strings.Join(short, "  ")
}

func (m *dashboardScreen) renderFilterRow(ctx screenCtx) string {
	s, k := ctx.styles, ctx.keys
	keyChip := s.ChipKey.Render(k.Search.Help().Key)
	if m.inputFocus {
		return keyChip + " " + m.input.View()
	}
	if m.query == "" {
		return keyChip + " " + s.Muted.Render(m.input.Placeholder)
	}
	editHint := fmt.Sprintf("(press %s to edit · %s to clear)", k.Search.Help().Key, k.ClearSearch.Help().Key)
	return keyChip + " " + s.HeaderEm.Render(m.query) + "   " + s.Muted.Render(editHint)
}

// renderListRowDefault lays out a "browse" row over 2 visible lines:
//
//	row 1: title · date · status_badge
//	row 2: ◆ N decisions   ▸ N actions   ⚠ N risks   ? N questions  (omits zero counts)
//
// Width budget for row 1: title (flexible) + date (12) + status (8) → leave a 20-cell shoulder
// for the meta cells, the rest goes to the title.
func (m *dashboardScreen) renderListRowDefault(s theme.Styles, mt notoapi.Meeting, width int) []string {
	titleBudget := width - 22
	if titleBudget < 10 {
		titleBudget = 10
	}
	title := fit(mt.Title, titleBudget)
	when := mt.CreatedAt.Format("Jan 02 15:04")
	rowParts := []string{
		s.HeaderEm.Render(title),
		s.Muted.Render(when),
	}
	if badge := dashboardStatusBadge(s, mt.Status); badge != "" {
		rowParts = append(rowParts, badge)
	}
	row1 := strings.Join(rowParts, "  ")
	counters := renderCounterStrip(s, mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount)
	if counters == "" {
		counters = s.Muted.Render("·")
	}
	return []string{row1, counters}
}

func dashboardStatusBadge(s theme.Styles, status notoapi.MeetingStatus) string {
	switch status {
	case notoapi.StatusRecorded:
		return badgeWarn(s, "todo")
	case notoapi.StatusRecording:
		return badgeDanger(s, "● rec")
	case notoapi.StatusFailed:
		return badgeDanger(s, "✗ failed")
	default:
		return ""
	}
}

// renderListRowSearch returns 1 or 2 visible lines: the title row and,
// when present, a snippet preview. The caller is responsible for
// clipping/indenting each line.
func (m *dashboardScreen) renderListRowSearch(s theme.Styles, mh notoapi.MeetingHits, width int, tokens []string) []string {
	titleBudget := width - 22
	if titleBudget < 10 {
		titleBudget = 10
	}
	title := fit(mh.MeetingTitle, titleBudget)
	titleStyled := highlightString(s, title, tokens, -1, -1)
	dot := s.Muted.Render("○ ")
	switch {
	case mh.TitleMatch:
		dot = s.HeaderEm.Render("● ")
	case mh.SummaryMatch:
		dot = s.Info.Render("✦ ")
	}
	when := ""
	if !mh.CreatedAt.IsZero() {
		when = mh.CreatedAt.Format("Jan 02")
	}
	counts := []string{}
	if mh.TranscriptCount > 0 {
		counts = append(counts, fmt.Sprintf("tr %d", mh.TranscriptCount))
	}
	if mh.SummaryCount > 0 {
		counts = append(counts, fmt.Sprintf("su %d", mh.SummaryCount))
	}
	first := dot + titleStyled + "  " + s.Muted.Render(when)
	if len(counts) > 0 {
		first += "  " + s.Info.Render(strings.Join(counts, " · "))
	}
	out := []string{first}
	if mh.Snippet != "" {
		out = append(out, s.Muted.Render(snippetText(mh.Snippet, width-2)))
	}
	return out
}

func (m *dashboardScreen) listSubtitle() string {
	if m.query != "" {
		return fmt.Sprintf("%d hits", len(m.matched))
	}
	return fmt.Sprintf("%d total", len(m.all))
}

// --- highlight + token helpers ------------------------------------

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func queryTokens(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	parts := nonWord.Split(q, -1)
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func textContainsAny(text string, tokens []string) bool {
	lower := strings.ToLower(text)
	for _, t := range tokens {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// highlightString wraps every token occurrence (case-insensitive) in
// the Highlight style. If segIdx == activeIdx the rendering uses the
// HighlightActive style so the user can see which segment n/N is on.
func highlightString(s theme.Styles, text string, tokens []string, segIdx, activeIdx int) string {
	if text == "" || len(tokens) == 0 {
		return text
	}
	pattern := buildTokenPattern(tokens)
	if pattern == nil {
		return text
	}
	style := s.Highlight
	if segIdx >= 0 && segIdx == activeIdx {
		style = s.HighlightActive
	}
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		return style.Render(match)
	})
}

var patternCache = map[string]*regexp.Regexp{}

func buildTokenPattern(tokens []string) *regexp.Regexp {
	key := strings.Join(tokens, "|")
	if re, ok := patternCache[key]; ok {
		return re
	}
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = regexp.QuoteMeta(t)
	}
	re, err := regexp.Compile("(?i)(" + strings.Join(quoted, "|") + ")")
	if err != nil {
		return nil
	}
	patternCache[key] = re
	return re
}

// --- small helpers ------------------------------------------------

// snippetText strips FTS5 '**' delimiters (we apply our own highlight)
// and clamps length.
func snippetText(snip string, maxLen int) string {
	snip = strings.ReplaceAll(snip, "**", "")
	if len(snip) > maxLen {
		snip = snip[:maxLen-1] + "…"
	}
	return snip
}

// clipLine truncates a single rendered line to `width` visible cells
// while preserving ANSI styles. Lipgloss's MaxWidth handles the ANSI
// math; we keep the input single-line so it doesn't insert padding.
func clipLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func deleteMeetingCmd(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		err := ctx.client.DeleteMeeting(ctx.ctx, id)
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "meeting deleted"}
	}
}

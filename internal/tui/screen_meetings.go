package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// meetingsScreen is the unified meetings + search surface.
//
// Layout (wide):
//
//	┌── meetings (1/3) ───┬── summary (top 1/3) ─────────────┐
//	│ /filter             │ short summary + decisions/...    │
//	│ ▸ row 1             ├── transcript (bottom 2/3) ───────┤
//	│   row 2             │ segments with FTS highlights     │
//	│   row 3             │                                  │
//	└─────────────────────┴──────────────────────────────────┘
//
// `/` focuses the filter input. Typing fires an FTS search; empty
// query falls back to "recent first" listing. n/N jumps the transcript
// to the next/previous segment that matched.
type meetingsScreen struct {
	loading bool
	err     error

	// Default listing — populated by ListMeetings.
	all []notoapi.Meeting
	// FTS-driven view — populated by Search.
	matched []notoapi.MeetingHits

	input      textinput.Model
	inputFocus bool
	query      string
	lastQuery  string

	cursor int

	// Right pane state for the meeting currently under the cursor.
	selectedID         string
	selectedTranscript notoapi.Transcript
	selectedSummary    notoapi.Summary
	selectedMeeting    notoapi.Meeting
	rightLoadingID     string

	// FTS jump-to-match: indices into selectedTranscript.Segments whose
	// text contains at least one query token.
	matchedSegments []int
	matchCursor     int
	transcriptScroll int
}

func newMeetingsScreen() screen {
	ti := textinput.New()
	ti.Placeholder = "search title, transcript, decisions, action items, risks…"
	ti.CharLimit = 200
	return &meetingsScreen{loading: true, input: ti}
}

func (m *meetingsScreen) id() screenID         { return sMeetings }
func (m *meetingsScreen) title() string        { return "meetings" }
func (m *meetingsScreen) helpKeys() []keys.Map { return nil }
func (m *meetingsScreen) inputActive() bool    { return m.inputFocus }

func (m *meetingsScreen) meetingID() string {
	return m.currentMeetingID()
}

func (m *meetingsScreen) enter(ctx screenCtx, param string) tea.Cmd {
	// param convention:
	//   ""              — no preload
	//   "filter"        — focus the filter input on entry
	//   anything else   — treat as a pre-filled query
	switch param {
	case "":
	case "filter":
		m.inputFocus = true
		m.input.Focus()
	default:
		m.input.SetValue(param)
		m.query = strings.TrimSpace(param)
		m.lastQuery = m.query
		m.inputFocus = true
		m.input.Focus()
	}
	return fetchMeetings(ctx)
}

func (m *meetingsScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (m *meetingsScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {

	case meetingsLoadedMsg:
		m.loading = false
		m.err = v.Err
		m.all = v.Result.Meetings
		m.clampCursor()
		return m, m.loadSelectedCmd(ctx)

	case searchResultMsg:
		// Discard stale results — query may have moved on.
		if v.Query == m.query {
			m.err = v.Err
			m.matched = v.Result.Meetings
			m.clampCursor()
			return m, m.loadSelectedCmd(ctx)
		}
		return m, nil

	case transcriptLoadedMsg:
		if v.Err == nil && v.Transcript.MeetingID == m.rightLoadingID {
			m.selectedTranscript = v.Transcript
			m.recomputeMatches()
		}
		return m, nil

	case summaryLoadedMsg:
		if v.Err == nil && v.Summary.MeetingID == m.rightLoadingID {
			m.selectedSummary = v.Summary
		}
		return m, nil

	case meetingLoadedMsg:
		if v.Err == nil && v.Meeting.ID == m.rightLoadingID {
			m.selectedMeeting = v.Meeting
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(ctx, v)
	}
	return m, nil
}

func (m *meetingsScreen) handleKey(ctx screenCtx, v tea.KeyMsg) (screen, tea.Cmd) {
	// Filter has focus: text input + a few escape-hatch keys.
	if m.inputFocus {
		switch v.String() {
		case "esc":
			m.inputFocus = false
			m.input.Blur()
			return m, nil
		case "enter":
			// Defocus but keep the query active so the user can navigate
			// the filtered list with arrows.
			m.inputFocus = false
			m.input.Blur()
			return m, nil
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

	switch {
	case key.Matches(v, ctx.keys.Search):
		m.inputFocus = true
		m.input.Focus()
		return m, nil
	case v.String() == "/":
		// Defensive: if Search key match misses (shouldn't, but just
		// in case), still focus the filter so `/` always works here.
		m.inputFocus = true
		m.input.Focus()
		return m, nil
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
	case key.Matches(v, ctx.keys.Enter):
		if id := m.currentMeetingID(); id != "" {
			return m, push(sDetail, id)
		}
	case key.Matches(v, ctx.keys.Delete):
		if id := m.currentMeetingID(); id != "" {
			return m, deleteMeetingCmd(ctx, id)
		}
	case v.String() == "n":
		m.jumpToMatch(+1)
		return m, nil
	case v.String() == "N", v.String() == "shift+n":
		m.jumpToMatch(-1)
		return m, nil
	case v.String() == "x":
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
func (m *meetingsScreen) runQueryCmd(ctx screenCtx) tea.Cmd {
	if m.query == "" {
		m.matched = nil
		m.cursor = 0
		m.lastQuery = ""
		return m.loadSelectedCmd(ctx)
	}
	if m.query == m.lastQuery {
		return nil
	}
	m.lastQuery = m.query
	m.cursor = 0
	return runSearch(ctx, m.query)
}

// loadSelectedCmd kicks transcript+summary+meeting fetches for the row
// currently under the cursor. Cheap to call repeatedly: identical IDs
// are no-oped.
func (m *meetingsScreen) loadSelectedCmd(ctx screenCtx) tea.Cmd {
	id := m.currentMeetingID()
	if id == "" {
		m.selectedID = ""
		m.rightLoadingID = ""
		m.selectedTranscript = notoapi.Transcript{}
		m.selectedSummary = notoapi.Summary{}
		m.selectedMeeting = notoapi.Meeting{}
		m.matchedSegments = nil
		return nil
	}
	if id == m.rightLoadingID {
		return nil
	}
	m.rightLoadingID = id
	m.selectedID = id
	m.selectedTranscript = notoapi.Transcript{}
	m.selectedSummary = notoapi.Summary{}
	m.selectedMeeting = notoapi.Meeting{}
	m.matchedSegments = nil
	m.matchCursor = 0
	m.transcriptScroll = 0
	return tea.Batch(
		fetchMeeting(ctx, id),
		fetchTranscript(ctx, id),
		fetchSummary(ctx, id),
	)
}

func (m *meetingsScreen) clampCursor() {
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

func (m *meetingsScreen) visibleCount() int {
	if m.query != "" {
		return len(m.matched)
	}
	return len(m.all)
}

func (m *meetingsScreen) currentMeetingID() string {
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

func (m *meetingsScreen) currentMatchedRow() (notoapi.MeetingHits, bool) {
	if m.query == "" {
		return notoapi.MeetingHits{}, false
	}
	if m.cursor < 0 || m.cursor >= len(m.matched) {
		return notoapi.MeetingHits{}, false
	}
	return m.matched[m.cursor], true
}

func (m *meetingsScreen) recomputeMatches() {
	m.matchedSegments = m.matchedSegments[:0]
	tokens := queryTokens(m.query)
	if len(tokens) == 0 {
		m.matchCursor = 0
		m.transcriptScroll = 0
		return
	}
	for i, seg := range m.selectedTranscript.Segments {
		if textContainsAny(seg.Text, tokens) {
			m.matchedSegments = append(m.matchedSegments, i)
		}
	}
	m.matchCursor = 0
	if len(m.matchedSegments) > 0 {
		m.transcriptScroll = m.matchedSegments[0]
	} else {
		m.transcriptScroll = 0
	}
}

func (m *meetingsScreen) jumpToMatch(direction int) {
	if len(m.matchedSegments) == 0 {
		return
	}
	m.matchCursor = (m.matchCursor + direction + len(m.matchedSegments)) % len(m.matchedSegments)
	m.transcriptScroll = m.matchedSegments[m.matchCursor]
}

// --- view ---------------------------------------------------------

func (m *meetingsScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if m.loading {
		return panelEmpty(ctx, "meetings", s.Muted.Render("loading meetings…"))
	}
	if m.err != nil {
		return panelEmpty(ctx, "meetings", s.Danger.Render("✗ "+m.err.Error()))
	}

	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		return m.viewNarrow(ctx)
	}
	return m.viewWide(ctx)
}

func (m *meetingsScreen) viewWide(ctx screenCtx) string {
	s := ctx.styles
	leftW := ctx.width / 3
	if leftW < 28 {
		leftW = 28
	}
	rightW := ctx.width - leftW - 1
	listBody := m.renderList(ctx, leftW)
	topH := ctx.height / 3
	if topH < 6 {
		topH = 6
	}
	bottomH := ctx.height - topH - 1
	if bottomH < 6 {
		bottomH = 6
	}
	summaryPanel := layout.Panel{
		Title:    "summary",
		Subtitle: m.summarySubtitle(),
		Width:    rightW,
		Height:   topH,
		Body:     m.renderSummaryBody(ctx, rightW),
	}.Render(s)
	transcriptPanel := layout.Panel{
		Title:    "transcript",
		Subtitle: m.transcriptSubtitle(),
		Width:    rightW,
		Height:   bottomH,
		Body:     m.renderTranscriptBody(ctx, rightW, bottomH),
	}.Render(s)
	left := layout.Panel{
		Title:    "meetings",
		Subtitle: m.listSubtitle(),
		Width:    leftW,
		Height:   ctx.height,
		Focused:  true,
		Body:     listBody,
	}.Render(s)
	right := lipgloss.JoinVertical(lipgloss.Left, summaryPanel, transcriptPanel)
	return left + " " + right
}

func (m *meetingsScreen) viewNarrow(ctx screenCtx) string {
	s := ctx.styles
	body := m.renderList(ctx, ctx.width)
	return layout.Panel{
		Title:    "meetings",
		Subtitle: m.listSubtitle(),
		Width:    ctx.width,
		Height:   ctx.height,
		Focused:  true,
		Body:     body,
	}.Render(s)
}

// --- list rendering -----------------------------------------------

func (m *meetingsScreen) renderList(ctx screenCtx, width int) string {
	s := ctx.styles
	header := m.renderFilterRow(s)
	hints := []string{
		hint(s, "/", "search"),
		hint(s, "↑/↓", "navigate"),
		hint(s, "⏎", "open"),
		hint(s, "n", "next hit"),
		hint(s, "x", "clear"),
	}
	hintBar := s.HintBar.Render(strings.Join(hints, "  "))

	if m.visibleCount() == 0 {
		empty := s.Muted.Render("no meetings yet — record one with r, or run `make seed`")
		if m.query != "" {
			empty = s.Muted.Render(fmt.Sprintf("no matches for %q", m.query))
		}
		return header + "\n\n" + empty + "\n\n" + hintBar
	}

	tokens := queryTokens(m.query)
	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}

	var rows []string
	for i := 0; i < m.visibleCount(); i++ {
		var row string
		if m.query == "" {
			row = m.renderListRowDefault(s, m.all[i], innerW)
		} else {
			row = m.renderListRowSearch(s, m.matched[i], innerW, tokens)
		}
		if i == m.cursor {
			row = s.RowSelected.Render(" ▸ " + truncateAnsi(row, innerW-4))
		} else {
			row = "   " + row
		}
		rows = append(rows, row)
	}
	return header + "\n\n" + strings.Join(rows, "\n") + "\n\n" + hintBar
}

func (m *meetingsScreen) renderFilterRow(s theme.Styles) string {
	chip := s.ChipKey.Render("/")
	if m.inputFocus {
		return chip + " " + m.input.View()
	}
	if m.query == "" {
		return chip + " " + s.Muted.Render(m.input.Placeholder)
	}
	return chip + " " + s.HeaderEm.Render(m.query) + "   " + s.Muted.Render("(press / to edit · x to clear)")
}

func (m *meetingsScreen) renderListRowDefault(s theme.Styles, mt notoapi.Meeting, width int) string {
	title := fitWidth(mt.Title, max(16, width-30))
	when := mt.CreatedAt.Format("Jan 02 15:04")
	parts := []string{
		s.HeaderEm.Render(title),
		s.Muted.Render(when),
		statusBadge(s, mt.Status),
		s.Muted.Render(fmt.Sprintf("D %d · A %d · R %d", mt.DecisionCount, mt.ActionCount, mt.RiskCount)),
	}
	return strings.Join(parts, "   ")
}

func (m *meetingsScreen) renderListRowSearch(s theme.Styles, mh notoapi.MeetingHits, width int, tokens []string) string {
	title := fitWidth(mh.MeetingTitle, max(16, width-36))
	titleStyled := highlightString(s, title, tokens, -1, -1)
	if mh.TitleMatch {
		titleStyled = s.HeaderEm.Render("● ") + titleStyled
	} else {
		titleStyled = s.Muted.Render("○ ") + titleStyled
	}
	when := ""
	if !mh.CreatedAt.IsZero() {
		when = mh.CreatedAt.Format("Jan 02")
	}
	counts := fmt.Sprintf("tr %d · su %d", mh.TranscriptCount, mh.SummaryCount)
	parts := []string{
		titleStyled,
		s.Muted.Render(when),
		s.Info.Render(counts),
	}
	row := strings.Join(parts, "   ")
	if mh.Snippet != "" {
		row += "\n     " + s.Muted.Render(snippetText(mh.Snippet, max(20, width-8)))
	}
	return row
}

// --- right pane ---------------------------------------------------

func (m *meetingsScreen) summarySubtitle() string {
	if m.selectedSummary.MeetingID == "" {
		return ""
	}
	mh, ok := m.currentMatchedRow()
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d in summary", mh.SummaryCount)
}

func (m *meetingsScreen) transcriptSubtitle() string {
	mh, ok := m.currentMatchedRow()
	parts := []string{}
	if ok {
		parts = append(parts, fmt.Sprintf("%d in transcript", mh.TranscriptCount))
	}
	if len(m.matchedSegments) > 0 {
		parts = append(parts, fmt.Sprintf("hit %d/%d · n/N to jump", m.matchCursor+1, len(m.matchedSegments)))
	} else if len(m.selectedTranscript.Segments) > 0 {
		parts = append(parts, fmt.Sprintf("%d segments", len(m.selectedTranscript.Segments)))
	}
	return strings.Join(parts, " · ")
}

func (m *meetingsScreen) listSubtitle() string {
	if m.query != "" {
		return fmt.Sprintf("%d hits", len(m.matched))
	}
	return fmt.Sprintf("%d total", len(m.all))
}

func (m *meetingsScreen) renderSummaryBody(ctx screenCtx, width int) string {
	s := ctx.styles
	if m.currentMeetingID() == "" {
		return s.Muted.Render("select a meeting to preview")
	}
	if m.selectedSummary.MeetingID == "" {
		return s.Muted.Render("loading summary…")
	}
	tokens := queryTokens(m.query)
	var lines []string
	if m.selectedSummary.ShortSummary != "" {
		lines = append(lines, highlightString(s, m.selectedSummary.ShortSummary, tokens, -1, -1))
	} else {
		lines = append(lines, s.Muted.Render("(no short summary)"))
	}
	if len(m.selectedSummary.Decisions) > 0 {
		lines = append(lines, "", s.PanelTitle.Render("decisions"))
		for _, d := range m.selectedSummary.Decisions {
			lines = append(lines, "  • "+highlightString(s, d.Text, tokens, -1, -1))
		}
	}
	if len(m.selectedSummary.ActionItems) > 0 {
		lines = append(lines, "", s.PanelTitle.Render("actions"))
		for _, a := range m.selectedSummary.ActionItems {
			row := "  • " + highlightString(s, a.Text, tokens, -1, -1)
			if a.Owner != "" {
				row += "  " + s.Muted.Render("("+a.Owner+")")
			}
			lines = append(lines, row)
		}
	}
	if len(m.selectedSummary.Risks) > 0 {
		lines = append(lines, "", s.PanelTitle.Render("risks"))
		for _, r := range m.selectedSummary.Risks {
			lines = append(lines, "  • "+highlightString(s, r.Text, tokens, -1, -1))
		}
	}
	_ = width
	return strings.Join(lines, "\n")
}

func (m *meetingsScreen) renderTranscriptBody(ctx screenCtx, width, height int) string {
	s := ctx.styles
	if m.currentMeetingID() == "" {
		return s.Muted.Render("select a meeting to preview")
	}
	segs := m.selectedTranscript.Segments
	if len(segs) == 0 {
		if m.selectedTranscript.MeetingID == "" {
			return s.Muted.Render("loading transcript…")
		}
		return s.Muted.Render("no transcript yet — run a pipeline job")
	}
	// Each segment uses 2 lines (header + body). Compute how many fit.
	innerH := height - 4
	if innerH < 4 {
		innerH = 4
	}
	perSeg := 2
	segLimit := innerH / perSeg
	if segLimit < 1 {
		segLimit = 1
	}
	start := m.transcriptScroll
	if start > len(segs)-segLimit {
		start = len(segs) - segLimit
	}
	if start < 0 {
		start = 0
	}
	end := start + segLimit
	if end > len(segs) {
		end = len(segs)
	}
	tokens := queryTokens(m.query)
	activeSeg := -1
	if m.matchCursor < len(m.matchedSegments) {
		activeSeg = m.matchedSegments[m.matchCursor]
	}
	speakerStyle := map[string]int{}
	next := 0
	var rows []string
	for i := start; i < end; i++ {
		seg := segs[i]
		idx, ok := speakerStyle[seg.SpeakerID]
		if !ok {
			idx = next % 3
			speakerStyle[seg.SpeakerID] = idx
			next++
		}
		spStyle := s.SpeakerA
		switch idx {
		case 1:
			spStyle = s.SpeakerB
		case 2:
			spStyle = s.SpeakerC
		}
		header := fmt.Sprintf("%s  %s  %s",
			s.Muted.Render(fmt.Sprintf("[%s]", formatSec(seg.StartSec))),
			spStyle.Render(seg.Speaker),
			s.Citation.Render(seg.ID))
		if i == activeSeg {
			header = s.HighlightActive.Render(" ▸ ") + " " + header
		} else {
			header = "    " + header
		}
		body := "      " + highlightString(s, seg.Text, tokens, i, activeSeg)
		rows = append(rows, header, body)
	}
	footer := ""
	if start > 0 || end < len(segs) {
		footer = "\n" + s.Muted.Render(fmt.Sprintf("showing %d–%d of %d", start+1, end, len(segs)))
	}
	_ = width
	return strings.Join(rows, "\n") + footer
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

func fitWidth(s string, w int) string {
	return fit(s, w)
}

// snippetText strips FTS5 '**' delimiters (we apply our own highlight)
// and clamps length.
func snippetText(snip string, maxLen int) string {
	snip = strings.ReplaceAll(snip, "**", "")
	if len(snip) > maxLen {
		snip = snip[:maxLen-1] + "…"
	}
	return snip
}

// truncateAnsi is a no-op for now: the row may contain ANSI escapes
// from lipgloss styles so we don't want to byte-slice into them. The
// outer panel already clips visually.
func truncateAnsi(s string, _ int) string {
	return s
}

// quiet unused-import warnings if any helpers are removed later.
var _ = time.Now

func deleteMeetingCmd(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		err := ctx.client.DeleteMeeting(ctx.ctx, id)
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "meeting deleted"}
	}
}

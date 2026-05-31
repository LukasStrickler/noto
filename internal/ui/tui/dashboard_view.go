package tui

import (
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

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

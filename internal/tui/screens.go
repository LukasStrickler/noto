package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)


func (m AppModel) renderActive() string {
	width := m.contentWidth()
	height := m.contentHeight()
	switch m.UI.Active {
	case ScreenDashboard:
		return m.dashboardView(width, height)
	case ScreenMeetings:
		return m.meetingsView(width, height)
	case ScreenSearch:
		return m.searchView(width, height)
	case ScreenDetail:
		return m.detailView(width, height)
	case ScreenTranscript:
		return m.transcriptView(width, height)
	default:
		return m.dashboardView(width, height)
	}
}


func (m AppModel) dashboardView(width int, height int) string {
	if m.compactLayout() {
		top := clamp(height/2, 10, height-9)
		return lipgloss.JoinVertical(lipgloss.Left,
			m.dashboardCompact(width, top),
			m.dashboardBottomBar(width, height-top-1),
		)
	}

	leftWidth := clamp(width*38/100, 40, min(58, width/2-2))
	rightWidth := width - leftWidth - 1
	rightTopHeight := clamp(height*40/100, 10, height-12)
	rightBottomHeight := height - rightTopHeight - 1

	left := m.dashboardRecentMeetings(leftWidth, height)
	rightTop := m.dashboardRecordingState(rightWidth, rightTopHeight)
	rightBottom := m.dashboardStats(rightWidth, rightBottomHeight)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		left,
		" ",
		lipgloss.JoinVertical(lipgloss.Left, rightTop, rightBottom),
	)
}

func (m AppModel) dashboardCompact(width int, height int) string {
	body := m.dashboardRecordingState(width, height)
	return Panel{Title: "dashboard", Width: width, Height: height, Focused: true, Body: body}.Render()
}

func (m AppModel) dashboardRecentMeetings(width int, height int) string {
	recent := m.appRecentMeetings(5)
	if len(recent) == 0 {
		body := styles.Muted.Render("No meetings yet. Press r to record or i to import.")
		return Panel{Title: "recent", Width: width, Height: height, Focused: true, Body: body}.Render()
	}

	var b strings.Builder
	for i, mtg := range recent {
		style := styles.Row
		prefix := " "
		if i == m.UI.SelectedMeeting && m.UI.Active == ScreenDashboard {
			style = styles.RowSelected
			prefix = ">"
		}
		badge := mtg.StatusBadge()
		duration := mtg.Duration
		if mtg.Recording {
			duration = mtg.ElapsedFormatted()
		}
		line := fmt.Sprintf("%s%s  %s  %s", prefix, fit(mtg.Title, 28), duration, badge)
		b.WriteString(style.Width(width-4).Render(fit(line, width-6)))
		b.WriteString("\n")
	}
	body := b.String()
	return Panel{Title: "recent", Subtitle: fmt.Sprintf("%d total", len(m.App.Meetings)), Width: width, Height: height, Focused: true, Body: body}.Render()
}

func (m AppModel) dashboardRecordingState(width int, height int) string {
	bp := m.appBubblePup()
	if bp.Recording {
		body := RenderBubblePup(bp, width)
		return Panel{Title: "recording", Width: width, Height: height, Focused: true, Body: body}.Render()
	}

	state := "idle"
	if !m.speechConfigured() {
		state = "blocked"
	}
	lines := []string{
		"state   " + state,
		"provider  " + m.App.Config.Routing.SpeechProvider,
	}
	if !m.speechConfigured() {
		lines = append(lines, "", "configure a speech provider to enable recording")
	}
	body := detailLines(width-4, lines)
	return Panel{Title: "recording", Subtitle: state, Width: width, Height: height, Focused: false, Body: body}.Render()
}

func (m AppModel) dashboardStats(width int, height int) string {
	today := m.appTodayStats()
	lines := []string{
		fmt.Sprintf("today   %d meetings   %d actions   %d decisions", today.Meetings, today.Actions, today.Decisions),
		fmt.Sprintf("index   %s", m.App.Storage.Index),
	}
	body := detailLines(width-4, lines)
	return Panel{Title: "today", Width: width, Height: height, Focused: false, Body: body}.Render()
}

func (m AppModel) dashboardBottomBar(width int, height int) string {
	var b strings.Builder
	b.WriteString(m.dashboardRecordingBadge())
	b.WriteString("   ")
	b.WriteString(m.dashboardIndexBadge())
	b.WriteString("\n")
	b.WriteString(styles.Muted.Render("r record   i import   / search   : command   ? help   q quit"))
	body := b.String()
	return lipgloss.NewStyle().Width(width).Height(height).Foreground(defaultTheme.Muted).Render(body)
}

func (m AppModel) dashboardRecordingBadge() string {
	bp := m.appBubblePup()
	if bp.Recording {
		pulse := "●"
		if !bp.IsPulsing() {
			pulse = "○"
		}
		return styles.Danger.Render(pulse) + " " + styles.HeaderStrong.Render(bp.ElapsedFormatted())
	}
	return styles.Muted.Render("○ idle")
}

func (m AppModel) dashboardIndexBadge() string {
	idx := m.App.Storage.Index
	if idx == "clean" {
		return styles.Success.Render(idx)
	}
	return styles.Warning.Render(idx)
}


func (m AppModel) meetingsView(width int, height int) string {
	filterWidth := min(40, width/3)
	listWidth := width - filterWidth - 1

	if m.compactLayout() {
		return m.meetingsListBody(width, height, false)
	}

	filterPanel := m.meetingsFilterPanel(filterWidth, height)
	listPanel := m.meetingsListPanel(listWidth, height)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		filterPanel,
		" ",
		listPanel,
	)
}

func (m AppModel) meetingsFilterPanel(width int, height int) string {
	query := m.UI.SearchQuery
	if query == "" {
		query = "_"
	}
	input := styles.Input.Width(max(8, width-4)).Render(">" + query)
	body := input + "\n" + styles.Muted.Render("/ filter   s sort   d delete   n new")
	return Panel{Title: "filter", Width: width, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: body}.Render()
}

func (m AppModel) meetingsListPanel(width int, height int) string {
	body := m.meetingsListBody(width, height, true)
	return Panel{Title: "meetings", Width: width, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: body}.Render()
}

func (m AppModel) meetingsListBody(width int, height int, showHeader bool) string {
	meetings := m.appFilteredMeetings()
	if len(meetings) == 0 {
		return styles.Muted.Render("No meetings match the filter.")
	}

	vp := m.appMeetingsViewport(len(meetings), height)
	start, end := vp.VisibleRange()

	var b strings.Builder
	if showHeader {
		header := styles.Muted.Render("  TITLE              DUR   DATE     D    A    R")
		b.WriteString(header)
		b.WriteString("\n")
	}

	for i := start; i < end && i < len(meetings); i++ {
		mtg := meetings[i]
		style := styles.Row
		prefix := " "
		if i == vp.Selected {
			style = styles.RowSelected
			prefix = ">"
		}
		title := fit(mtg.Title, 18)
		date := mtg.Date
		dCount := fmt.Sprintf("%d", mtg.DecisionCount())
		aCount := fmt.Sprintf("%d", mtg.ActionCount())
		rCount := fmt.Sprintf("%d", mtg.RiskCount())
		line := fmt.Sprintf("%s%-18s %5s  %-9s %3s %3s %3s", prefix, title, mtg.Duration, date, dCount, aCount, rCount)
		b.WriteString(style.Width(width-4).Render(fit(line, width-6)))
		b.WriteString("\n")
	}

	return b.String()
}

func (m AppModel) appMeetingsViewport(totalItems int, visibleHeight int) ViewportComponent {
	itemHeight := 1
	vp := NewViewportComponent(totalItems, visibleHeight, itemHeight)
	vp.Offset = clamp(m.UI.SelectedMeeting-itemHeight+1, 0, max(0, totalItems-visibleHeight))
	vp.Selected = m.UI.SelectedMeeting
	return vp
}


func (m AppModel) detailView(width int, height int) string {
	meeting := m.selectedMeeting()
	if meeting == nil {
		return Panel{Title: "detail", Width: width, Height: height, Focused: true, Body: styles.Muted.Render("No meeting selected.")}.Render()
	}

	if m.compactLayout() {
		return Panel{Title: meeting.Title, Width: width, Height: height, Focused: true, Body: m.detailBody(width, meeting)}.Render()
	}

	if width >= 120 {
		leftWidth, rightWidth := splitWidths(width)
		left := Panel{Title: "summary", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.detailBody(leftWidth, meeting)}.Render()
		right := Panel{Title: "evidence", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.detailEvidenceBody(rightWidth, meeting)}.Render()
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	}

	return Panel{Title: fit(meeting.Title, width-4), Width: width, Height: height, Focused: true, Body: m.detailBody(width, meeting)}.Render()
}

func (m AppModel) detailBody(width int, meeting *MeetingFixture) string {
	var b strings.Builder

	b.WriteString(styles.HeaderStrong.Render(meeting.Title))
	b.WriteString("\n")
	meta := fmt.Sprintf("%s   %s   %d speakers", meeting.Date, meeting.Duration, meeting.Speakers)
	b.WriteString(styles.Muted.Render(meta))
	b.WriteString("\n\n")
	b.WriteString(styles.Muted.Render(meeting.Summary))
	b.WriteString("\n\n")

	b.WriteString(m.detailCollapsibleSection("decisions", meeting.Decisions, styles.Semantic.Decision, true, width))
	b.WriteString("\n")

	b.WriteString(m.detailCollapsibleSection("action items", meeting.Actions, styles.Semantic.Action, false, width))
	b.WriteString("\n")

	b.WriteString(m.detailCollapsibleSection("risks", meeting.Risks, styles.Semantic.Risk, false, width))
	b.WriteString("\n")

	b.WriteString(m.detailCollapsibleSection("open questions", meeting.OpenQuestions, styles.Semantic.Info, false, width))

	return b.String()
}

func (m AppModel) detailCollapsibleSection(title string, items []string, style lipgloss.Style, numbered bool, width int) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	prefix := "▸"
	if numbered {
		prefix = "▸"
	}
	b.WriteString(style.Render(prefix + " " + strings.ToUpper(title) + fmt.Sprintf(" (%d)", len(items))))
	b.WriteString("\n")
	for i, item := range items {
		num := ""
		if numbered {
			num = fmt.Sprintf("  %d. ", i+1)
		} else {
			num = "  • "
		}
		citation := ""
		if seg := m.itemSegment(meeting, item); seg != nil {
			citation = "  " + styles.Muted.Render("["+seg.ID+"]")
		}
		line := num + fit(item, max(8, width-20))
		b.WriteString(style.Width(width-4).Render(line) + citation + "\n")
	}
	return b.String()
}

func (m AppModel) itemSegment(meeting *MeetingFixture, itemText string) *TranscriptSegment {
	return nil
}

func (m AppModel) detailEvidenceBody(width int, meeting *MeetingFixture) string {
	if len(meeting.Segments) == 0 {
		return styles.Muted.Render("No transcript segments available.")
	}
	var b strings.Builder
	b.WriteString(styles.Label.Render("TRANSCRIPT"))
	b.WriteString("\n\n")
	for _, seg := range meeting.Segments {
		timeStyle := styles.Muted
		speakerStyle := styles.Semantic.SpeakerA
		segStyle := styles.Muted
		line := fmt.Sprintf("%s %s [%s] %s", timeStyle.Render(seg.Time), speakerStyle.Render(seg.Speaker), segStyle.Render(seg.Role), seg.ID)
		b.WriteString(style.Width(max(8, width-4)).Render(fit(line, width-6)))
		b.WriteString("\n")
		b.WriteString("  " + fit(seg.Text, width-8))
		b.WriteString("\n\n")
	}
	return b.String()
}


func (m AppModel) transcriptView(width int, height int) string {
	meeting := m.selectedMeeting()
	if meeting == nil {
		return Panel{Title: "transcript", Width: width, Height: height, Focused: true, Body: styles.Muted.Render("No meeting selected.")}.Render()
	}

	if m.compactLayout() {
		return Panel{Title: "transcript", Subtitle: meeting.Title, Width: width, Height: height, Focused: true, Body: m.transcriptBody(width, meeting)}.Render()
	}

	railWidth := clamp(width/5, 18, 28)
	bodyWidth := width - railWidth - 1

	rail := m.transcriptTimeline(railWidth, height, meeting)
	body := m.transcriptBody(bodyWidth, meeting)

	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "timeline", Width: railWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: rail}.Render(),
		" ",
		Panel{Title: "transcript", Subtitle: meeting.Title, Width: bodyWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: body}.Render(),
	)
}

func (m AppModel) transcriptTimeline(width int, height int, meeting *MeetingFixture) string {
	if meeting == nil || len(meeting.Segments) == 0 {
		return styles.Muted.Render("(no segments)")
	}
	vp := m.appTranscriptViewport(len(meeting.Segments), height)
	start, end := vp.VisibleRange()

	var b strings.Builder
	for i := start; i < end && i < len(meeting.Segments); i++ {
		seg := meeting.Segments[i]
		style := styles.Row
		if i == vp.Selected {
			style = styles.RowSelected
		}
		line := fit(seg.Time+" "+seg.ID, width-4)
		b.WriteString(style.Width(width-2).Render(line))
		b.WriteString("\n")
	}
	return b.String()
}

func (m AppModel) transcriptBody(width int, meeting *MeetingFixture) string {
	if meeting == nil || len(meeting.Segments) == 0 {
		return styles.Muted.Render("No transcript segments.")
	}
	vp := m.appTranscriptViewport(len(meeting.Segments), m.contentHeight())
	start, end := vp.VisibleRange()

	var b strings.Builder
	for i := start; i < end && i < len(meeting.Segments); i++ {
		seg := meeting.Segments[i]

		timeStr := styles.Muted.Render("[" + seg.Time + "]")
		speakerStr := m.semanticSpeakerStyle(seg.Speaker).Render(seg.Speaker)
		roleStr := styles.Muted.Render("[" + seg.Role + "]")
		segStr := styles.Muted.Render("  " + seg.ID)

		b.WriteString(timeStr)
		b.WriteString(" ")
		b.WriteString(speakerStr)
		b.WriteString(" ")
		b.WriteString(roleStr)
		b.WriteString(segStr)
		b.WriteString("\n")

		b.WriteString("  " + fit(seg.Text, max(8, width-8)))
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m AppModel) semanticSpeakerStyle(speaker string) lipgloss.Style {
	switch hashSpeaker(speaker) % 3 {
	case 0:
		return styles.Semantic.SpeakerA
	case 1:
		return styles.Semantic.SpeakerB
	default:
		return styles.Semantic.SpeakerC
	}
}

func hashSpeaker(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	return h
}

func (m AppModel) appTranscriptViewport(totalItems int, visibleHeight int) ViewportComponent {
	itemHeight := 3
	vp := NewViewportComponent(totalItems, visibleHeight, itemHeight)
	vp.Offset = clamp(m.UI.SelectedResult/itemHeight, 0, max(0, totalItems-visibleHeight/itemHeight))
	vp.Selected = m.UI.SelectedResult
	return vp
}


func (m AppModel) searchView(width int, height int) string {
	showEvidence := width >= 120

	if m.compactLayout() {
		return Panel{Title: "search", Subtitle: "evidence", Width: width, Height: height, Focused: true, Body: m.searchBody(width, height, false)}.Render()
	}

	if showEvidence {
		leftWidth, rightWidth := splitWidths(width)
		left := Panel{Title: "search", Subtitle: "results", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.searchBody(leftWidth, height, false)}.Render()
		right := Panel{Title: "evidence", Subtitle: "selected", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.searchEvidenceBody(rightWidth)}.Render()
		return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	}

	leftWidth, rightWidth := splitWidths(width)
	left := Panel{Title: "search", Subtitle: "results", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.searchBody(leftWidth, height, false)}.Render()
	right := Panel{Title: "preview", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.searchPreviewBody(rightWidth)}.Render()
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func (m AppModel) searchBody(width int, height int, showEvidence bool) string {
	var b strings.Builder

	query := m.UI.SearchQuery
	if query == "" {
		query = "_"
	}
	inputStyle := styles.Input.Width(max(12, width-4))
	b.WriteString(inputStyle.Render("> " + query))
	b.WriteString("\n")
	b.WriteString(styles.Muted.Render("/ filter   enter open   c copy citation   esc close"))
	b.WriteString("\n\n")

	results := m.filteredSearchResults()
	if len(results) == 0 {
		b.WriteString(styles.Muted.Render("No matching transcript segments."))
		return b.String()
	}

	vp := m.appSearchViewport(len(results), height-8)
	start, end := vp.VisibleRange()

	for i := start; i < end && i < len(results); i++ {
		result := results[i]
		style := styles.Row
		prefix := " "
		if i == vp.Selected {
			style = styles.RowSelected
			prefix = ">"
		}

		timeStr := fit(result.Segment.Time, 8)
		speakerStr := fit(result.Segment.Speaker, 12)
		meetingStr := fit(result.MeetingTitle, 20)
		textStr := fit(result.Segment.Text, max(16, width-50))

		line := fmt.Sprintf("%s%-8s %-12s %-20s %s", prefix, timeStr, speakerStr, meetingStr, textStr)
		b.WriteString(style.Width(width-4).Render(fit(line, width-6)))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(styles.Muted.Render(fmt.Sprintf("%d matches in %d meetings", len(results), m.uniqueMeetingCount(results))))

	return b.String()
}

func (m AppModel) searchEvidenceBody(width int) string {
	results := m.filteredSearchResults()
	if len(results) == 0 {
		return detailLines(width-4, []string{"No selected result.", "Type to search, Enter to open."})
	}
	result := results[clamp(m.UI.SelectedResult, 0, len(results)-1)]
	lines := []string{
		"meeting    " + result.MeetingTitle,
		"segment    " + result.Segment.ID,
		"time       " + result.Segment.Time,
		"speaker    " + result.Segment.Speaker + " [" + result.Segment.Role + "]",
		"",
		result.Segment.Text,
		"",
		"citation   " + citationFor(result),
	}
	return detailLines(width-4, lines)
}

func (m AppModel) searchPreviewBody(width int) string {
	results := m.filteredSearchResults()
	if len(results) == 0 {
		return styles.Muted.Render("No preview available.")
	}
	result := results[clamp(m.UI.SelectedResult, 0, len(results)-1)]
	var b strings.Builder
	b.WriteString(styles.Label.Render(result.MeetingTitle))
	b.WriteString("\n\n")
	b.WriteString(styles.Muted.Render("[" + result.Segment.Time + "]"))
	b.WriteString(" ")
	b.WriteString(styles.Semantic.SpeakerA.Render(result.Segment.Speaker))
	b.WriteString(" ")
	b.WriteString(styles.Muted.Render("[" + result.Segment.Role + "]"))
	b.WriteString("\n\n")
	b.WriteString(fit(result.Segment.Text, max(8, width-8)))
	b.WriteString("\n\n")
	b.WriteString(styles.Muted.Render("seg: " + result.Segment.ID))
	return b.String()
}

func (m AppModel) appSearchViewport(totalItems int, visibleHeight int) ViewportComponent {
	itemHeight := 1
	vp := NewViewportComponent(totalItems, visibleHeight, itemHeight)
	vp.Offset = clamp(m.UI.SelectedResult-itemHeight+1, 0, max(0, totalItems-visibleHeight))
	vp.Selected = m.UI.SelectedResult
	return vp
}

func (m AppModel) uniqueMeetingCount(results []SearchResult) int {
	seen := make(map[string]bool)
	for _, r := range results {
		seen[r.MeetingID] = true
	}
	return len(seen)
}


func (m AppModel) appRecentMeetings(n int) []MeetingFixture {
	all := m.App.Meetings
	if len(all) <= n {
		return all
	}
	return all[:n]
}

func (m AppModel) appFilteredMeetings() []MeetingFixture {
	query := strings.ToLower(m.UI.SearchQuery)
	if query == "" || query == "_" {
		return m.App.Meetings
	}
	var result []MeetingFixture
	for _, mtg := range m.App.Meetings {
		if strings.Contains(strings.ToLower(mtg.Title), query) {
			result = append(result, mtg)
		}
	}
	return result
}

func (m AppModel) selectedMeeting() *MeetingFixture {
	if len(m.App.Meetings) == 0 {
		return nil
	}
	idx := clamp(m.UI.SelectedMeeting, 0, len(m.App.Meetings)-1)
	return &m.App.Meetings[idx]
}

func (m AppModel) appBubblePup() BubblePupState {
	bp := NewBubblePupState()
	if m.App.Recorder.State == "recording" {
		bp.Recording = true
		bp.StartTime = now()
		bp.Elapsed = bp.Elapsed
	}
	bp.MicLevel = m.App.Recorder.MicDB
	bp.SpeakerLevel = m.App.Recorder.ParticipantsDB
	bp.AmbientLevel = -50
	bp.PulseFrame = bp.PulseFrameCycle()
	return bp
}

type todayStats struct {
	Meetings  int
	Actions   int
	Decisions int
}

func (m AppModel) appTodayStats() todayStats {
	stats := todayStats{}
	for _, mtg := range m.App.Meetings {
		if mtg.Date == "2026-04-24" || mtg.Date == "2026-04-23" {
			stats.Meetings++
			stats.Decisions += mtg.DecisionCount()
			stats.Actions += mtg.ActionCount()
		}
	}
	if stats.Meetings == 0 {
		stats.Meetings = len(m.App.Meetings)
		for _, mtg := range m.App.Meetings {
			stats.Decisions += mtg.DecisionCount()
			stats.Actions += mtg.ActionCount()
		}
	}
	return stats
}

func (m MeetingFixture) StatusBadge() string {
	switch m.Status {
	case "summarized":
		return styles.Success.Render("summarized")
	case "todo", "pending":
		return styles.Warning.Render("pending")
	case "recorded":
		return styles.Info.Render("recorded")
	default:
		return styles.Muted.Render(m.Status)
	}
}

func (m MeetingFixture) DecisionCount() int {
	if m.Decisions == nil {
		return 0
	}
	return len(m.Decisions)
}

func (m MeetingFixture) ActionCount() int {
	if m.Actions == nil {
		return 0
	}
	return len(m.Actions)
}

func (m MeetingFixture) RiskCount() int {
	if m.Risks == nil {
		return 0
	}
	return len(m.Risks)
}

func (m MeetingFixture) ElapsedFormatted() string {
	return "00:00"
}
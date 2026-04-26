package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/providers"
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
		return m.searchWorkspaceView(width, height)
	case ScreenRecorder:
		return m.recorderView(width, height)
	case ScreenDetail:
		return m.detailView(width, height)
	case ScreenTranscript:
		return m.transcriptView(width, height)
	case ScreenProviders:
		return m.providersView(width, height)
	case ScreenStorage:
		return m.storageView(width, height)
	case ScreenSettings:
		return m.settingsView(width, height)
	default:
		return m.dashboardView(width, height)
	}
}

func (m AppModel) dashboardView(width int, height int) string {
	if m.compactLayout() {
		top := clamp(height/2, 9, height-8)
		return lipgloss.JoinVertical(lipgloss.Left,
			m.meetingList(width, top, "meetings"),
			m.opsPanel(width, height-top-1),
		)
	}
	leftWidth := m.dashboardListWidth()
	rightWidth := width - leftWidth - 1
	topHeight := clamp(height*2/3, 11, height-8)
	rightTop := m.meetingPreview(rightWidth, topHeight)
	rightBottom := lipgloss.JoinHorizontal(lipgloss.Top,
		m.jobsPanel(rightWidth/2, height-topHeight-1),
		" ",
		m.healthPanel(rightWidth-rightWidth/2-1, height-topHeight-1),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.meetingList(leftWidth, height, "meetings"),
		" ",
		lipgloss.JoinVertical(lipgloss.Left, rightTop, rightBottom),
	)
}

func (m AppModel) meetingsView(width int, height int) string {
	if m.compactLayout() {
		return m.meetingList(width, height, "meetings")
	}
	leftWidth, rightWidth := splitWidths(width)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		m.meetingList(leftWidth, height, "meetings"),
		" ",
		m.meetingPreview(rightWidth, height),
	)
}

func (m AppModel) searchWorkspaceView(width int, height int) string {
	results := m.filteredSearchResults()
	if m.compactLayout() {
		return Panel{Title: "search", Subtitle: "local evidence", Width: width, Height: height, Focused: true, Body: m.searchResultsBody(width, results)}.Render()
	}
	leftWidth, rightWidth := splitWidths(width)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "search", Subtitle: "local evidence", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.searchResultsBody(leftWidth, results)}.Render(),
		" ",
		Panel{Title: "evidence", Subtitle: "selected segment", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.searchEvidenceBody(rightWidth, results)}.Render(),
	)
}

func (m AppModel) searchResultsBody(width int, results []SearchResult) string {
	var b strings.Builder
	query := m.UI.SearchQuery
	if query == "" {
		query = "<type to filter>"
	}
	b.WriteString(renderInput("Query", query, false, width-4))
	b.WriteString("\n")
	b.WriteString(styles.Muted.Render("Type to filter  Enter open  C copy citation  Esc clear/back"))
	b.WriteString("\n\n")
	if len(results) == 0 {
		b.WriteString(styles.Muted.Render("No matching transcript segments."))
		return b.String()
	}
	rows := make([]TableRow, 0, len(results))
	for i, result := range results {
		rows = append(rows, TableRow{
			Selected: i == m.UI.SelectedResult,
			Cells: []string{
				result.Segment.Time,
				result.Segment.Speaker,
				result.MeetingTitle,
				result.Segment.Text,
			},
		})
	}
	b.WriteString(renderTable(width, rows, 8, 10, max(14, width/3), max(16, width/2)))
	return b.String()
}

func (m AppModel) searchEvidenceBody(width int, results []SearchResult) string {
	if len(results) == 0 {
		return detailLines(width-4, []string{"No selected result.", "Try provider, terminal, cost, or local_speaker."})
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

func (m AppModel) recorderView(width int, height int) string {
	ready := m.speechConfigured()
	status := "ready"
	if !ready {
		status = "blocked: missing STT key"
	}
	preflight := []string{
		"title       " + m.App.Recorder.Title,
		"state       " + status,
		"provider    " + m.App.Config.Routing.SpeechProvider,
		"permission  " + m.App.Recorder.Permission,
		"retention   " + m.App.Recorder.Retention,
		"pipeline    ingest -> transcribe -> summarize -> index",
	}
	if !ready {
		preflight = append(preflight, "", "missing     configure Mistral, AssemblyAI, or ElevenLabs before live capture.")
	}
	if m.compactLayout() {
		sources := m.recorderSources(width)
		lines := append(preflight, "")
		lines = append(lines, sources...)
		return Panel{Title: "recorder", Subtitle: "preflight", Width: width, Height: height, Focused: true, Body: detailLines(width-4, lines)}.Render()
	}
	leftWidth, rightWidth := splitWidths(width)
	sources := m.recorderSources(rightWidth)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "recorder", Subtitle: "preflight", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: detailLines(leftWidth-4, preflight)}.Render(),
		" ",
		Panel{Title: "sources", Subtitle: "meters", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: detailLines(rightWidth-4, sources)}.Render(),
	)
}

func (m AppModel) recorderSources(width int) []string {
	return []string{
		"source roles",
		"me/mic       -> local_speaker",
		"participants -> participants/system",
		"",
		renderMeter("me/mic       ", m.App.Recorder.MicDB, width-10),
		renderMeter("participants ", m.App.Recorder.ParticipantsDB, width-10),
	}
}

func (m AppModel) detailView(width int, height int) string {
	meeting := m.selectedMeetingFixture()
	if m.compactLayout() {
		return Panel{Title: meeting.Title, Width: width, Height: height, Focused: true, Body: m.meetingDetailBody(width, meeting)}.Render()
	}
	leftWidth, rightWidth := splitWidths(width)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "summary", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.meetingDetailBody(leftWidth, meeting)}.Render(),
		" ",
		Panel{Title: "evidence", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.evidenceBody(rightWidth, meeting)}.Render(),
	)
}

func (m AppModel) transcriptView(width int, height int) string {
	meeting := m.selectedMeetingFixture()
	if m.compactLayout() {
		return Panel{Title: "transcript", Width: width, Height: height, Focused: true, Body: m.transcriptBody(width, meeting)}.Render()
	}
	railWidth := clamp(width/4, 20, 30)
	bodyWidth := width - railWidth - 1
	var rail strings.Builder
	for i, seg := range meeting.Segments {
		style := styles.Row
		if i == m.UI.SelectedResult {
			style = styles.RowSelected
		}
		rail.WriteString(style.Width(railWidth - 4).Render(fit(seg.Time+" "+seg.ID, railWidth-8)))
		rail.WriteString("\n")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "timeline", Width: railWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: rail.String()}.Render(),
		" ",
		Panel{Title: "transcript", Width: bodyWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.transcriptBody(bodyWidth, meeting)}.Render(),
	)
}

func (m AppModel) providersView(width int, height int) string {
	if m.compactLayout() {
		return Panel{Title: "providers", Width: width, Height: height, Focused: true, Body: m.providerList(width)}.Render()
	}
	leftWidth, rightWidth := splitWidths(width)
	selected := m.selectedProviderSuite()
	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "providers", Subtitle: "speech-to-text / openrouter", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.providerList(leftWidth)}.Render(),
		" ",
		Panel{Title: "provider detail", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.providerDetail(rightWidth, selected)}.Render(),
	)
}

func (m AppModel) storageView(width int, height int) string {
	status := "verified"
	if !m.App.Storage.Verified {
		status = "warning"
	}
	lines := []string{
		"artifact root   " + m.App.Config.ArtifactRoot,
		"config dir      " + m.App.Config.ConfigDir,
		"schema          " + m.App.Storage.Schema,
		"checksums       " + m.App.Storage.Checksum,
		"index           " + m.App.Storage.Index,
		"meetings        " + fmt.Sprintf("%d fixture-backed", len(m.App.Meetings)),
		"status          " + status,
		"last verify     " + m.App.Storage.LastResult,
		"",
		"agent command   noto verify --json",
	}
	if m.App.Storage.Warning != "" {
		lines = append(lines, "warning         "+m.App.Storage.Warning)
	}
	return Panel{Title: "storage and verification", Width: width, Height: height, Focused: true, Body: detailLines(width-4, lines)}.Render()
}

func (m AppModel) settingsView(width int, height int) string {
	if m.compactLayout() {
		return Panel{Title: "settings", Width: width, Height: height, Focused: true, Body: m.settingsList(width)}.Render()
	}
	leftWidth, rightWidth := splitWidths(width)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		Panel{Title: "settings", Width: leftWidth, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: m.settingsList(leftWidth)}.Render(),
		" ",
		Panel{Title: "selection", Width: rightWidth, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.settingDetail(rightWidth)}.Render(),
	)
}

func (m AppModel) meetingList(width int, height int, title string) string {
	rows := make([]TableRow, 0, len(m.App.Meetings))
	for i, meeting := range m.App.Meetings {
		rows = append(rows, TableRow{Selected: i == m.UI.SelectedMeeting, Cells: []string{meeting.Title, meeting.Duration, meeting.Status}})
	}
	body := styles.Muted.Render("  / search evidence   enter open") + "\n" + renderTable(width, rows, max(18, width-26), 6, 12)
	if len(m.App.Meetings) == 0 {
		body = styles.Muted.Render("No meetings yet. Press r to record or i to import.")
	}
	return Panel{Title: title, Width: width, Height: height, Focused: m.UI.Focus == FocusPrimary, Body: body}.Render()
}

func (m AppModel) meetingPreview(width int, height int) string {
	meeting := m.selectedMeetingFixture()
	return Panel{Title: "evidence preview", Subtitle: meeting.Title, Width: width, Height: height, Focused: m.UI.Focus == FocusDetail, Body: m.meetingDetailBody(width, meeting)}.Render()
}

func (m AppModel) meetingDetailBody(width int, meeting MeetingFixture) string {
	lines := []string{
		meeting.Title,
		meeting.Date + "  " + meeting.Duration + "  " + fmt.Sprintf("%d speakers", meeting.Speakers),
		meeting.Summary,
		"",
		"Decisions",
	}
	lines = append(lines, prefixLines(meeting.Decisions, "  - ")...)
	lines = append(lines, "", "Actions")
	lines = append(lines, prefixLines(meeting.Actions, "  - ")...)
	lines = append(lines, "", "Risks")
	lines = append(lines, prefixLines(meeting.Risks, "  - ")...)
	lines = append(lines, "", "Files  "+strings.Join(meeting.Files, ", "))
	return detailLines(width-4, lines)
}

func (m AppModel) evidenceBody(width int, meeting MeetingFixture) string {
	lines := []string{"Transcript evidence", ""}
	for _, seg := range meeting.Segments {
		lines = append(lines, seg.ID+" "+seg.Time+" "+seg.Speaker+" ["+seg.Role+"]", "  "+seg.Text)
	}
	lines = append(lines, "", "Copy command  noto transcript --json "+meeting.ID)
	return detailLines(width-4, lines)
}

func (m AppModel) transcriptBody(width int, meeting MeetingFixture) string {
	var b strings.Builder
	for _, seg := range meeting.Segments {
		b.WriteString(styles.Label.Render(seg.Time + " " + seg.Speaker))
		b.WriteString(" ")
		b.WriteString(styles.Muted.Render("[" + seg.Role + "] " + seg.ID))
		b.WriteString("\n")
		b.WriteString("  " + fit(seg.Text, width-8))
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m AppModel) jobsPanel(width int, height int) string {
	var lines []string
	for _, job := range m.App.Jobs {
		lines = append(lines, job.Name+"  "+job.Status+"  "+job.Detail)
	}
	return Panel{Title: "jobs", Width: width, Height: height, Focused: m.UI.Focus == FocusJobs, Body: detailLines(width-4, lines)}.Render()
}

func (m AppModel) healthPanel(width int, height int) string {
	lines := []string{
		"recorder   " + m.App.Recorder.State,
		"storage    " + m.App.Storage.Index,
		"schema     " + m.App.Storage.Schema,
		"provider   " + fmt.Sprintf("%d missing", m.missingKeyCount()),
		"llm        openrouter",
	}
	return Panel{Title: "health", Width: width, Height: height, Focused: false, Body: detailLines(width-4, lines)}.Render()
}

func (m AppModel) opsPanel(width int, height int) string {
	return lipgloss.JoinVertical(lipgloss.Left, m.jobsPanel(width, height/2), m.healthPanel(width, height-height/2))
}

func (m AppModel) providerList(width int) string {
	var b strings.Builder
	b.WriteString(styles.Muted.Render("Speech-to-text"))
	b.WriteString("\n")
	for i, p := range m.providerRows() {
		if i == len(sortedByKind(m.App.Providers, providers.ProviderKindSpeech)) {
			b.WriteString("\n")
			b.WriteString(styles.Muted.Render("LLM via OpenRouter"))
			b.WriteString("\n")
		}
		status := m.App.Statuses[p.ID]
		selected := " "
		if p.ID == m.App.Config.Routing.SpeechProvider || p.ID == "openrouter" {
			selected = "*"
		}
		kind := "stt"
		if p.ID == "openrouter" {
			kind = "llm"
		}
		style := styles.Row
		if i == m.UI.SelectedProvider {
			style = styles.RowSelected
		}
		line := fmt.Sprintf("%s %-3s %-11s %-12s %s", selected, kind, p.ID, keyState(status), providerEnv(p.CredentialRef))
		b.WriteString(style.Width(width - 4).Render(fit(line, width-8)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m AppModel) providerDetail(width int, selected providers.ProviderSuite) string {
	status := m.App.Statuses[selected.ID]
	lines := []string{
		"id             " + selected.ID,
		"credential     " + selected.CredentialRef,
		"key status     " + keyState(status) + " (" + status.Source + ")",
		"env fallback   " + providerEnv(selected.CredentialRef),
		"model          " + firstModel(selected),
		"data leaves    " + boolText(selected.SendsRawAudioOffDevice),
		"benchmark      compare WER, DER/JER, latency, cost before defaulting",
		"notes          " + selected.Notes,
	}
	if selected.ID == "openrouter" {
		lines = append(lines, "", "OpenRouter model  "+m.App.Config.Routing.LLMModel, "Real LLM work is fixed to OpenRouter.")
	}
	return detailLines(width-4, lines)
}

func (m AppModel) settingsList(width int) string {
	rows := m.settingRows()
	tableRows := make([]TableRow, 0, len(rows))
	for i, row := range rows {
		action := "view"
		if row.Target != "" {
			action = "edit"
		}
		if row.Cycle {
			action = "cycle"
		}
		tableRows = append(tableRows, TableRow{Selected: i == m.UI.SelectedSetting, Cells: []string{row.Label, row.Value, action}})
	}
	return renderTable(width, tableRows, 17, max(16, width-34), 8)
}

func (m AppModel) settingDetail(width int) string {
	row := m.selectedSettingRow()
	lines := []string{"name      " + row.Label, "value     " + row.Value}
	if row.Target != "" {
		lines = append(lines, "action    Enter opens an in-TUI edit form.")
	} else if row.Cycle {
		lines = append(lines, "action    Space cycles this value.")
	} else {
		lines = append(lines, "action    read-only")
	}
	lines = append(lines, "", "Provider keys live in Providers. Config stores credential refs and routing only.")
	return detailLines(width-4, lines)
}

func (m AppModel) selectedMeetingFixture() MeetingFixture {
	if len(m.App.Meetings) == 0 {
		return MeetingFixture{Title: "No meeting", Status: "empty"}
	}
	return m.App.Meetings[clamp(m.UI.SelectedMeeting, 0, len(m.App.Meetings)-1)]
}

func (m AppModel) providerRows() []providers.ProviderSuite {
	rows := sortedByKind(m.App.Providers, providers.ProviderKindSpeech)
	for _, p := range sortedByKind(m.App.Providers, providers.ProviderKindLLM) {
		if p.ID == "openrouter" {
			rows = append(rows, p)
		}
	}
	return rows
}

func (m AppModel) selectedProviderSuite() providers.ProviderSuite {
	rows := m.providerRows()
	if len(rows) == 0 {
		return providers.ProviderSuite{ID: "none", DisplayName: "No providers"}
	}
	return rows[clamp(m.UI.SelectedProvider, 0, len(rows)-1)]
}

func (m AppModel) settingRows() []settingRow {
	return []settingRow{
		{Key: "artifact", Label: "Artifact root", Value: m.App.Config.ArtifactRoot, Target: EditArtifactRoot},
		{Key: "speech", Label: "Speech provider", Value: m.App.Config.Routing.SpeechProvider, Cycle: true},
		{Key: "llm_model", Label: "OpenRouter model", Value: m.App.Config.Routing.LLMModel, Target: EditOpenRouterModel},
		{Key: "retention", Label: "Retention", Value: "delete raw audio after valid transcript", Cycle: true},
		{Key: "config", Label: "Config dir", Value: m.App.Config.ConfigDir},
		{Key: "keys", Label: "Key storage", Value: "macOS Keychain + env fallback"},
	}
}

func (m AppModel) selectedSettingRow() settingRow {
	rows := m.settingRows()
	if len(rows) == 0 {
		return settingRow{}
	}
	return rows[clamp(m.UI.SelectedSetting, 0, len(rows)-1)]
}

func prefixLines(lines []string, prefix string) []string {
	if len(lines) == 0 {
		return []string{prefix + "none"}
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, prefix+line)
	}
	return out
}

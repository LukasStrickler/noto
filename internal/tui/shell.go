package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m AppModel) renderShell(content string) string {
	width, height := normalizedSize(m.UI.Width, m.UI.Height)
	headerHeight := 2
	bannerHeight := 0
	if m.UI.Banner != nil {
		bannerHeight = 1
	}
	footerHeight := 1
	bodyHeight := height - headerHeight - bannerHeight - footerHeight
	bodyWidth := width

	body := lipgloss.NewStyle().Width(width).Height(bodyHeight).Render(content)
	if !m.compactLayout() {
		sidebarWidth := m.sidebarWidth()
		bodyWidth = width - sidebarWidth - 1
		sidebar := m.renderSidebar(sidebarWidth, bodyHeight)
		main := lipgloss.NewStyle().Width(bodyWidth).Height(bodyHeight).Render(content)
		body = lipgloss.JoinHorizontal(lipgloss.Top, sidebar, " ", main)
	}
	modal := m.renderOverlay(bodyWidth, bodyHeight)

	parts := []string{m.renderHeader(width)}
	if m.UI.Banner != nil {
		parts = append(parts, renderBanner(*m.UI.Banner, width))
	}
	parts = append(parts, body, m.renderCommandBar(width))
	view := lipgloss.NewStyle().Width(width).Height(height).Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
	if modal != "" {
		view += modalOverlayCommands(modal, width, height)
	}
	return view
}

func (m AppModel) renderHeader(width int) string {
	keysMissing := m.missingKeyCount()
	providerState := "providers ok"
	if keysMissing > 0 {
		providerState = fmt.Sprintf("%d keys missing", keysMissing)
	}
	left := styles.HeaderStrong.Render("Noto")
	middleText := fmt.Sprintf("%s  rec %s  idx %s  jobs %s", activeLabel(m.UI.Active), m.App.Recorder.State, m.App.Storage.Index, m.jobSummary())
	middle := styles.HeaderMeta.Render(middleText)
	right := styles.HeaderMeta.Render(fmt.Sprintf("%s  stt %s  llm openrouter", providerState, m.App.Config.Routing.SpeechProvider))
	if width < 110 {
		middle = styles.HeaderMeta.Render(fmt.Sprintf("%s  %d meetings", activeLabel(m.UI.Active), len(m.App.Meetings)))
		right = styles.HeaderMeta.Render(fmt.Sprintf("keys %d  %s", keysMissing, m.App.Config.Routing.SpeechProvider))
	}
	gap := max(0, width-lipgloss.Width(left)-lipgloss.Width(middle)-lipgloss.Width(right))
	top := styles.Header.Width(width).Render(left + middle + strings.Repeat(" ", gap) + right)
	rule := styles.Rule.Width(width).Render(strings.Repeat("─", width))
	return lipgloss.JoinVertical(lipgloss.Left, top, rule)
}

func (m AppModel) renderSidebar(width int, height int) string {
	var b strings.Builder
	navIndex := 0
	for groupIndex, group := range navGroups() {
		if groupIndex > 0 {
			b.WriteString("\n")
		}
		b.WriteString(styles.Muted.Render(group.Label))
		b.WriteString("\n")
		for _, item := range group.Items {
			key := fmt.Sprintf("%d", navIndex+1)
			style := styles.Row
			if item.Screen == m.UI.Active {
				style = styles.RowSelected
			}
			if m.UI.Focus == FocusSidebar && navIndex == m.UI.SelectedNav {
				style = styles.RowSelected.BorderLeft(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(defaultTheme.Focused)
			}
			badge := item.Badge(m)
			line := fmt.Sprintf("%s %-11s %s", key, item.Label, badge)
			b.WriteString(style.Width(width - 4).Render(fit(line, width-6)))
			b.WriteString("\n")
			navIndex++
		}
	}
	b.WriteString("\n")
	b.WriteString(styles.Muted.Render("jobs"))
	b.WriteString("\n")
	for _, job := range m.App.Jobs {
		b.WriteString(styles.Row.Width(width - 4).Render(fit("  "+job.Name+" "+job.Status, width-8)))
		b.WriteString("\n")
	}
	return Panel{Title: "workspace", Width: width, Height: height, Focused: m.UI.Focus == FocusSidebar, Body: b.String()}.Render()
}

func (m AppModel) renderCommandBar(width int) string {
	chips := m.visibleActionChips()
	parts := make([]string, 0, len(chips))
	cursor := 1
	bounds := make([]ActionBound, 0, len(chips))
	for _, chip := range chips {
		text := plainChip(chip)
		chipWidth := lipgloss.Width(text)
		if cursor+chipWidth+1 > width {
			break
		}
		parts = append(parts, renderActionChip(chip))
		bounds = append(bounds, ActionBound{Action: chip.ID, X0: cursor, X1: cursor + chipWidth, Y: m.height() - 1})
		cursor += chipWidth + 1
	}
	// Bubble Tea models are value-oriented, so mouse hit testing recomputes these
	// bounds in actionAt; keeping the derivation here prevents drift.
	_ = bounds
	return styles.Header.Width(width).Render(" " + strings.Join(parts, " "))
}

func renderBanner(banner Banner, width int) string {
	style := styles.BannerInfo
	if banner.Kind == BannerWarn {
		style = styles.BannerWarn
	}
	if banner.Kind == BannerError {
		style = styles.BannerError
	}
	return style.Width(width).Render(" " + fit(banner.Message, width-2))
}

func renderActionChip(chip ActionChip) string {
	keyStyle := styles.ChipKey
	labelStyle := styles.ChipLabel
	frame := styles.Chip
	if !chip.Enabled {
		keyStyle = styles.ChipDisabled
		labelStyle = styles.ChipDisabled
		frame = styles.ChipDisabled
	}
	return frame.Render("[" + keyStyle.Render(chip.Key) + " " + labelStyle.Render(chip.Label) + "]")
}

func plainChip(chip ActionChip) string {
	return "[" + chip.Key + " " + chip.Label + "]"
}

func (m AppModel) renderOverlay(width int, height int) string {
	switch m.UI.Overlay.Kind {
	case OverlayHelp:
		return m.helpOverlay(width, height)
	case OverlayCommand:
		return m.commandOverlay(width, height)
	case OverlaySearch:
		return m.searchOverlay(width, height)
	case OverlayProviderKey, OverlaySettings:
		return m.formOverlay(width, height)
	case OverlayConfirm:
		return m.confirmOverlay(width, height)
	default:
		return ""
	}
}

func (m AppModel) helpOverlay(width int, height int) string {
	lines := []string{
		"Navigation",
		"  arrows/hjkl move focus and selected rows by pane",
		"  1-8 jump workspaces; tab cycles panes; q backs out",
		"",
		"Commands",
		"  / search evidence   : command palette   r recorder   p providers",
		"  enter opens selected context; space toggles/selects when visible",
		"",
		"Safety",
		"  Missing keys block live jobs only. Browsing, search, settings, and fixtures stay usable.",
	}
	return overlayContent("help", detailLines(width-8, lines))
}

func (m AppModel) commandOverlay(width int, height int) string {
	rows := m.commandRows()
	var b strings.Builder
	b.WriteString(renderInput("Command", m.UI.Overlay.Query, false, min(width-10, 70)))
	b.WriteString("\n\n")
	for i, row := range rows {
		style := styles.Row
		if i == m.UI.Overlay.Selected {
			style = styles.RowSelected
		}
		b.WriteString(style.Width(min(width-10, 70)).Render(fit(row.label+"  "+row.hint, min(width-14, 66))))
		b.WriteString("\n")
	}
	return overlayContent("command palette", b.String())
}

func (m AppModel) searchOverlay(width int, height int) string {
	results := m.filteredSearchResults()
	var b strings.Builder
	b.WriteString(renderInput("Search local evidence", m.UI.Overlay.Query, false, min(width-10, 80)))
	b.WriteString("\n")
	b.WriteString(styles.Muted.Render("Enter open  C copy citation  Esc close"))
	b.WriteString("\n\n")
	if len(results) == 0 {
		b.WriteString(styles.Muted.Render("No matching transcript segments."))
	} else {
		for i, result := range results {
			style := styles.Row
			if i == m.UI.Overlay.Selected {
				style = styles.RowSelected
			}
			line := fmt.Sprintf("%s  %s  %s  %s", result.Segment.Time, result.Segment.Speaker, result.MeetingTitle, result.Segment.Text)
			b.WriteString(style.Width(min(width-10, 96)).Render(fit(line, min(width-14, 92))))
			b.WriteString("\n")
		}
		selected := results[clamp(m.UI.Overlay.Selected, 0, len(results)-1)]
		b.WriteString("\n")
		b.WriteString(styles.Label.Render("evidence "))
		b.WriteString(styles.Muted.Render(selected.Segment.ID + " " + selected.Segment.Role))
		b.WriteString("\n")
		b.WriteString("  " + fit(selected.Segment.Text, min(width-14, 92)))
	}
	return overlayContent("search", b.String())
}

func (m AppModel) formOverlay(width int, height int) string {
	return overlayContent("edit", renderInput(m.UI.Overlay.Label, m.UI.Overlay.Buffer, m.UI.Overlay.Mask, min(width-10, 72))+
		"\n\n"+styles.Muted.Render("Enter save  Esc cancel  Backspace delete"))
}

func (m AppModel) confirmOverlay(width int, height int) string {
	body := styles.Title.Render(m.UI.Overlay.Label) + "\n\n" +
		renderActionChip(ActionChip{Key: "Y", Label: "Confirm", Enabled: true, ID: ActionRemove}) + " " +
		renderActionChip(ActionChip{Key: "N", Label: "Cancel", Enabled: true, ID: ActionCancel})
	return overlayContent("confirm", body)
}

func overlayContent(title string, body string) string {
	return sectionTitle(title) + "\n\n" + body
}

type navItem struct {
	Label  string
	Screen Screen
	Badge  func(AppModel) string
}

type navGroup struct {
	Label string
	Items []navItem
}

func navGroups() []navGroup {
	return []navGroup{
		{"daily", []navItem{
			{"Dashboard", ScreenDashboard, func(m AppModel) string { return m.App.Recorder.State }},
			{"Meetings", ScreenMeetings, func(m AppModel) string { return fmt.Sprintf("%d", len(m.App.Meetings)) }},
			{"Search", ScreenSearch, func(m AppModel) string { return fmt.Sprintf("%d hits", len(m.filteredSearchResults())) }},
			{"Recorder", ScreenRecorder, func(m AppModel) string { return m.recorderBadge() }},
			{"Transcript", ScreenTranscript, func(m AppModel) string { return "fixture" }},
		}},
		{"setup", []navItem{
			{"Providers", ScreenProviders, func(m AppModel) string { return fmt.Sprintf("%d missing", m.missingKeyCount()) }},
			{"Storage", ScreenStorage, func(m AppModel) string { return m.App.Storage.Index }},
			{"Settings", ScreenSettings, func(m AppModel) string { return "local" }},
		}},
	}
}

func navItems() []navItem {
	var items []navItem
	for _, group := range navGroups() {
		items = append(items, group.Items...)
	}
	return items
}

func navIndex(screen Screen) int {
	for i, item := range navItems() {
		if item.Screen == screen {
			return i
		}
	}
	return 0
}

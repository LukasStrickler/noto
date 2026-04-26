package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m AppModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyText := keyName(msg)
	if m.UI.Overlay.Kind != OverlayNone {
		return m.handleOverlayKey(msg, keyText), nil
	}
	if keyText == ":" {
		m.openOverlay(OverlayCommand)
		return m, nil
	}
	if m.UI.Active == ScreenSearch {
		if handled := m.handleSearchPageKey(msg, keyText); handled {
			return m, nil
		}
	}
	if action, ok := m.actionForKey(keyText); ok {
		return m, m.performAction(action)
	}
	switch keyText {
	case "ctrl+c":
		m.UI.Quitting = true
		return m, tea.Quit
	case "1", "2", "3", "4", "5", "6", "7", "8":
		m.jumpNumber(keyText)
	case "esc":
		if m.UI.Banner != nil {
			m.UI.Banner = nil
		} else if m.UI.Active != ScreenDashboard {
			m.setScreen(ScreenDashboard)
		}
	case "backspace":
		if m.UI.Active != ScreenDashboard {
			m.setScreen(m.UI.Previous)
		}
	case "tab":
		m.nextFocus()
	case "shift+tab":
		m.prevFocus()
	case "left", "h":
		m.moveFocusLeft()
	case "right", "l":
		m.moveFocusRight()
	case "g":
		m.UI.Focus = FocusSidebar
	case "d":
		m.setScreen(ScreenDetail)
	case "t":
		m.setScreen(ScreenTranscript)
	case ",":
		m.setScreen(ScreenSettings)
	case "enter", "o":
		m.enter()
	case "space":
		m.space()
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	}
	return m, nil
}

func (m *AppModel) handleSearchPageKey(msg tea.KeyMsg, keyText string) bool {
	switch keyText {
	case "ctrl+c":
		m.UI.Quitting = true
		return true
	case "enter":
		m.openSelectedSearchResult()
		return true
	case "up", "k":
		m.move(-1)
		return true
	case "down", "j":
		m.move(1)
		return true
	case "backspace":
		if len(m.UI.SearchQuery) > 0 {
			runes := []rune(m.UI.SearchQuery)
			m.UI.SearchQuery = string(runes[:len(runes)-1])
			m.syncSearch(m.UI.SearchQuery)
		}
		return true
	case "esc":
		if m.UI.SearchQuery != "" {
			m.UI.SearchQuery = ""
			m.syncSearch("")
			return true
		}
		m.setScreen(ScreenDashboard)
		return true
	case "/":
		m.UI.SearchQuery = ""
		m.syncSearch("")
		return true
	case "c":
		if isUpperRune(msg, 'C') {
			m.copyCitation()
			return true
		}
		m.appendSearchText(msg)
		return true
	case "q":
		if isUpperRune(msg, 'Q') {
			_ = m.back()
			return true
		}
		m.appendSearchText(msg)
		return true
	case "?":
		return false
	case "m":
		if isUpperRune(msg, 'M') {
			return false
		}
		m.appendSearchText(msg)
		return true
	default:
		if len(msg.Runes) > 0 {
			m.appendSearchText(msg)
			return true
		}
	}
	return false
}

func (m *AppModel) appendSearchText(msg tea.KeyMsg) {
	if len(msg.Runes) == 0 {
		return
	}
	m.UI.SearchQuery += string(msg.Runes)
	m.UI.SelectedResult = 0
	m.syncSearch(m.UI.SearchQuery)
}

func (m AppModel) handleOverlayKey(msg tea.KeyMsg, keyText string) tea.Model {
	switch keyText {
	case "esc":
		m.UI.Overlay = OverlayState{}
		return m
	case "ctrl+c":
		m.UI.Quitting = true
		return m
	case "enter":
		m.commitOverlay()
		return m
	case "up", "k":
		m.moveOverlay(-1)
		return m
	case "down", "j":
		m.moveOverlay(1)
		return m
	case "backspace":
		if len(m.UI.Overlay.Buffer) > 0 {
			runes := []rune(m.UI.Overlay.Buffer)
			m.UI.Overlay.Buffer = string(runes[:len(runes)-1])
		} else if len(m.UI.Overlay.Query) > 0 {
			runes := []rune(m.UI.Overlay.Query)
			m.UI.Overlay.Query = string(runes[:len(runes)-1])
		}
	case "c":
		if m.UI.Overlay.Kind == OverlaySearch && isUpperRune(msg, 'C') {
			results := m.filteredSearchResults()
			if len(results) > 0 {
				result := results[clamp(m.UI.Overlay.Selected, 0, len(results)-1)]
				m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Citation copied: " + citationFor(result)}
			}
			return m
		}
		m.appendOverlayText(msg)
	case "y":
		if m.UI.Overlay.Kind == OverlayConfirm {
			m.confirm()
			return m
		}
		m.appendOverlayText(msg)
	case "n", "q":
		if m.UI.Overlay.Kind == OverlayConfirm {
			m.UI.Overlay = OverlayState{}
			m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Cancelled."}
			return m
		}
		m.appendOverlayText(msg)
	default:
		m.appendOverlayText(msg)
	}
	return m
}

func isUpperRune(msg tea.KeyMsg, want rune) bool {
	return len(msg.Runes) == 1 && msg.Runes[0] == want
}

func (m *AppModel) appendOverlayText(msg tea.KeyMsg) {
	if len(msg.Runes) == 0 {
		return
	}
	text := string(msg.Runes)
	switch m.UI.Overlay.Kind {
	case OverlaySearch, OverlayCommand:
		m.UI.Overlay.Query += text
		m.UI.Overlay.Selected = 0
	case OverlayProviderKey, OverlaySettings:
		m.UI.Overlay.Buffer += text
	}
}

func (m AppModel) handleMouse(msg tea.MouseMsg) (AppModel, tea.Cmd) {
	if msg.Action != tea.MouseActionPress && msg.Action != tea.MouseActionMotion {
		return m, nil
	}
	if action, ok := m.actionAt(msg.X, msg.Y); ok {
		return m, m.performAction(action)
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if index, ok := m.sidebarNavIndexAt(msg.X, msg.Y); ok {
		m.setScreen(navItems()[index].Screen)
		return m, nil
	}
	if m.handleContentClick(msg.X, msg.Y) {
		return m, nil
	}
	return m, nil
}

func (m *AppModel) handleContentClick(x int, y int) bool {
	mainX, bodyY := m.mainOrigin()
	if x < mainX || y < bodyY {
		return false
	}
	localX := x - mainX
	row := y - bodyY
	switch m.UI.Active {
	case ScreenDashboard:
		leftWidth := m.dashboardListWidth()
		if m.compactLayout() || localX < leftWidth {
			return m.selectMeetingAtRow(row - 3)
		}
		m.UI.Focus = FocusDetail
		return true
	case ScreenMeetings:
		leftWidth, _ := splitWidths(m.contentWidth())
		if m.compactLayout() || localX < leftWidth {
			return m.selectMeetingAtRow(row - 3)
		}
		m.UI.Focus = FocusDetail
		return true
	case ScreenProviders:
		return m.selectProviderAtRow(row)
	case ScreenSettings:
		return m.selectSettingAtRow(row - 2)
	case ScreenSearch:
		return m.selectSearchResultAtRow(row - 6)
	case ScreenTranscript:
		return m.selectTranscriptAtRow(row - 2)
	}
	return false
}

func (m AppModel) sidebarNavIndexAt(x int, y int) (int, bool) {
	if m.compactLayout() || x < 0 || x >= m.sidebarWidth() {
		return 0, false
	}
	bodyStart := 2
	if m.UI.Banner != nil {
		bodyStart++
	}
	row := bodyStart + 2
	index := 0
	for groupIndex, group := range navGroups() {
		if groupIndex > 0 {
			row++
		}
		row++ // group label
		for range group.Items {
			if y == row {
				return index, true
			}
			index++
			row++
		}
	}
	return 0, false
}

func (m AppModel) mainOrigin() (int, int) {
	x := 0
	if !m.compactLayout() {
		x = m.sidebarWidth() + 1
	}
	y := 2
	if m.UI.Banner != nil {
		y++
	}
	return x, y
}

func (m AppModel) dashboardListWidth() int {
	width := m.contentWidth()
	if m.compactLayout() {
		return width
	}
	return clamp(width*38/100, 40, 58)
}

func (m *AppModel) selectMeetingAtRow(index int) bool {
	if index < 0 || index >= len(m.App.Meetings) {
		return false
	}
	m.UI.SelectedMeeting = index
	m.UI.Focus = FocusPrimary
	return true
}

func (m *AppModel) selectProviderAtRow(row int) bool {
	// Provider panel rows: border=0, title=1, speech label=2,
	// three STT providers=3..5, blank=6, LLM label=7, OpenRouter=8.
	var index int
	switch {
	case row >= 3 && row <= 5:
		index = row - 3
	case row == 8:
		index = 3
	default:
		return false
	}
	if index < 0 || index >= len(m.providerRows()) {
		return false
	}
	m.UI.SelectedProvider = index
	m.UI.Focus = FocusPrimary
	return true
}

func (m *AppModel) selectSettingAtRow(index int) bool {
	if index < 0 || index >= len(m.settingRows()) {
		return false
	}
	m.UI.SelectedSetting = index
	m.UI.Focus = FocusPrimary
	return true
}

func (m *AppModel) selectSearchResultAtRow(index int) bool {
	results := m.filteredSearchResults()
	if index < 0 || index >= len(results) {
		return false
	}
	m.UI.SelectedResult = index
	m.UI.Focus = FocusPrimary
	return true
}

func (m *AppModel) selectTranscriptAtRow(index int) bool {
	segments := m.selectedMeetingFixture().Segments
	if index < 0 || index >= len(segments) {
		return false
	}
	m.UI.SelectedResult = index
	m.UI.Focus = FocusPrimary
	return true
}

func keyName(msg tea.KeyMsg) string {
	value := msg.String()
	if len(msg.Runes) == 1 {
		value = string(msg.Runes[0])
	}
	if value == " " {
		return "space"
	}
	if len(value) == 1 {
		return strings.ToLower(value)
	}
	return strings.ToLower(value)
}

func (m *AppModel) setScreen(screen Screen) {
	if screen == "" {
		screen = ScreenDashboard
	}
	m.UI.Previous = m.UI.Active
	m.UI.Active = screen
	m.UI.SelectedNav = navIndex(screen)
	m.UI.Focus = FocusPrimary
	m.UI.Banner = nil
}

func (m *AppModel) jumpNumber(value string) {
	index := int(value[0] - '1')
	items := navItems()
	if index < 0 || index >= len(items) {
		return
	}
	m.setScreen(items[index].Screen)
}

func (m *AppModel) nextFocus() {
	order := m.focusOrder()
	for i, f := range order {
		if m.UI.Focus == f {
			m.UI.Focus = order[(i+1)%len(order)]
			return
		}
	}
	m.UI.Focus = FocusPrimary
}

func (m *AppModel) prevFocus() {
	order := m.focusOrder()
	for i, f := range order {
		if m.UI.Focus == f {
			m.UI.Focus = order[(i+len(order)-1)%len(order)]
			return
		}
	}
	m.UI.Focus = FocusPrimary
}

func (m *AppModel) moveFocusLeft() {
	switch m.UI.Focus {
	case FocusDetail:
		m.UI.Focus = FocusPrimary
	case FocusPrimary:
		if !m.compactLayout() {
			m.UI.Focus = FocusSidebar
		}
	case FocusJobs:
		m.UI.Focus = FocusPrimary
	default:
		m.prevFocus()
	}
}

func (m *AppModel) moveFocusRight() {
	switch m.UI.Focus {
	case FocusSidebar:
		m.UI.Focus = FocusPrimary
	case FocusPrimary:
		if hasDetailPane(m.UI.Active) {
			m.UI.Focus = FocusDetail
		} else if m.UI.Active == ScreenDashboard {
			m.UI.Focus = FocusJobs
		}
	case FocusJobs:
		m.UI.Focus = FocusDetail
	default:
		m.nextFocus()
	}
}

func (m AppModel) focusOrder() []Focus {
	if m.compactLayout() {
		return []Focus{FocusPrimary}
	}
	switch m.UI.Active {
	case ScreenDashboard:
		return []Focus{FocusSidebar, FocusPrimary, FocusDetail, FocusJobs}
	case ScreenMeetings, ScreenProviders, ScreenDetail, ScreenTranscript, ScreenSettings:
		return []Focus{FocusSidebar, FocusPrimary, FocusDetail}
	default:
		return []Focus{FocusSidebar, FocusPrimary}
	}
}

func (m *AppModel) move(delta int) {
	if m.UI.Focus == FocusSidebar {
		m.UI.SelectedNav = wrapIndex(m.UI.SelectedNav, delta, len(navItems()))
		return
	}
	switch m.UI.Active {
	case ScreenDashboard, ScreenMeetings, ScreenDetail:
		m.UI.SelectedMeeting = wrapIndex(m.UI.SelectedMeeting, delta, len(m.App.Meetings))
	case ScreenTranscript:
		m.UI.SelectedResult = wrapIndex(m.UI.SelectedResult, delta, len(m.selectedMeetingFixture().Segments))
	case ScreenProviders:
		m.UI.SelectedProvider = wrapIndex(m.UI.SelectedProvider, delta, len(m.providerRows()))
	case ScreenSearch:
		m.UI.SelectedResult = wrapIndex(m.UI.SelectedResult, delta, len(m.filteredSearchResults()))
	case ScreenSettings:
		m.UI.SelectedSetting = wrapIndex(m.UI.SelectedSetting, delta, len(m.settingRows()))
	}
}

func (m *AppModel) moveOverlay(delta int) {
	switch m.UI.Overlay.Kind {
	case OverlaySearch:
		m.UI.Overlay.Selected = clamp(m.UI.Overlay.Selected+delta, 0, len(m.filteredSearchResults())-1)
	case OverlayCommand:
		m.UI.Overlay.Selected = clamp(m.UI.Overlay.Selected+delta, 0, len(m.commandRows())-1)
	}
}

func hasDetailPane(screen Screen) bool {
	switch screen {
	case ScreenDashboard, ScreenMeetings, ScreenProviders, ScreenDetail, ScreenTranscript, ScreenSettings:
		return true
	default:
		return false
	}
}

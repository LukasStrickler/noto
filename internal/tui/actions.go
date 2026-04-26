package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/secrets"
)

func (m AppModel) actionForKey(key string) (ActionID, bool) {
	for _, chip := range m.contextActionChips() {
		if keyMatches(chip.Key, key) {
			return chip.ID, true
		}
	}
	for _, chip := range m.globalActionChips() {
		if keyMatches(chip.Key, key) {
			return chip.ID, true
		}
	}
	return "", false
}

func (m AppModel) actionAt(x int, y int) (ActionID, bool) {
	_, height := normalizedSize(m.UI.Width, m.UI.Height)
	if y != height-1 {
		return "", false
	}
	cursor := 1
	for _, chip := range m.visibleActionChips() {
		text := plainChip(chip)
		end := cursor + lipgloss.Width(text)
		if x >= cursor && x < end {
			return chip.ID, true
		}
		cursor = end + 1
	}
	return "", false
}

func (m AppModel) globalActionChips() []ActionChip {
	return []ActionChip{
		{Key: "?", Label: "Help", Enabled: true, ID: ActionHelp},
		{Key: "M", Label: "Menu", Enabled: true, ID: ActionMenu},
		{Key: "/", Label: "Search", Enabled: true, ID: ActionSearch},
		{Key: "R", Label: "Record", Enabled: true, ID: ActionRecord},
		{Key: "I", Label: "Import", Enabled: true, ID: ActionImport},
		{Key: "P", Label: "Providers", Enabled: true, ID: ActionProviders},
		{Key: "V", Label: "Verify", Enabled: true, ID: ActionVerify},
		{Key: "Q", Label: "Back", Enabled: true, ID: ActionBack},
	}
}

func (m AppModel) contextActionChips() []ActionChip {
	switch m.UI.Active {
	case ScreenProviders:
		selected := m.selectedProviderSuite()
		status := m.App.Statuses[selected.ID]
		return []ActionChip{
			{Key: "Space", Label: "Select", Enabled: selected.Kind == providers.ProviderKindSpeech, Reason: "OpenRouter is fixed for real LLM work; select a speech provider row.", ID: ActionSelect},
			{Key: "E", Label: "Edit Key", Enabled: true, ID: ActionEdit},
			{Key: "T", Label: "Test", Enabled: status.Configured, Reason: fmt.Sprintf("Provider key required: configure %s before testing.", selected.ID), ID: ActionTest},
			{Key: "X", Label: "Remove", Enabled: status.Configured, Reason: fmt.Sprintf("No stored key exists for %s.", selected.ID), ID: ActionRemove},
			{Key: "O", Label: "Model", Enabled: true, ID: ActionModel},
			{Key: "D", Label: "Details", Enabled: true, ID: ActionDetails},
		}
	case ScreenSettings:
		return []ActionChip{
			{Key: "Enter", Label: "Edit", Enabled: true, ID: ActionEdit},
			{Key: "Space", Label: "Cycle", Enabled: true, ID: ActionCycle},
			{Key: "S", Label: "Save", Enabled: true, ID: ActionSave},
			{Key: "Esc", Label: "Cancel", Enabled: true, ID: ActionCancel},
		}
	case ScreenRecorder:
		enabled := m.speechConfigured()
		return []ActionChip{
			{Key: "R", Label: "Start", Enabled: enabled, Reason: "Provider key required: configure Mistral, AssemblyAI, or ElevenLabs before recording/transcribing.", ID: ActionRecord},
			{Key: "P", Label: "Provider", Enabled: true, ID: ActionProviders},
			{Key: "Space", Label: "Toggle", Enabled: enabled, Reason: "Provider key required: configure a speech provider before capture toggles.", ID: ActionSelect},
			{Key: "Enter", Label: "Preflight", Enabled: true, ID: ActionPreflight},
		}
	case ScreenSearch:
		return []ActionChip{
			{Key: "Enter", Label: "Open", Enabled: len(m.filteredSearchResults()) > 0, Reason: "No search result is selected.", ID: ActionOpen},
			{Key: "C", Label: "Copy Citation", Enabled: len(m.filteredSearchResults()) > 0, Reason: "No citation is available until a result is selected.", ID: ActionCopyCitation},
			{Key: "Esc", Label: "Clear", Enabled: true, ID: ActionCancel},
		}
	default:
		return nil
	}
}

func (m AppModel) visibleActionChips() []ActionChip {
	chips := append([]ActionChip{}, m.contextActionChips()...)
	seen := map[ActionID]bool{}
	for _, chip := range chips {
		seen[chip.ID] = true
	}
	for _, chip := range m.globalActionChips() {
		if seen[chip.ID] {
			continue
		}
		chips = append(chips, chip)
		seen[chip.ID] = true
	}
	width := m.width()
	if width >= 100 {
		return chips
	}
	short := map[ActionID]string{
		ActionSearch:       "Find",
		ActionRecord:       "Rec",
		ActionImport:       "Imp",
		ActionProviders:    "Prov",
		ActionVerify:       "Ver",
		ActionSelect:       "Sel",
		ActionEdit:         "Edit",
		ActionRemove:       "Rm",
		ActionDetails:      "Info",
		ActionPreflight:    "Check",
		ActionCopyCitation: "Cite",
	}
	for i := range chips {
		if label, ok := short[chips[i].ID]; ok {
			chips[i].Label = label
		}
	}
	return fitActionChips(width, chips)
}

func fitActionChips(width int, chips []ActionChip) []ActionChip {
	if chipsWidth(chips) <= width-2 {
		return chips
	}
	dropOrder := []ActionID{ActionImport, ActionVerify, ActionMenu, ActionHelp, ActionDetails, ActionProviders}
	out := append([]ActionChip{}, chips...)
	for _, id := range dropOrder {
		if chipsWidth(out) <= width-2 {
			break
		}
		out = removeChip(out, id)
	}
	return out
}

func chipsWidth(chips []ActionChip) int {
	total := 1
	for _, chip := range chips {
		total += len([]rune(plainChip(chip))) + 1
	}
	return total
}

func removeChip(chips []ActionChip, id ActionID) []ActionChip {
	out := chips[:0]
	for _, chip := range chips {
		if chip.ID == id && chip.ID != ActionBack {
			continue
		}
		out = append(out, chip)
	}
	return out
}

func (m *AppModel) performAction(id ActionID) tea.Cmd {
	chip, exists := m.actionByID(id)
	if exists && !chip.Enabled {
		m.UI.Banner = &Banner{Kind: BannerError, Message: chip.Reason}
		return nil
	}
	switch id {
	case ActionHelp:
		m.openOverlay(OverlayHelp)
	case ActionMenu:
		m.openOverlay(OverlayCommand)
	case ActionSearch:
		m.openSearch()
	case ActionRecord:
		m.attemptRecord()
	case ActionImport:
		m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Import will use the same artifact flow; implementation is pending, fixture imports remain browsable."}
	case ActionProviders:
		m.setScreen(ScreenProviders)
	case ActionVerify:
		m.verify()
	case ActionBack:
		return m.back()
	case ActionSelect:
		m.space()
	case ActionEdit:
		m.startEdit()
	case ActionModel:
		m.startModelEdit()
	case ActionTest:
		m.testSelectedProvider()
	case ActionRemove:
		m.startRemoveProvider()
	case ActionDetails:
		m.showProviderDetails()
	case ActionSave:
		m.saveConfig("Settings saved.")
	case ActionCancel:
		m.cancel()
	case ActionPreflight:
		m.attemptRecord()
	case ActionCopyCitation:
		m.copyCitation()
	case ActionCycle:
		m.cycleSetting()
	case ActionOpen:
		m.enter()
	}
	return nil
}

func (m AppModel) actionByID(id ActionID) (ActionChip, bool) {
	for _, chip := range m.visibleActionChips() {
		if chip.ID == id {
			return chip, true
		}
	}
	return ActionChip{}, false
}

func (m *AppModel) openOverlay(kind OverlayKind) {
	m.UI.Overlay = OverlayState{Kind: kind}
	m.UI.Banner = nil
}

func (m *AppModel) openSearch() {
	m.setScreen(ScreenSearch)
	m.UI.Overlay = OverlayState{}
	m.syncSearch(m.UI.SearchQuery)
	m.UI.Banner = nil
}

func (m *AppModel) back() tea.Cmd {
	if m.UI.Overlay.Kind != OverlayNone {
		m.UI.Overlay = OverlayState{}
		return nil
	}
	if m.UI.Active == ScreenDashboard {
		m.UI.Quitting = true
		return tea.Quit
	}
	m.setScreen(ScreenDashboard)
	return nil
}

func (m *AppModel) cancel() {
	if m.UI.Overlay.Kind != OverlayNone {
		m.UI.Overlay = OverlayState{}
		return
	}
	m.UI.Banner = nil
}

func (m *AppModel) enter() {
	if m.UI.Overlay.Kind != OverlayNone {
		m.commitOverlay()
		return
	}
	if m.UI.Focus == FocusSidebar {
		m.setScreen(navItems()[m.UI.SelectedNav].Screen)
		return
	}
	switch m.UI.Active {
	case ScreenDashboard, ScreenMeetings:
		m.setScreen(ScreenDetail)
	case ScreenSearch:
		m.openSelectedSearchResult()
	case ScreenRecorder:
		m.attemptRecord()
	case ScreenProviders:
		m.startEdit()
	case ScreenStorage:
		m.verify()
	case ScreenSettings:
		m.startEdit()
	}
}

func (m *AppModel) space() {
	switch m.UI.Active {
	case ScreenProviders:
		selected := m.selectedProviderSuite()
		if selected.Kind != providers.ProviderKindSpeech {
			m.UI.Banner = &Banner{Kind: BannerInfo, Message: "OpenRouter is fixed for real LLM work. Use M to edit its model."}
			return
		}
		m.App.Config.Routing.SpeechProvider = selected.ID
		m.App.Config.Routing.LLMProvider = "openrouter"
		m.saveConfig("")
		m.UI.Banner = &Banner{Kind: BannerInfo, Message: fmt.Sprintf("Selected %s for speech-to-text. Add a key before live transcription.", selected.ID)}
	case ScreenRecorder:
		m.attemptRecord()
	case ScreenSettings:
		m.cycleSetting()
	default:
		m.enter()
	}
}

func (m *AppModel) startEdit() {
	switch m.UI.Active {
	case ScreenProviders:
		selected := m.selectedProviderSuite()
		m.UI.Overlay = OverlayState{Kind: OverlayProviderKey, Target: EditProviderKey, ProviderID: selected.ID, Label: fmt.Sprintf("Set %s key (%s)", selected.ID, selected.CredentialRef), Mask: true}
	case ScreenSettings:
		row := m.selectedSettingRow()
		switch row.Target {
		case EditArtifactRoot:
			m.UI.Overlay = OverlayState{Kind: OverlaySettings, Target: EditArtifactRoot, Label: "Artifact root", Buffer: m.App.Config.ArtifactRoot}
		case EditOpenRouterModel:
			m.UI.Overlay = OverlayState{Kind: OverlaySettings, Target: EditOpenRouterModel, Label: "OpenRouter model", Buffer: m.App.Config.Routing.LLMModel}
		default:
			if row.Cycle {
				m.cycleSetting()
				return
			}
			m.UI.Banner = &Banner{Kind: BannerInfo, Message: fmt.Sprintf("%s is read-only in this build.", row.Label)}
		}
	default:
		m.enter()
	}
}

func (m *AppModel) startModelEdit() {
	m.UI.Overlay = OverlayState{Kind: OverlaySettings, Target: EditOpenRouterModel, Label: "OpenRouter model", Buffer: m.App.Config.Routing.LLMModel}
}

func (m *AppModel) commitOverlay() {
	switch m.UI.Overlay.Kind {
	case OverlaySearch:
		m.openSelectedSearchResult()
	case OverlayCommand:
		rows := m.commandRows()
		if len(rows) == 0 {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "No command matches the current query."}
			return
		}
		row := rows[clamp(m.UI.Overlay.Selected, 0, len(rows)-1)]
		m.UI.Overlay = OverlayState{}
		_ = m.performAction(row.action)
	case OverlayProviderKey, OverlaySettings:
		m.commitEdit()
	case OverlayConfirm:
		m.confirm()
	}
}

func (m *AppModel) commitEdit() {
	value := strings.TrimSpace(m.UI.Overlay.Buffer)
	switch m.UI.Overlay.Target {
	case EditProviderKey:
		if value == "" {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "Credential cannot be empty."}
			return
		}
		selected, ok := providerByID(m.App.Providers, m.UI.Overlay.ProviderID)
		if !ok || selected.CredentialRef == "" {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "Selected provider has no credential reference."}
			m.UI.Overlay = OverlayState{}
			return
		}
		if m.Runtime.Secrets != nil {
			if err := m.Runtime.Secrets.Set(context.Background(), selected.CredentialRef, value); err != nil {
				m.UI.Banner = &Banner{Kind: BannerError, Message: "Could not save provider key: " + err.Error()}
				return
			}
		}
		m.App.Statuses[selected.ID] = secrets.Status{Ref: selected.CredentialRef, Configured: true, Source: "keychain"}
		m.UI.Banner = &Banner{Kind: BannerInfo, Message: fmt.Sprintf("%s key saved to %s.", selected.ID, selected.CredentialRef)}
	case EditArtifactRoot:
		if value == "" {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "Artifact root cannot be empty."}
			return
		}
		m.App.Config.ArtifactRoot = value
		m.saveConfig("Artifact root saved.")
	case EditOpenRouterModel:
		if value == "" {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "OpenRouter model cannot be empty."}
			return
		}
		m.App.Config.Routing.LLMProvider = "openrouter"
		m.App.Config.Routing.LLMModel = value
		m.saveConfig("OpenRouter model saved.")
	}
	m.UI.Overlay = OverlayState{}
}

func (m *AppModel) testSelectedProvider() {
	selected := m.selectedProviderSuite()
	status := m.App.Statuses[selected.ID]
	if !status.Configured {
		m.UI.Banner = &Banner{Kind: BannerError, Message: fmt.Sprintf("Provider key required: configure %s before testing.", selected.ID)}
		return
	}
	if m.Runtime.Providers != nil {
		if err := m.Runtime.Providers.Test(selected.ID); err != nil {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "Provider test failed: " + err.Error()}
			return
		}
	}
	m.UI.Banner = &Banner{Kind: BannerInfo, Message: fmt.Sprintf("%s key is available from %s.", selected.ID, status.Source)}
}

func (m *AppModel) startRemoveProvider() {
	selected := m.selectedProviderSuite()
	if !m.App.Statuses[selected.ID].Configured {
		m.UI.Banner = &Banner{Kind: BannerError, Message: fmt.Sprintf("No stored key exists for %s.", selected.ID)}
		return
	}
	m.UI.Overlay = OverlayState{Kind: OverlayConfirm, Target: EditProviderKey, ProviderID: selected.ID, Label: fmt.Sprintf("Remove %s key?", selected.ID), ConfirmID: ActionRemove}
}

func (m *AppModel) confirm() {
	if m.UI.Overlay.ConfirmID == ActionRemove {
		m.removeSelectedProvider()
	}
}

func (m *AppModel) removeSelectedProvider() {
	selected, ok := providerByID(m.App.Providers, m.UI.Overlay.ProviderID)
	if !ok {
		m.UI.Overlay = OverlayState{}
		return
	}
	if m.Runtime.Secrets != nil {
		if err := m.Runtime.Secrets.Remove(context.Background(), selected.CredentialRef); err != nil {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "Could not remove provider key: " + err.Error()}
			return
		}
	}
	m.App.Statuses[selected.ID] = secrets.Status{Ref: selected.CredentialRef, Configured: false, Source: "missing"}
	m.UI.Overlay = OverlayState{}
	m.UI.Banner = &Banner{Kind: BannerInfo, Message: fmt.Sprintf("%s key removed.", selected.ID)}
}

func (m *AppModel) showProviderDetails() {
	selected := m.selectedProviderSuite()
	m.UI.Banner = &Banner{Kind: BannerInfo, Message: fmt.Sprintf("%s: %s; env fallback %s; data leaves device: %s.", selected.ID, firstModel(selected), providerEnv(selected.CredentialRef), boolText(selected.SendsRawAudioOffDevice))}
}

func (m *AppModel) cycleSetting() {
	if m.UI.Active != ScreenSettings {
		return
	}
	row := m.selectedSettingRow()
	switch row.Key {
	case "speech":
		speech := sortedByKind(m.App.Providers, providers.ProviderKindSpeech)
		if len(speech) == 0 {
			return
		}
		current := 0
		for i, suite := range speech {
			if suite.ID == m.App.Config.Routing.SpeechProvider {
				current = i
				break
			}
		}
		m.App.Config.Routing.SpeechProvider = speech[(current+1)%len(speech)].ID
		m.saveConfig("Speech provider saved.")
	case "retention":
		m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Retention is fixed for this build: delete raw audio after a valid transcript."}
	}
}

func (m *AppModel) saveConfig(message string) {
	if m.Runtime.ConfigSaver != nil {
		if err := m.Runtime.ConfigSaver.Save(m.App.Config); err != nil {
			m.UI.Banner = &Banner{Kind: BannerError, Message: "Could not save config: " + err.Error()}
			return
		}
	}
	if message != "" {
		m.UI.Banner = &Banner{Kind: BannerInfo, Message: message}
	}
}

func (m *AppModel) attemptRecord() {
	m.setScreen(ScreenRecorder)
	if !m.speechConfigured() {
		m.UI.Banner = &Banner{Kind: BannerError, Message: "Provider key required: configure Mistral, AssemblyAI, or ElevenLabs before recording/transcribing."}
		return
	}
	m.UI.Banner = &Banner{Kind: BannerWarn, Message: "Recording capture helper is not implemented yet; preflight UI is ready."}
}

func (m *AppModel) verify() {
	m.setScreen(ScreenStorage)
	if m.App.Config.Routing.LLMProvider != "openrouter" {
		m.App.Storage.Verified = false
		m.UI.Banner = &Banner{Kind: BannerError, Message: "Invalid route: real LLM capabilities must use OpenRouter."}
		return
	}
	m.App.Storage.Verified = true
	m.App.Storage.LastResult = "passed this session"
	m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Local verification passed: config route, provider registry, and offline fixtures are readable."}
}

func (m *AppModel) copyCitation() {
	results := m.filteredSearchResults()
	if len(results) == 0 {
		m.UI.Banner = &Banner{Kind: BannerError, Message: "No citation is available until a result is selected."}
		return
	}
	result := results[clamp(m.UI.SelectedResult, 0, len(results)-1)]
	m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Citation copied: " + citationFor(result)}
}

func (m *AppModel) openSelectedSearchResult() {
	results := m.filteredSearchResults()
	if len(results) == 0 {
		m.UI.Banner = &Banner{Kind: BannerError, Message: "No search result is selected."}
		return
	}
	index := clamp(m.UI.SelectedResult, 0, len(results)-1)
	result := results[index]
	for i, meeting := range m.App.Meetings {
		if meeting.ID == result.MeetingID {
			m.UI.SelectedMeeting = i
			break
		}
	}
	m.UI.SelectedResult = index
	m.setScreen(ScreenDetail)
	m.UI.Banner = &Banner{Kind: BannerInfo, Message: "Opened evidence " + result.Segment.ID + " from " + result.MeetingTitle + "."}
}

func keyMatches(chipKey string, key string) bool {
	ck := strings.ToLower(chipKey)
	k := strings.ToLower(key)
	switch ck {
	case "space":
		return k == "space"
	case "enter":
		return k == "enter"
	case "esc":
		return k == "esc"
	default:
		return ck == k
	}
}

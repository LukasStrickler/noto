package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/secrets"
)

func testScreen(configured bool) ProviderScreen {
	reg := providers.DefaultRegistry()
	statuses := map[string]secrets.Status{
		"mistral":    {Ref: "provider:mistral", Configured: configured, Source: "memory"},
		"assemblyai": {Ref: "provider:assemblyai", Configured: false, Source: "missing"},
		"elevenlabs": {Ref: "provider:elevenlabs", Configured: false, Source: "missing"},
		"openrouter": {Ref: "provider:openrouter", Configured: configured, Source: "memory"},
		"fake-stt":   {Configured: true, Source: "none"},
		"fake-llm":   {Configured: true, Source: "none"},
	}
	return ProviderScreen{Config: config.Default(), Providers: reg.List(), Statuses: statuses}
}

func TestRenderProvidersSeparatesSpeechAndOpenRouter(t *testing.T) {
	rendered := RenderProviders(testScreen(true))

	for _, want := range []string{"Speech-to-text", "LLM via OpenRouter", "mistral", "assemblyai", "elevenlabs", "openrouter"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "sk-") {
		t.Fatalf("render leaked secret-looking text:\n%s", rendered)
	}
}

func TestAppStartsOnDashboard(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	rendered := model.View()
	for _, want := range []string{"Dashboard", "meetings", "evidence preview", "Provider"} {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
			t.Fatalf("dashboard render missing %q:\n%s", want, rendered)
		}
	}
}

func TestAppNavigationByKeyboard(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("p"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenProviders {
		t.Fatalf("active after p = %s, want providers", model.UI.Active)
	}
	updated, _ = model.Update(key("2"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenMeetings {
		t.Fatalf("active after 2 = %s, want meetings", model.UI.Active)
	}
	updated, _ = model.Update(key("/"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenSearch {
		t.Fatalf("active after / = %s, want search", model.UI.Active)
	}
}

func TestKeyboardSidebarNavigation(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("g"))
	model = updated.(AppModel)
	if model.UI.Focus != FocusSidebar {
		t.Fatalf("focus after g = %s, want sidebar", model.UI.Focus)
	}
	updated, _ = model.Update(key("down"))
	model = updated.(AppModel)
	if model.UI.SelectedNav != 1 {
		t.Fatalf("selectedNav after down = %d, want 1", model.UI.SelectedNav)
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenMeetings {
		t.Fatalf("active after sidebar enter = %s, want meetings", model.UI.Active)
	}
}

func TestKeyboardNumberJumps(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("6"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenProviders {
		t.Fatalf("active after 6 = %s, want providers", model.UI.Active)
	}
	updated, _ = model.Update(key("3"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenSearch {
		t.Fatalf("active after 3 = %s, want search", model.UI.Active)
	}
}

func TestKeyboardMovementAfterNumberJumpChangesPrimarySelection(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenProviders)
	updated, _ := model.Update(key("1"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenDashboard || model.UI.Focus != FocusPrimary {
		t.Fatalf("after 1 active/focus = %s/%s, want dashboard/primary", model.UI.Active, model.UI.Focus)
	}
	updated, _ = model.Update(key("down"))
	model = updated.(AppModel)
	if model.UI.SelectedMeeting != 1 {
		t.Fatalf("down after 1 selectedMeeting = %d, want 1", model.UI.SelectedMeeting)
	}
	updated, _ = model.Update(key("down"))
	model = updated.(AppModel)
	if model.UI.SelectedMeeting != 0 {
		t.Fatalf("down at end should wrap to 0, got %d", model.UI.SelectedMeeting)
	}
}

func TestKeyboardPaneFocusCycling(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("tab"))
	model = updated.(AppModel)
	if model.UI.Focus != FocusDetail {
		t.Fatalf("focus after tab = %s, want detail", model.UI.Focus)
	}
	updated, _ = model.Update(key("left"))
	model = updated.(AppModel)
	if model.UI.Focus != FocusPrimary {
		t.Fatalf("focus after left = %s, want primary", model.UI.Focus)
	}
}

func TestKeyboardProviderSelectionWithSpace(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenProviders)
	updated, _ := model.Update(key("down"))
	model = updated.(AppModel)
	updated, _ = model.Update(key("space"))
	model = updated.(AppModel)
	if model.App.Config.Routing.SpeechProvider != "elevenlabs" {
		t.Fatalf("speech provider = %s, want elevenlabs", model.App.Config.Routing.SpeechProvider)
	}
	if model.UI.Banner == nil || !strings.Contains(model.UI.Banner.Message, "Selected elevenlabs") {
		t.Fatalf("space selection banner = %#v", model.UI.Banner)
	}
}

func TestMissingKeyRecordShowsBannerAndDoesNotTrapUser(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("r"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenRecorder {
		t.Fatalf("active after r = %s, want recorder", model.UI.Active)
	}
	if model.UI.Banner == nil || !strings.Contains(model.UI.Banner.Message, "Provider key required") {
		t.Fatalf("missing provider banner = %#v", model.UI.Banner)
	}
	updated, _ = model.Update(key("p"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenProviders {
		t.Fatalf("user could not leave recorder; active = %s", model.UI.Active)
	}
}

func TestProviderScreenMissingKeysStillNavigable(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenProviders)
	updated, _ := model.Update(key("down"))
	model = updated.(AppModel)
	if model.UI.SelectedProvider != 1 {
		t.Fatalf("selectedProvider = %d, want 1", model.UI.SelectedProvider)
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	if model.UI.Overlay.Kind != OverlayProviderKey {
		t.Fatalf("provider enter overlay = %s, want provider key form", model.UI.Overlay.Kind)
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(AppModel)
	updated, _ = model.Update(key("2"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenMeetings {
		t.Fatalf("user could not navigate away from missing provider state")
	}
}

func TestOverlaysOpenAndClose(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("?"))
	model = updated.(AppModel)
	if model.UI.Overlay.Kind != OverlayHelp {
		t.Fatalf("overlay after ? = %s, want help", model.UI.Overlay.Kind)
	}
	updated, _ = model.Update(key("esc"))
	model = updated.(AppModel)
	if model.UI.Overlay.Kind != "" {
		t.Fatalf("overlay after esc = %s, want closed", model.UI.Overlay.Kind)
	}
	updated, _ = model.Update(key(":"))
	model = updated.(AppModel)
	if model.UI.Overlay.Kind != OverlayCommand {
		t.Fatalf("overlay after : = %s, want command", model.UI.Overlay.Kind)
	}
}

func TestMouseSidebarSelection(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: 2, Y: 6})
	model = updated.(AppModel)
	if model.UI.Active != ScreenMeetings {
		t.Fatalf("mouse sidebar active = %s, want meetings", model.UI.Active)
	}
}

func TestMouseSidebarRowsMatchVisibleNavigation(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenProviders)
	model.UI.Width = 140
	model.UI.Height = 30

	cases := []struct {
		y      int
		screen Screen
	}{
		{5, ScreenDashboard},
		{6, ScreenMeetings},
		{7, ScreenSearch},
		{8, ScreenRecorder},
		{9, ScreenTranscript},
		{12, ScreenProviders},
		{13, ScreenStorage},
		{14, ScreenSettings},
	}
	for _, tc := range cases {
		updated, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: 3, Y: tc.y})
		model = updated.(AppModel)
		if model.UI.Active != tc.screen {
			t.Fatalf("click y=%d active = %s, want %s", tc.y, model.UI.Active, tc.screen)
		}
	}
	updated, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: 3, Y: 11})
	model = updated.(AppModel)
	if model.UI.Active != ScreenSettings {
		t.Fatalf("clicking sidebar group gap changed active screen to %s", model.UI.Active)
	}
}

func TestMouseContentRowsSelectVisibleItems(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	model.UI.Width = 140
	model.UI.Height = 30
	updated, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: model.sidebarWidth() + 4, Y: 6})
	model = updated.(AppModel)
	if model.UI.SelectedMeeting != 1 {
		t.Fatalf("dashboard meeting click selected = %d, want 1", model.UI.SelectedMeeting)
	}

	model = NewAppModel(testScreen(false), ScreenProviders)
	model.UI.Width = 140
	model.UI.Height = 30
	updated, _ = model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: model.sidebarWidth() + 4, Y: 7})
	model = updated.(AppModel)
	if model.UI.SelectedProvider != 2 {
		t.Fatalf("provider click selected = %d, want 2", model.UI.SelectedProvider)
	}

	model = NewAppModel(testScreen(false), ScreenSettings)
	model.UI.Width = 140
	model.UI.Height = 30
	updated, _ = model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: model.sidebarWidth() + 4, Y: 7})
	model = updated.(AppModel)
	if model.UI.SelectedSetting != 3 {
		t.Fatalf("settings click selected = %d, want 3", model.UI.SelectedSetting)
	}
}

func TestSnapshotsCoverKeyScreens(t *testing.T) {
	cases := []struct {
		name   string
		screen Screen
		want   []string
	}{
		{"dashboard", ScreenDashboard, []string{"Product architecture sync", "JOBS", "EVIDENCE PREVIEW"}},
		{"sidebar groups", ScreenDashboard, []string{"DAILY", "SETUP"}},
		{"providers", ScreenProviders, []string{"SPEECH-TO-TEXT", "ASSEMBLYAI_API_KEY", "provider:assemblyai"}},
		{"recorder", ScreenRecorder, []string{"RECORDER PREFLIGHT", "provider", "me/mic"}},
		{"search", ScreenSearch, []string{"SEARCH", "QUERY", "EVIDENCE", "Type to filter"}},
		{"detail", ScreenDetail, []string{"PRODUCT ARCHITECTURE SYNC", "DECISION", "FILES"}},
		{"transcript", ScreenTranscript, []string{"TIMELINE", "TRANSCRIPT", "local_speaker"}},
		{"storage", ScreenStorage, []string{"STORAGE AND VERIFICATION", "noto verify --json"}},
		{"settings", ScreenSettings, []string{"SETTINGS", "openrouter"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := NewAppModel(testScreen(false), tc.screen)
			model.UI.Width = 120
			model.UI.Height = 34
			rendered := model.View()
			for _, want := range tc.want {
				if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
					t.Fatalf("%s render missing %q:\n%s", tc.name, want, rendered)
				}
			}
			if strings.Contains(rendered, "sk-") {
				t.Fatalf("%s render leaked secret-looking text:\n%s", tc.name, rendered)
			}
		})
	}
}

func TestResponsiveLayouts(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		model := NewAppModel(testScreen(false), ScreenDashboard)
		model.UI.Width = width
		model.UI.Height = 30
		rendered := model.View()
		if !strings.Contains(rendered, "Noto") || !strings.Contains(rendered, "Q") {
			t.Fatalf("responsive layout %d missing shell:\n%s", width, rendered)
		}
	}
}
func TestCommandBarChipsHaveWorkingHotkeys(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	rendered := model.View()
	for _, want := range []string{"[? Help]", "[M Menu]", "[/ Search]", "[R Record]", "[I Import]", "[P Providers]", "[V Verify]", "[Q Back]"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("command bar missing %q:\n%s", want, rendered)
		}
	}
	updated, _ := model.Update(key("m"))
	model = updated.(AppModel)
	if model.UI.Overlay.Kind != OverlayCommand {
		t.Fatalf("m hotkey overlay = %s, want command", model.UI.Overlay.Kind)
	}
}

func TestActionChipMouseMatchesHotkey(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	model.UI.Width = 120
	model.UI.Height = 34
	updated, _ := model.Update(tea.MouseMsg{Action: tea.MouseActionPress, X: 26, Y: 33})
	model = updated.(AppModel)
	if model.UI.Active != ScreenSearch {
		t.Fatalf("mouse search chip active = %s, want search", model.UI.Active)
	}
}

func TestDisabledActionShowsBanner(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenProviders)
	updated, _ := model.Update(key("t"))
	model = updated.(AppModel)
	if model.UI.Banner == nil || !strings.Contains(model.UI.Banner.Message, "Provider key required") {
		t.Fatalf("disabled test banner = %#v", model.UI.Banner)
	}
}

func TestSearchPageFiltersAndOpensEvidence(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("/"))
	model = updated.(AppModel)
	for _, r := range "cost" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(AppModel)
	}
	rendered := model.View()
	if !strings.Contains(strings.ToLower(rendered), "query") || !strings.Contains(strings.ToLower(rendered), "cost") {
		t.Fatalf("search page missing query/input:\n%s", rendered)
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenDetail {
		t.Fatalf("search enter active = %s, want detail", model.UI.Active)
	}
	if model.UI.Banner == nil || !strings.Contains(model.UI.Banner.Message, "Opened evidence") {
		t.Fatalf("search open banner = %#v", model.UI.Banner)
	}
}

func TestSearchPageAllowsLowercaseCAndUppercaseCopiesCitation(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key("/"))
	model = updated.(AppModel)
	for _, r := range "product" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(AppModel)
	}
	if model.UI.SearchQuery != "product" {
		t.Fatalf("search query = %q, want product", model.UI.SearchQuery)
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	model = updated.(AppModel)
	if model.UI.Banner == nil || !strings.Contains(model.UI.Banner.Message, "Citation copied") {
		t.Fatalf("uppercase C did not copy citation: %#v", model.UI.Banner)
	}
	if model.UI.SearchQuery != "product" {
		t.Fatalf("uppercase C mutated query = %q", model.UI.SearchQuery)
	}
}

func TestCommandPaletteFiltersAndRunsCommand(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	updated, _ := model.Update(key(":"))
	model = updated.(AppModel)
	for _, r := range "verify" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(AppModel)
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	if model.UI.Active != ScreenStorage {
		t.Fatalf("command palette active = %s, want storage", model.UI.Active)
	}
}

func TestIdleModelHasNoTick(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	if cmd := model.Init(); cmd != nil {
		t.Fatalf("idle model returned tick command")
	}
}

func TestProviderKeyEditMasksAndSaves(t *testing.T) {
	store := secrets.NewMemoryStore()
	model := NewAppModelWithRuntime(testScreen(false), ScreenProviders, AppRuntime{Secrets: store})
	model.UI.Width = 120
	model.UI.Height = 34
	updated, _ := model.Update(key("e"))
	model = updated.(AppModel)
	for _, r := range "sk-test-secret" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(AppModel)
	}
	if strings.Contains(model.View(), "sk-test-secret") {
		t.Fatalf("edit modal leaked raw key:\n%s", model.View())
	}
	if !strings.Contains(model.View(), "EDIT") {
		t.Fatalf("edit modal missing title:\n%s", model.View())
	}
	if !strings.Contains(model.View(), "PROVIDERS") {
		t.Fatalf("edit modal did not preserve the underlying screen:\n%s", model.View())
	}
	if got := lipgloss.Height(model.View()); got != model.UI.Height {
		t.Fatalf("edit modal changed render height = %d, want %d:\n%s", got, model.UI.Height, model.View())
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	if !model.App.Statuses["assemblyai"].Configured {
		t.Fatalf("provider status was not configured after edit")
	}
	if _, ok := store.Values["provider:assemblyai"]; !ok {
		t.Fatalf("provider key was not saved to credential store")
	}
	if strings.Contains(model.View(), "sk-test-secret") {
		t.Fatalf("post-save render leaked raw key:\n%s", model.View())
	}
}

func TestSettingsEditAndCycle(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenSettings)
	model.App.Config.Routing.LLMModel = ""
	updated, _ := model.Update(key("down"))
	model = updated.(AppModel)
	updated, _ = model.Update(key("space"))
	model = updated.(AppModel)
	if model.App.Config.Routing.SpeechProvider == "mistral" {
		t.Fatalf("speech provider did not cycle")
	}
	updated, _ = model.Update(key("down"))
	model = updated.(AppModel)
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	for _, r := range "openai/gpt-4.1-mini" {
		updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		model = updated.(AppModel)
	}
	updated, _ = model.Update(key("enter"))
	model = updated.(AppModel)
	if model.App.Config.Routing.LLMModel != "openai/gpt-4.1-mini" {
		t.Fatalf("llm model = %s", model.App.Config.Routing.LLMModel)
	}
}

func TestNoFocusTextInRender(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	if strings.Contains(strings.ToLower(model.View()), "[focus]") {
		t.Fatalf("render still contains focus marker:\n%s", model.View())
	}
}

func TestDashboardKeepsMeetingListUsableAtWideWidth(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenDashboard)
	model.UI.Width = 140
	model.UI.Height = 24
	rendered := model.View()
	if !strings.Contains(rendered, "/ search evidence") {
		t.Fatalf("dashboard meeting/search affordance was clipped:\n%s", rendered)
	}
	if strings.Contains(rendered, "filter or search all") {
		t.Fatalf("dashboard kept the old overlong search hint:\n%s", rendered)
	}
}

func TestRecorderHasPreflightAndSourcePanelsWithoutDuplicateRecordChip(t *testing.T) {
	model := NewAppModel(testScreen(false), ScreenRecorder)
	model.UI.Width = 140
	model.UI.Height = 24
	rendered := model.View()
	for _, want := range []string{"RECORDER", "SOURCES", "me/mic", "participants"} {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(want)) {
			t.Fatalf("recorder render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, "[R Start]") != 1 {
		t.Fatalf("recorder should render one record chip:\n%s", rendered)
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

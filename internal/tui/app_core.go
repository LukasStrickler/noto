package tui

import tea "github.com/charmbracelet/bubbletea"

func NewAppModel(screen ProviderScreen, initial Screen) AppModel {
	return NewAppModelWithRuntime(screen, initial, AppRuntime{})
}

func NewAppModelWithRuntime(screen ProviderScreen, initial Screen, runtime AppRuntime) AppModel {
	if initial == "" {
		initial = ScreenDashboard
	}
	store := runtime.Meetings
	if store == nil {
		fixtures := newFixtureStore()
		store = fixtures
	}
	meetings := store.ListMeetings()
	app := AppState{
		Config:        screen.Config,
		Providers:     screen.Providers,
		Statuses:      screen.Statuses,
		Meetings:      meetings,
		Jobs:          fixtureJobs(),
		Recorder:      fixtureRecorder(),
		Storage:       fixtureStorage(),
		SearchResults: store.SearchSegments(""),
	}
	if runtime.Jobs != nil {
		app.Jobs = runtime.Jobs.Jobs()
	}
	if runtime.Recorder != nil {
		app.Recorder = runtime.Recorder.Status()
	}
	if runtime.Providers != nil {
		app.Statuses = runtime.Providers.Statuses()
	}
	model := AppModel{
		App: app,
		UI: UIState{
			Active:      initial,
			Previous:    ScreenDashboard,
			Focus:       FocusPrimary,
			Width:       104,
			Height:      34,
			SelectedNav: navIndex(initial),
		},
		Runtime: runtime,
	}
	model.syncSearch("")
	return model
}

func RunApp(screen ProviderScreen, initial Screen, runtimes ...AppRuntime) error {
	runtime := AppRuntime{}
	if len(runtimes) > 0 {
		runtime = runtimes[0]
	}
	_, err := tea.NewProgram(NewAppModelWithRuntime(screen, initial, runtime), tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func RunProvidersApp(screen ProviderScreen) error {
	return RunApp(screen, ScreenProviders)
}

func (m AppModel) Init() tea.Cmd {
	return nil
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.UI.Width = msg.Width
		m.UI.Height = msg.Height
	case tea.MouseMsg:
		var cmd tea.Cmd
		m, cmd = m.handleMouse(msg)
		return m, cmd
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m AppModel) View() string {
	if m.UI.Quitting {
		return ""
	}
	return m.renderShell(m.renderActive())
}

func RenderProvidersInteractive(screen ProviderScreen, focus int) string {
	m := NewAppModel(screen, ScreenProviders)
	m.UI.SelectedProvider = focus
	return m.View()
}

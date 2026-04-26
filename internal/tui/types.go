package tui

import (
	"github.com/lukasstrickler/noto/internal/config"
	"github.com/lukasstrickler/noto/internal/providers"
	"github.com/lukasstrickler/noto/internal/secrets"
)

type Screen string

const (
	ScreenDashboard  Screen = "dashboard"
	ScreenMeetings   Screen = "meetings"
	ScreenSearch     Screen = "search"
	ScreenRecorder   Screen = "recorder"
	ScreenDetail     Screen = "detail"
	ScreenTranscript Screen = "transcript"
	ScreenProviders  Screen = "providers"
	ScreenStorage    Screen = "storage"
	ScreenSettings   Screen = "settings"
)

type Focus string

const (
	FocusSidebar Focus = "sidebar"
	FocusPrimary Focus = "primary"
	FocusDetail  Focus = "detail"
	FocusJobs    Focus = "jobs"
)

type BannerKind string

const (
	BannerInfo  BannerKind = "info"
	BannerWarn  BannerKind = "warn"
	BannerError BannerKind = "error"
)

type Banner struct {
	Kind    BannerKind
	Message string
}

type ActionID string

const (
	ActionHelp         ActionID = "help"
	ActionMenu         ActionID = "menu"
	ActionSearch       ActionID = "search"
	ActionRecord       ActionID = "record"
	ActionImport       ActionID = "import"
	ActionProviders    ActionID = "providers"
	ActionVerify       ActionID = "verify"
	ActionBack         ActionID = "back"
	ActionSelect       ActionID = "select"
	ActionEdit         ActionID = "edit"
	ActionModel        ActionID = "model"
	ActionTest         ActionID = "test"
	ActionRemove       ActionID = "remove"
	ActionDetails      ActionID = "details"
	ActionSave         ActionID = "save"
	ActionCancel       ActionID = "cancel"
	ActionPreflight    ActionID = "preflight"
	ActionCopyCitation ActionID = "copy_citation"
	ActionCycle        ActionID = "cycle"
	ActionOpen         ActionID = "open"
	ActionDelete       ActionID = "delete"
)

type ActionChip struct {
	Key     string
	Label   string
	Enabled bool
	Reason  string
	ID      ActionID
}

type ActionBound struct {
	Action ActionID
	X0     int
	X1     int
	Y      int
}

type OverlayKind string

const (
	OverlayNone        OverlayKind = ""
	OverlayHelp        OverlayKind = "help"
	OverlayCommand     OverlayKind = "command"
	OverlaySearch      OverlayKind = "search"
	OverlayProviderKey OverlayKind = "provider_key"
	OverlaySettings    OverlayKind = "settings_form"
	OverlayConfirm     OverlayKind = "confirm"
)

type EditTarget string

const (
	EditProviderKey     EditTarget = "provider_key"
	EditOpenRouterModel EditTarget = "openrouter_model"
	EditArtifactRoot    EditTarget = "artifact_root"
)

type OverlayState struct {
	Kind       OverlayKind
	Query      string
	Selected   int
	Target     EditTarget
	ProviderID string
	Label      string
	Buffer     string
	Mask       bool
	ConfirmID  ActionID
}

type AppState struct {
	Config        config.Config
	Providers     []providers.ProviderSuite
	Statuses      map[string]secrets.Status
	Meetings      []MeetingFixture
	Jobs          []JobState
	Recorder      RecorderState
	Storage       StorageState
	SearchResults []SearchResult
}

type UIState struct {
	Active           Screen
	Previous         Screen
	Focus            Focus
	Width            int
	Height           int
	Banner           *Banner
	SelectedNav      int
	SelectedMeeting  int
	SelectedProvider int
	SelectedResult   int
	SelectedSetting  int
	SearchQuery      string
	Overlay          OverlayState
	ActionBounds     []ActionBound
	Quitting         bool
}

type ConfigSaver interface {
	Save(config.Config) error
}

type MeetingStore interface {
	ListMeetings() []MeetingFixture
	GetMeeting(id string) (MeetingFixture, bool)
	SearchSegments(query string) []SearchResult
}

type JobStore interface {
	Jobs() []JobState
}

type RecorderStore interface {
	Status() RecorderState
}

type ProviderStatusStore interface {
	Statuses() map[string]secrets.Status
	Test(providerID string) error
}

type SettingsStore interface {
	ConfigSaver
}

type AppRuntime struct {
	ConfigSaver ConfigSaver
	Secrets     secrets.Store
	Meetings    MeetingStore
	Jobs        JobStore
	Recorder    RecorderStore
	Providers   ProviderStatusStore
}

type AppModel struct {
	App     AppState
	UI      UIState
	Runtime AppRuntime
}

type MeetingFixture struct {
	ID        string
	Title     string
	Date      string
	Duration  string
	Status    string
	Speakers  int
	Summary   string
	Decisions []string
	Risks     []string
	Actions   []string
	Files     []string
	Segments  []TranscriptSegment
}

type TranscriptSegment struct {
	ID      string
	Time    string
	Speaker string
	Role    string
	Text    string
}

type SearchResult struct {
	MeetingID    string
	MeetingTitle string
	Segment      TranscriptSegment
}

type JobState struct {
	Name   string
	Status string
	Detail string
}

type RecorderState struct {
	State          string
	Title          string
	MicDB          int
	ParticipantsDB int
	Permission     string
	Retention      string
}

type StorageState struct {
	Schema     string
	Checksum   string
	Index      string
	Warning    string
	Verified   bool
	LastResult string
}

type settingRow struct {
	Key    string
	Label  string
	Value  string
	Target EditTarget
	Cycle  bool
}

func ProviderScreenFromConfig(cfg config.Config, suites []providers.ProviderSuite, statuses map[string]secrets.Status) ProviderScreen {
	return ProviderScreen{Config: cfg, Providers: suites, Statuses: statuses}
}

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

type ContextID string

const (
	ContextRoot        ContextID = "root"
	ContextMeetingList ContextID = "meeting_list"
	ContextDetail      ContextID = "detail"
	ContextTranscript  ContextID = "transcript"
	ContextSearch      ContextID = "search"
	ContextRecorder    ContextID = "recorder"
)

type ContextStack struct {
	stack []ContextID
}

func NewContextStack(initial ContextID) ContextStack {
	return ContextStack{stack: []ContextID{initial}}
}

func (cs *ContextStack) Current() ContextID {
	if len(cs.stack) == 0 {
		return ContextRoot
	}
	return cs.stack[len(cs.stack)-1]
}

func (cs *ContextStack) Push(ctx ContextID) {
	cs.stack = append(cs.stack, ctx)
}

func (cs *ContextStack) Pop() ContextID {
	if len(cs.stack) == 0 {
		return ContextRoot
	}
	ctx := cs.stack[len(cs.stack)-1]
	cs.stack = cs.stack[:len(cs.stack)-1]
	return ctx
}

func (cs *ContextStack) Replace(ctx ContextID) {
	if len(cs.stack) == 0 {
		cs.stack = append(cs.stack, ctx)
		return
	}
	cs.stack[len(cs.stack)-1] = ctx
}

type ViewportComponent struct {
	TotalItems    int
	VisibleHeight int
	ItemHeight   int
	Offset       int
	Selected     int
}

func NewViewportComponent(totalItems, visibleHeight, itemHeight int) ViewportComponent {
	return ViewportComponent{
		TotalItems:    totalItems,
		VisibleHeight: visibleHeight,
		ItemHeight:    itemHeight,
		Offset:        0,
		Selected:      0,
	}
}

func (vp ViewportComponent) VisibleRange() (start, end int) {
	start = vp.Offset
	end = min(start+vp.VisibleHeight, vp.TotalItems)
	return start, end
}

func (vp *ViewportComponent) ScrollDown(n int) {
	newOffset := min(vp.Offset+n, max(0, vp.TotalItems-vp.VisibleHeight))
	vp.Offset = newOffset
}

func (vp *ViewportComponent) ScrollUp(n int) {
	vp.Offset = max(0, vp.Offset-n)
}

func (vp *ViewportComponent) ScrollToItem(item int) {
	if item < vp.Offset {
		vp.Offset = max(0, item)
	} else if item >= vp.Offset+vp.VisibleHeight {
		vp.Offset = min(item, max(0, vp.TotalItems-vp.VisibleHeight))
	}
}

func (vp *ViewportComponent) SelectItem(item int) {
	vp.Selected = clamp(item, 0, vp.TotalItems-1)
	vp.ScrollToItem(vp.Selected)
}

type BubblePupState struct {
	Recording    bool
	StartTime    int64
	Elapsed      int64
	MicLevel     int
	SpeakerLevel int
	AmbientLevel int
	PulseFrame   int
}

func NewBubblePupState() BubblePupState {
	return BubblePupState{
		Recording:    false,
		StartTime:    0,
		Elapsed:      0,
		MicLevel:     -60,
		SpeakerLevel: -60,
		AmbientLevel: -60,
		PulseFrame:   0,
	}
}

func (bp *BubblePupState) Start() {
	bp.Recording = true
	bp.StartTime = now()
	bp.Elapsed = 0
}

func (bp *BubblePupState) Stop() {
	bp.Recording = false
}

func (bp *BubblePupState) Tick() {
	if bp.Recording {
		bp.Elapsed = now() - bp.StartTime
	}
}

func (bp *BubblePupState) PulseFrameCycle() int {
	frame := (bp.PulseFrame + 1) % 10
	bp.PulseFrame = frame
	return frame
}

func (bp *BubblePupState) IsPulsing() bool {
	return bp.PulseFrame < 5
}

func (bp BubblePupState) ElapsedFormatted() string {
	return formatElapsed(bp.Elapsed)
}

type CommandPaletteState struct {
	Query       string
	Selected    int
	Candidates  []CommandCandidate
}

type CommandCandidate struct {
	Label  string
	Hint   string
	Action ActionID
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

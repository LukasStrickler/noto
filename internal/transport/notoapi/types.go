// Package notoapi defines the wire types shared between the noto server
// and its clients (in-process direct, HTTP local UDS, HTTP remote TCP).
// Every type is JSON-serializable; no third-party types leak through.
package notoapi

import "time"

// ---------- Meetings ----------

type Meeting struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	CreatedAt        time.Time      `json:"created_at"`
	DurationSeconds  int            `json:"duration_seconds"`
	Status           MeetingStatus  `json:"status"`
	CurrentVersionID string         `json:"current_version_id,omitempty"`
	Speakers         int            `json:"speakers,omitempty"`
	Attendees        []string       `json:"attendees,omitempty"`
	DecisionCount    int            `json:"decision_count"`
	ActionCount      int            `json:"action_count"`
	RiskCount        int            `json:"risk_count"`
	QuestionCount    int            `json:"question_count"`
	Versions         []Version      `json:"versions,omitempty"`
	ShortSummary     string         `json:"short_summary,omitempty"`
	Source           *MeetingSource `json:"source,omitempty"`
	// Identity is a rollup of the meeting's cross-meeting speaker-identity
	// state, so the dashboard can flag which meetings still need attention.
	// Nil when the meeting has no speaker mappings yet.
	Identity *SpeakerIdentitySummary `json:"identity,omitempty"`
}

// SpeakerIdentitySummary buckets a meeting's speaker mappings by how resolved
// their cross-meeting person is: Resolved (auto/manual — confirmed), Likely
// (pending — a strong candidate awaiting one-tap confirm), New (an auto-minted
// provisional person, usually unnamed), Unset (no profile / unmatched).
type SpeakerIdentitySummary struct {
	Resolved int `json:"resolved"`
	Likely   int `json:"likely"`
	New      int `json:"new"`
	Unset    int `json:"unset"`
}

type MeetingStatus string

const (
	StatusRecording   MeetingStatus = "recording"
	StatusRecorded    MeetingStatus = "recorded"
	StatusTranscribed MeetingStatus = "transcribed"
	StatusSummarized  MeetingStatus = "summarized"
	StatusFailed      MeetingStatus = "failed"
)

type Version struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Reason    string    `json:"reason"`
	Checksum  string    `json:"checksum,omitempty"`
}

type MeetingSource struct {
	Kind              string `json:"kind"`
	LocalSpeakerLabel string `json:"local_speaker_label,omitempty"`
	ParticipantLabel  string `json:"participant_label,omitempty"`
}

type ListMeetingsOpts struct {
	Query string `json:"q,omitempty"`
	Since string `json:"since,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

type ListMeetingsResult struct {
	Meetings []Meeting `json:"meetings"`
	Total    int       `json:"total"`
}

// ---------- Transcript ----------

type Transcript struct {
	MeetingID string              `json:"meeting_id"`
	Segments  []TranscriptSegment `json:"segments"`
	Speakers  []Speaker           `json:"speakers"`
}

type TranscriptSegment struct {
	ID         string  `json:"id"`
	SpeakerID  string  `json:"speaker_id"`
	Speaker    string  `json:"speaker"`
	Role       string  `json:"role,omitempty"`
	StartSec   float64 `json:"start_sec"`
	EndSec     float64 `json:"end_sec"`
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence,omitempty"`
}

type Speaker struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role,omitempty"`
	// Label is the stable, anonymous per-meeting token ("Speaker A") — the only
	// speaker identifier ever sent to the LLM. The UI resolves it back to a
	// person + color locally (see internal/ui/tui renderPeople).
	Label string `json:"label,omitempty"`
}

// ---------- Summary ----------

type Summary struct {
	MeetingID     string        `json:"meeting_id"`
	ShortSummary  string        `json:"short_summary"`
	Markdown      string        `json:"markdown,omitempty"`
	Decisions     []SummaryItem `json:"decisions"`
	ActionItems   []ActionItem  `json:"action_items"`
	Risks         []SummaryItem `json:"risks"`
	OpenQuestions []SummaryItem `json:"open_questions"`
}

type SummaryItem struct {
	Text        string   `json:"text"`
	SpeakerIDs  []string `json:"speaker_ids,omitempty"`
	SegmentRefs []string `json:"segment_refs,omitempty"`
}

type ActionItem struct {
	Text        string   `json:"text"`
	Owner       string   `json:"owner,omitempty"`
	Completed   bool     `json:"completed,omitempty"`
	SegmentRefs []string `json:"segment_refs,omitempty"`
}

// ---------- Files / Agent handoff ----------

type MeetingFiles struct {
	MeetingID   string `json:"meeting_id"`
	Manifest    string `json:"manifest,omitempty"`
	Transcript  string `json:"transcript,omitempty"`
	SummaryJSON string `json:"summary_json,omitempty"`
	SummaryMD   string `json:"summary_md,omitempty"`
	Audio       string `json:"audio,omitempty"`
	MeetingDir  string `json:"meeting_dir,omitempty"`
}

type AgentHandoff struct {
	MeetingID string       `json:"meeting_id"`
	VersionID string       `json:"version_id,omitempty"`
	Files     MeetingFiles `json:"files"`
	Commands  []string     `json:"commands"`
}

// ---------- Search ----------

type SearchOpts struct {
	Query   string `json:"q"`
	Scope   string `json:"scope,omitempty"`
	Speaker string `json:"speaker,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type SearchResult struct {
	Hits     []SearchHit   `json:"hits"`
	Meetings []MeetingHits `json:"meetings,omitempty"`
	Total    int           `json:"total"`
}

type SearchHit struct {
	MeetingID    string  `json:"meeting_id"`
	MeetingTitle string  `json:"meeting_title"`
	SegmentID    string  `json:"segment_id,omitempty"`
	Speaker      string  `json:"speaker,omitempty"`
	Timestamp    float64 `json:"timestamp_seconds"`
	Snippet      string  `json:"snippet"`
	BM25Score    float64 `json:"bm25_score"`
	ResultType   string  `json:"result_type"`
}

// MeetingHits is the per-meeting aggregation a UI needs to render
// "5 transcript matches, 2 summary matches" badges and decide whether
// to show a title-first row or a body-only row.
type MeetingHits struct {
	MeetingID       string      `json:"meeting_id"`
	MeetingTitle    string      `json:"meeting_title"`
	CreatedAt       time.Time   `json:"created_at"`
	TitleMatch      bool        `json:"title_match"`
	SummaryMatch    bool        `json:"summary_match"`
	TranscriptCount int         `json:"transcript_count"`
	SummaryCount    int         `json:"summary_count"`
	QuestionCount   int         `json:"question_count"`
	Score           float64     `json:"score"`
	Snippet         string      `json:"snippet"`
	TopHits         []SearchHit `json:"top_hits,omitempty"`
}

// ---------- Recording ----------

type RecordingState struct {
	Active        bool      `json:"active"`
	MeetingID     string    `json:"meeting_id,omitempty"`
	Title         string    `json:"title,omitempty"`
	StartedAt     time.Time `json:"started_at,omitempty"`
	ElapsedSec    int       `json:"elapsed_sec"`
	Sources       []string  `json:"sources"`
	Permission    string    `json:"permission,omitempty"`
	Retention     string    `json:"retention,omitempty"`
	MicDB         int       `json:"mic_db"`
	ParticipantDB int       `json:"participant_db"`
	Notes         string    `json:"notes,omitempty"`
	Markers       []Marker  `json:"markers,omitempty"`
}

type Marker struct {
	OffsetSec int    `json:"offset_sec"`
	Label     string `json:"label"`
}

type StartRecordingOpts struct {
	Title     string    `json:"title"`
	Sources   []string  `json:"sources"`
	Retention string    `json:"retention,omitempty"`
	AfterStop AfterStop `json:"after_stop"`
}

type AfterStop struct {
	Ingest     bool `json:"ingest"`
	Transcribe bool `json:"transcribe"`
	Summarize  bool `json:"summarize"`
	Index      bool `json:"index"`
}

type StartRecordingResult struct {
	MeetingID string   `json:"meeting_id"`
	Title     string   `json:"title"`
	Sources   []string `json:"sources"`
}

type StopRecordingOpts struct {
	NotesMD string `json:"notes_md,omitempty"`
}

type StopRecordingResult struct {
	MeetingID   string   `json:"meeting_id"`
	DurationSec int      `json:"duration_sec"`
	JobsKicked  []string `json:"jobs_kicked,omitempty"`
	OutputPath  string   `json:"output_path,omitempty"`
}

// ImportAudioOpts describes a request to ingest an existing audio file.
type ImportAudioOpts struct {
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
}

// ImportAudioResult bundles the new meeting + the pipeline job that
// will turn the audio into a searchable artifact.
type ImportAudioResult struct {
	Meeting Meeting `json:"meeting"`
	Job     Job     `json:"job"`
}

type PreflightResult struct {
	MicReady    bool   `json:"mic_ready"`
	SystemReady bool   `json:"system_ready"`
	HelperPath  string `json:"helper_path,omitempty"`
	Diagnostic  string `json:"diagnostic,omitempty"`
}

type MeterEvent struct {
	MicDB         int       `json:"mic_db"`
	ParticipantDB int       `json:"participant_db"`
	AmbientDB     int       `json:"ambient_db"`
	Clip          bool      `json:"clip,omitempty"`
	At            time.Time `json:"at"`
}

// ---------- Jobs ----------

type JobKind string

const (
	JobIngest     JobKind = "ingest"
	JobTranscribe JobKind = "transcribe"
	JobSummarize  JobKind = "summarize"
	JobIndex      JobKind = "index"
	JobVerify     JobKind = "verify"
	JobPipeline   JobKind = "pipeline"
	JobReindex    JobKind = "reindex"
	// JobDownloadModel fetches a local model (STT/diarizer/runtime) into the
	// data dir, streaming byte progress so the TUI can show "1.2/2.0 GB". Runs
	// on the backend, so in remote mode it shows the server fetching its weights.
	JobDownloadModel JobKind = "download_model"
)

type JobStatus string

const (
	JobQueued      JobStatus = "queued"
	JobRunning     JobStatus = "running"
	JobSucceeded   JobStatus = "succeeded"
	JobFailed      JobStatus = "failed"
	JobCanceled    JobStatus = "canceled"
	JobInterrupted JobStatus = "interrupted"
)

type Job struct {
	ID         string         `json:"id"`
	Kind       JobKind        `json:"kind"`
	MeetingID  string         `json:"meeting_id,omitempty"`
	Status     JobStatus      `json:"status"`
	Phase      string         `json:"phase,omitempty"`
	Progress   float64        `json:"progress"`
	Detail     string         `json:"detail,omitempty"`
	Error      string         `json:"error,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	StartedAt  *time.Time     `json:"started_at,omitempty"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
	Attempt    int            `json:"attempt"`
	Options    map[string]any `json:"options,omitempty"`
}

type CreateJobOpts struct {
	Kind      JobKind        `json:"kind"`
	MeetingID string         `json:"meeting_id,omitempty"`
	Options   map[string]any `json:"options,omitempty"`
}

type ListJobsOpts struct {
	Status    JobStatus `json:"status,omitempty"`
	MeetingID string    `json:"meeting_id,omitempty"`
	Limit     int       `json:"limit,omitempty"`
}

// ---------- Events ----------

type EventKind string

const (
	EventJob       EventKind = "job"
	EventRecorder  EventKind = "recorder"
	EventMeter     EventKind = "meter"
	EventStatusBar EventKind = "status_bar"
	EventClosed    EventKind = "closed"
)

type Event struct {
	Kind      EventKind       `json:"kind"`
	At        time.Time       `json:"at"`
	Job       *Job            `json:"job,omitempty"`
	Recorder  *RecordingState `json:"recorder,omitempty"`
	Meter     *MeterEvent     `json:"meter,omitempty"`
	StatusBar *StatusBar      `json:"status_bar,omitempty"`
}

type StatusBar struct {
	RecordingActive bool   `json:"recording_active"`
	RecordingTitle  string `json:"recording_title,omitempty"`
	ElapsedSec      int    `json:"elapsed_sec"`
	MeetingCount    int    `json:"meeting_count"`
	IndexState      string `json:"index_state"`
	JobsRunning     int    `json:"jobs_running"`
	JobsQueued      int    `json:"jobs_queued"`
	// SpeakersToID is the number of meeting-speaker mappings still unresolved
	// (not auto/manual) across all meetings — the meeting-side triage queue
	// that flags the Dashboard nav pill so newly-processed meetings get seen.
	SpeakersToID int `json:"speakers_to_id"`
	// PeopleToReview is the number of speaker profiles carrying an unconfirmed
	// mapping (auto-created/linked people not yet reviewed) — the person-side
	// signal that flags the People nav pill, distinct from SpeakersToID.
	PeopleToReview int `json:"people_to_review,omitempty"`
	// ConfigIssues is the number of actively-routed providers that need a
	// credential but have none configured — a broken route the user must fix.
	// It flags the Config nav pill.
	ConfigIssues int `json:"config_issues,omitempty"`
}

// ---------- Providers ----------

type ProviderInfo struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"display_name"`
	Kind         string   `json:"kind"`
	Capabilities []string `json:"capabilities"`
	HasKey       bool     `json:"has_key"`
	// RequiresKey is true when the provider needs a credential (a cloud
	// provider with a CredentialRef). Local providers (Parakeet) and fakes
	// require none, so they are never flagged "add key".
	RequiresKey            bool    `json:"requires_key"`
	KeySource              string  `json:"key_source,omitempty"`
	RequiresNetwork        bool    `json:"requires_network"`
	SendsRawAudioOffDevice bool    `json:"sends_raw_audio_off_device"`
	Notes                  string  `json:"notes,omitempty"`
	Models                 []Model `json:"models,omitempty"`
	IsActiveSpeech         bool    `json:"is_active_speech,omitempty"`
	IsActiveLLM            bool    `json:"is_active_llm,omitempty"`
	ActiveModel            string  `json:"active_model,omitempty"`
}

type Model struct {
	ID           string   `json:"id"`
	DisplayName  string   `json:"display_name"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type TestProviderResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	Detail    string `json:"detail,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ---------- Config ----------

type Config struct {
	SchemaVersion string          `json:"schema_version"`
	ArtifactRoot  string          `json:"artifact_root"`
	RecordingsDir string          `json:"recordings_dir"`
	ConfigDir     string          `json:"config_dir"`
	UI            ConfigUI        `json:"ui"`
	Routing       ConfigRouting   `json:"routing"`
	Compute       ConfigCompute   `json:"compute"`
	Retention     ConfigRetention `json:"retention"`
	Privacy       ConfigPrivacy   `json:"privacy"`
}

// ConfigPrivacy mirrors providers.LLMPrivacy over the wire: the OpenRouter
// provider-routing guards that keep transcripts on privacy-respecting
// endpoints. All default ON.
type ConfigPrivacy struct {
	ZDR                bool `json:"zdr"`
	DenyDataCollection bool `json:"deny_data_collection"`
	RequireParameters  bool `json:"require_parameters"`
}

type ConfigUI struct {
	Theme        string `json:"theme"`
	SidebarWidth int    `json:"sidebar_width"`
}

type ConfigRouting struct {
	SpeechProvider string `json:"speech_provider"`
	LLMProvider    string `json:"llm_provider"`
	LLMModel       string `json:"llm_model"`
	Profile        string `json:"profile,omitempty"`
}

type ConfigCompute struct {
	Provider         string      `json:"provider,omitempty"`
	SpeechLocation   string      `json:"speech_location"`
	DiarizeLocation  string      `json:"diarize_location"`
	EmbedLocation    string      `json:"embed_location"`
	EndpointURL      string      `json:"endpoint_url,omitempty"`
	EndpointTokenRef string      `json:"endpoint_token_ref,omitempty"`
	Modal            ModalConfig `json:"modal,omitempty"`
}

type ModalConfig struct {
	AppName                string `json:"app_name,omitempty"`
	Environment            string `json:"environment,omitempty"`
	GPU                    string `json:"gpu,omitempty"`
	EndpointURL            string `json:"endpoint_url,omitempty"`
	TokenRef               string `json:"token_ref,omitempty"`
	EndpointTokenRef       string `json:"endpoint_token_ref,omitempty"`
	DeploymentVersion      string `json:"deployment_version,omitempty"`
	ModelVolume            string `json:"model_volume,omitempty"`
	BenchmarkVolume        string `json:"benchmark_volume,omitempty"`
	UserTransferMode       string `json:"user_transfer_mode,omitempty"`
	BenchmarkTransferMode  string `json:"benchmark_transfer_mode,omitempty"`
	TemporaryThresholdMB   int    `json:"temporary_threshold_mb,omitempty"`
	ScaledownWindowSeconds int    `json:"scaledown_window_seconds,omitempty"`
	MinContainers          int    `json:"min_containers,omitempty"`
	ModelCache             string `json:"model_cache,omitempty"`
}

type ConfigRetention struct {
	DeleteAudioAfterTranscript bool `json:"delete_audio_after_transcript"`
}

type ConfigPatch struct {
	UI        *ConfigUI        `json:"ui,omitempty"`
	Routing   *ConfigRouting   `json:"routing,omitempty"`
	Compute   *ConfigCompute   `json:"compute,omitempty"`
	Retention *ConfigRetention `json:"retention,omitempty"`
	Privacy   *ConfigPrivacy   `json:"privacy,omitempty"`
}

type ModalSetupRequest struct {
	TokenID                string `json:"token_id,omitempty"`
	TokenSecret            string `json:"token_secret,omitempty"`
	EndpointURL            string `json:"endpoint_url,omitempty"`
	EndpointToken          string `json:"endpoint_token,omitempty"`
	AppName                string `json:"app_name,omitempty"`
	Environment            string `json:"environment,omitempty"`
	GPU                    string `json:"gpu,omitempty"`
	UserTransferMode       string `json:"user_transfer_mode,omitempty"`
	BenchmarkTransferMode  string `json:"benchmark_transfer_mode,omitempty"`
	TemporaryThresholdMB   int    `json:"temporary_threshold_mb,omitempty"`
	ScaledownWindowSeconds int    `json:"scaledown_window_seconds,omitempty"`
	MinContainers          int    `json:"min_containers,omitempty"`
	ModelCache             string `json:"model_cache,omitempty"`
	ConfigureRoutes        bool   `json:"configure_routes"`
}

type ModalStatus struct {
	Configured             bool   `json:"configured"`
	Ready                  bool   `json:"ready"`
	Provider               string `json:"provider"`
	AppName                string `json:"app_name,omitempty"`
	Environment            string `json:"environment,omitempty"`
	GPU                    string `json:"gpu,omitempty"`
	EndpointURL            string `json:"endpoint_url,omitempty"`
	CredentialSource       string `json:"credential_source,omitempty"`
	EndpointTokenSource    string `json:"endpoint_token_source,omitempty"`
	DeploymentVersion      string `json:"deployment_version,omitempty"`
	DesiredVersion         string `json:"desired_version"`
	NeedsUpdate            bool   `json:"needs_update"`
	UserDataPolicy         string `json:"user_data_policy"`
	BenchmarkDataPolicy    string `json:"benchmark_data_policy"`
	ModelDataPolicy        string `json:"model_data_policy"`
	UserTransferMode       string `json:"user_transfer_mode"`
	BenchmarkTransferMode  string `json:"benchmark_transfer_mode"`
	TemporaryThresholdMB   int    `json:"temporary_threshold_mb"`
	ScaledownWindowSeconds int    `json:"scaledown_window_seconds"`
	MinContainers          int    `json:"min_containers"`
	ModelCache             string `json:"model_cache"`
	LargeFileTransferMode  string `json:"large_file_transfer_mode"`
	Message                string `json:"message,omitempty"`
}

type ModalBenchmarkRequest struct {
	Suite  string  `json:"suite,omitempty"`
	Hours  float64 `json:"hours,omitempty"`
	Format string  `json:"format,omitempty"`
}

type ModalBenchmarkResult struct {
	Accepted bool   `json:"accepted"`
	RunID    string `json:"run_id,omitempty"`
	Message  string `json:"message"`
}

type Paths struct {
	ConfigDir     string `json:"config_dir"`
	ArtifactRoot  string `json:"artifact_root"`
	RecordingsDir string `json:"recordings_dir"`
	SQLitePath    string `json:"sqlite_path"`
	APISocket     string `json:"api_socket"`
}

type Storage struct {
	SchemaVersion string    `json:"schema_version"`
	RecordingsDir string    `json:"recordings_dir"`
	FreeBytes     int64     `json:"free_bytes,omitempty"`
	MeetingCount  int       `json:"meeting_count"`
	IndexState    string    `json:"index_state"`
	LastVerify    time.Time `json:"last_verify,omitempty"`
	LastVerifyOK  bool      `json:"last_verify_ok,omitempty"`
}

// ---------- System ----------

// System describes the backend a client is connected to: where it runs, what
// accelerator it resolved, and whether it can capture audio. A client uses this
// to render "backend: Mac M1, CoreML" vs "remote · linux-cuda @ host" and to
// decide whether to offer recording (a remote Linux server reports
// CaptureAvailable=false → the client must capture locally).
type System struct {
	SchemaVersion    string `json:"schema_version"`
	Mode             string `json:"mode"` // "local" | "remote"
	Hostname         string `json:"hostname"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	Accelerator      string `json:"accelerator"`       // cpu | cuda | coreml
	AcceleratorOK    bool   `json:"accelerator_ok"`    // true if verified at startup
	ModelTier        string `json:"model_tier"`        // accurate | fast
	StorageType      string `json:"storage_type"`      // local | s3 | remote
	CaptureAvailable bool   `json:"capture_available"` // can this backend record audio?
	Version          string `json:"version"`

	// Topology describes the three-plane split this backend participates in, so a
	// client can render where compute and data actually run (the Config screen's
	// deployment diagram). Compute reports per-capability placement; DataPlane
	// reports where artifacts are stored.
	Compute   ComputeTopology `json:"compute"`
	DataPlane DataPlane       `json:"data_plane"`
}

// ComputeTopology reports where each heavy capability runs relative to this
// backend. Identity (Embed) is called out separately because the recommended
// posture keeps it local even when STT/diarization are offloaded.
type ComputeTopology struct {
	Speech  ComputePlacement `json:"speech"`
	Diarize ComputePlacement `json:"diarize"`
	Embed   ComputePlacement `json:"embed"`
}

// ComputePlacement is one capability's location. Endpoint is a host:port hint
// for the diagram (never a token or full URL). Trust marks whether the endpoint
// is a system the user controls ("owned": this device or their own server) or a
// third-party ("cloud": Modal/RunPod/etc. — data leaves the user's systems).
type ComputePlacement struct {
	Location string `json:"location"`           // "local" | "remote"
	Endpoint string `json:"endpoint,omitempty"` // host:port when remote
	Trust    string `json:"trust,omitempty"`    // owned | cloud (remote only)
}

// DataPlane reports where this backend persists artifacts.
type DataPlane struct {
	Location string `json:"location"`           // "local" | "remote"
	Endpoint string `json:"endpoint,omitempty"` // host:port when remote
	Storage  string `json:"storage"`            // local | s3 | remote
	Trust    string `json:"trust,omitempty"`    // owned | cloud (remote only)
}

// Trust values for ComputePlacement / DataPlane.
const (
	TrustOwned = "owned" // this device or the user's own server
	TrustCloud = "cloud" // a third-party provider — data leaves the user's systems
)

// ---------- Health ----------

type Health struct {
	OK              bool      `json:"ok"`
	Version         string    `json:"version"`
	StartedAt       time.Time `json:"started_at"`
	UptimeSec       int       `json:"uptime_sec"`
	PID             int       `json:"pid"`
	RecordingActive bool      `json:"recording_active"`
}

// ---------- Speaker Profiles ----------

// Affiliation is one context a person belongs to (e.g. a university and a
// project are two affiliations, each with their own org + email).
type Affiliation struct {
	Context      string `json:"context"`
	Organization string `json:"organization"`
	Email        string `json:"email"`
}

// ProfileMeetingRef is a lightweight back-reference to a meeting a person
// appears in, for the People screen's "meetings they're part of" list.
type ProfileMeetingRef struct {
	MeetingID   string    `json:"meeting_id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	MatchStatus string    `json:"match_status"`
}

type SpeakerProfile struct {
	ID             string        `json:"id"`
	DisplayName    string        `json:"display_name"`
	Email          string        `json:"email"`
	Pronouns       string        `json:"pronouns"`
	Notes          string        `json:"notes"`
	Affiliations   []Affiliation `json:"affiliations,omitempty"`
	EmbeddingDim   int           `json:"embedding_dim"`
	EmbeddingModel string        `json:"embedding_model"`
	MeetingCount   int           `json:"meeting_count"`
	// UnconfirmedMeetings counts this person's mappings still in an
	// unconfirmed state (not auto/manual) — i.e. auto-created/linked but not
	// yet reviewed. Drives the People screen's "to review" attention signal.
	UnconfirmedMeetings int `json:"unconfirmed_meetings,omitempty"`
	// Meetings is populated only by GetSpeakerProfile (not List), so the
	// People detail can show where a person appears without an extra round-trip.
	Meetings   []ProfileMeetingRef `json:"meetings,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
	LastSeenAt *time.Time          `json:"last_seen_at,omitempty"`
}

type ListSpeakerProfilesResult struct {
	Profiles []SpeakerProfile `json:"profiles"`
	Total    int              `json:"total"`
}

type CreateSpeakerProfileRequest struct {
	DisplayName  string        `json:"display_name"`
	Email        string        `json:"email,omitempty"`
	Pronouns     string        `json:"pronouns,omitempty"`
	Notes        string        `json:"notes,omitempty"`
	Affiliations []Affiliation `json:"affiliations,omitempty"`
	// FromMeetingSpeaker, when set, seeds the new profile's voiceprint from a
	// meeting-speaker mapping's stored embedding and links that mapping to the
	// new profile in one step (used by the "+ new person" assign action).
	FromMeetingID string `json:"from_meeting_id,omitempty"`
	FromSpeakerID string `json:"from_speaker_id,omitempty"`
}

type SpeakerProfilePatch struct {
	DisplayName  *string        `json:"display_name,omitempty"`
	Email        *string        `json:"email,omitempty"`
	Pronouns     *string        `json:"pronouns,omitempty"`
	Notes        *string        `json:"notes,omitempty"`
	Affiliations *[]Affiliation `json:"affiliations,omitempty"`
}

type MergeSpeakerProfilesRequest struct {
	TargetProfileID string `json:"target_profile_id"`
	SourceProfileID string `json:"source_profile_id"`
}

// ---------- Meeting Speaker Mappings ----------

// SpeakerCandidate is one ranked "who is this?" suggestion for an unresolved
// meeting speaker: a profile, its blended score, and a human-readable reason
// (e.g. why the meeting context boosted it).
type SpeakerCandidate struct {
	ProfileID   string  `json:"profile_id"`
	DisplayName string  `json:"display_name"`
	Score       float64 `json:"score"`
	Reason      string  `json:"reason,omitempty"`
	Rank        int     `json:"rank"`
}

type MeetingSpeakerMapping struct {
	MeetingID        string   `json:"meeting_id"`
	MeetingSpeakerID string   `json:"meeting_speaker_id"`
	ProviderLabel    string   `json:"provider_label"`
	ProfileID        *string  `json:"profile_id,omitempty"`
	MatchConfidence  *float64 `json:"match_confidence,omitempty"`
	MatchStatus      string   `json:"match_status"`
	// ProfileName is the resolved display name of the linked profile, so the UI
	// can render "name once → everywhere" without a second lookup.
	ProfileName string `json:"profile_name,omitempty"`
	// Candidates is the ranked top-N of who this speaker most likely is,
	// populated for unresolved (pending/new) speakers.
	Candidates []SpeakerCandidate `json:"candidates,omitempty"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

type MeetingSpeakerMappings struct {
	MeetingID string                  `json:"meeting_id"`
	Mappings  []MeetingSpeakerMapping `json:"mappings"`
}

type MeetingSpeakerMappingsPatch struct {
	Mappings []MeetingSpeakerMappingPatchEntry `json:"mappings"`
}

type MeetingSpeakerMappingPatchEntry struct {
	MeetingSpeakerID string   `json:"meeting_speaker_id"`
	ProfileID        *string  `json:"profile_id,omitempty"`
	MatchConfidence  *float64 `json:"match_confidence,omitempty"`
	MatchStatus      *string  `json:"match_status,omitempty"`
}

// ---------- Agent API ----------
// Optimised types for AI agent access. Agents get paginated lists, then
// pull full context for individual meetings. Every response is self-contained
// — no secondary fetch required for the common "read and cite" workflow.

// AgentListOpts controls the cursor-based listing for agents.
type AgentListOpts struct {
	// After filters to meetings created after this time (for pagination).
	After time.Time `json:"after,omitempty"`
	// Before filters to meetings created before this time.
	Before time.Time `json:"before,omitempty"`
	// Limit caps the number of results. Defaults to 20, max 100.
	Limit int `json:"limit,omitempty"`
}

// AgentMeetingSummary is the lightweight per-meeting entry returned in a list.
type AgentMeetingSummary struct {
	ID              string        `json:"id"`
	Title           string        `json:"title"`
	CreatedAt       time.Time     `json:"created_at"`
	DurationSeconds int           `json:"duration_seconds"`
	Status          MeetingStatus `json:"status"`
	Speakers        int           `json:"speakers"`
	ShortSummary    string        `json:"short_summary,omitempty"`
	DecisionCount   int           `json:"decision_count"`
	ActionCount     int           `json:"action_count"`
	RiskCount       int           `json:"risk_count"`
	QuestionCount   int           `json:"question_count"`
}

// AgentListResult is the paginated meeting list response.
type AgentListResult struct {
	Meetings []AgentMeetingSummary `json:"meetings"`
	Total    int                   `json:"total"`
	// NextCursor is the CreatedAt of the last item; pass as ?before= to
	// fetch the next page. Empty when this is the last page.
	NextCursor string `json:"next_cursor,omitempty"`
}

// AgentMeetingContext is the full single-meeting response for agents:
// metadata + summary (structured, cited) + transcript (indexed by speaker).
// Fetching this one endpoint gives everything needed for "read and cite".
type AgentMeetingContext struct {
	ID              string                `json:"id"`
	Title           string                `json:"title"`
	CreatedAt       time.Time             `json:"created_at"`
	DurationSeconds int                   `json:"duration_seconds"`
	Status          MeetingStatus         `json:"status"`
	Summary         *AgentSummaryBlock    `json:"summary,omitempty"`
	Transcript      *AgentTranscriptBlock `json:"transcript,omitempty"`
}

// AgentSummaryBlock is the summary portion of AgentMeetingContext.
type AgentSummaryBlock struct {
	ShortSummary  string          `json:"short_summary"`
	Decisions     []AgentCitation `json:"decisions"`
	ActionItems   []AgentCitation `json:"action_items"`
	Risks         []AgentCitation `json:"risks"`
	OpenQuestions []AgentCitation `json:"open_questions"`
}

// AgentCitation is a single summary item with source references.
type AgentCitation struct {
	Text        string   `json:"text"`
	SpeakerIDs  []string `json:"speaker_ids,omitempty"`
	SegmentRefs []string `json:"segment_refs,omitempty"`
}

// AgentTranscriptBlock is the transcript portion of AgentMeetingContext.
type AgentTranscriptBlock struct {
	Speakers []Speaker      `json:"speakers"`
	Segments []AgentSegment `json:"segments"`
}

// AgentSegment is a single transcript segment with display name pre-resolved.
type AgentSegment struct {
	ID           string  `json:"id"`
	Speaker      string  `json:"speaker"`
	TimestampSec float64 `json:"timestamp_sec"`
	EndSec       float64 `json:"end_sec"`
	Text         string  `json:"text"`
}

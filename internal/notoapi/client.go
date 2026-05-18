package notoapi

import "context"

// Client is the contract every noto frontend (CLI, TUI) talks to.
// Two implementations: direct (in-process) and http (UDS or TCP).
type Client interface {
	// Meetings
	ListMeetings(ctx context.Context, opts ListMeetingsOpts) (ListMeetingsResult, error)
	GetMeeting(ctx context.Context, id string) (Meeting, error)
	GetTranscript(ctx context.Context, id string) (Transcript, error)
	GetSummary(ctx context.Context, id string) (Summary, error)
	GetMeetingFiles(ctx context.Context, id string) (MeetingFiles, error)
	GetAgentHandoff(ctx context.Context, id string) (AgentHandoff, error)
	DeleteMeeting(ctx context.Context, id string) error
	VerifyMeeting(ctx context.Context, id string) (Job, error)

	// Search
	Search(ctx context.Context, opts SearchOpts) (SearchResult, error)

	// Recording / Import
	ImportAudio(ctx context.Context, opts ImportAudioOpts) (ImportAudioResult, error)
	StartRecording(ctx context.Context, opts StartRecordingOpts) (StartRecordingResult, error)
	StopRecording(ctx context.Context, opts StopRecordingOpts) (StopRecordingResult, error)
	PauseRecording(ctx context.Context) error
	ResumeRecording(ctx context.Context) error
	AddMarker(ctx context.Context, label string) error
	PreflightRecording(ctx context.Context) (PreflightResult, error)
	GetRecording(ctx context.Context) (RecordingState, error)
	StreamMeters(ctx context.Context) (<-chan MeterEvent, error)

	// Jobs
	CreateJob(ctx context.Context, opts CreateJobOpts) (Job, error)
	ListJobs(ctx context.Context, opts ListJobsOpts) ([]Job, error)
	GetJob(ctx context.Context, id string) (Job, error)
	CancelJob(ctx context.Context, id string) error
	StreamEvents(ctx context.Context) (<-chan Event, error)

	// Providers
	ListProviders(ctx context.Context) ([]ProviderInfo, error)
	SetProviderKey(ctx context.Context, providerID, value string) error
	DeleteProviderKey(ctx context.Context, providerID string) error
	TestProvider(ctx context.Context, providerID string) (TestProviderResult, error)
	SetActiveSpeech(ctx context.Context, providerID string) error
	SetActiveLLMModel(ctx context.Context, modelID string) error

	// Config
	GetConfig(ctx context.Context) (Config, error)
	PatchConfig(ctx context.Context, patch ConfigPatch) (Config, error)
	GetPaths(ctx context.Context) (Paths, error)

	// Storage
	GetStorage(ctx context.Context) (Storage, error)
	VerifyStorage(ctx context.Context) (Job, error)
	ReindexStorage(ctx context.Context) (Job, error)

	// Lifecycle / health
	Health(ctx context.Context) (Health, error)
	Close() error
}

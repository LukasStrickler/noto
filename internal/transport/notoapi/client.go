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
	UpdateSpeakerName(ctx context.Context, meetingID, speakerID, displayName string) error

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
	GetModalComputeStatus(ctx context.Context) (ModalStatus, error)
	SetupModalCompute(ctx context.Context, req ModalSetupRequest) (ModalStatus, error)
	RunModalBenchmark(ctx context.Context, req ModalBenchmarkRequest) (ModalBenchmarkResult, error)

	// Benchmark / measurement spine (Program A)
	BenchEstimate(ctx context.Context, req BenchEstimateRequest) (BenchEstimateResult, error)
	BenchPreflight(ctx context.Context, req BenchPreflightRequest) (BenchPreflightResult, error)
	BenchRun(ctx context.Context, req BenchRunRequest) (BenchRunResult, error)
	BenchCompare(ctx context.Context, req BenchCompareRequest) (BenchCompareResult, error)
	BenchLedgerWinners(ctx context.Context, opts BenchLedgerWinnersOpts) (BenchLedgerWinnersResult, error)
	BenchLedgerAppend(ctx context.Context, req BenchLedgerAppendRequest) (BenchLedgerAppendResult, error)
	BenchDatasetList(ctx context.Context) (BenchDatasetListResult, error)
	BenchAudit(ctx context.Context, runID string) (BenchAuditResult, error)
	BenchRetrace(ctx context.Context, runID string) (BenchAuditResult, error)
	BenchScale(ctx context.Context, req BenchScaleRequest) (BenchScaleResult, error)
	BenchInsights(ctx context.Context, runID string) (BenchInsightsResult, error)
	BenchRepair(ctx context.Context, runID string) (BenchRepairResult, error)
	BenchCalibration(ctx context.Context, runID string) (BenchCalibrationResult, error)
	BenchOverlap(ctx context.Context, runID string) (BenchOverlapResult, error)

	// Storage
	GetStorage(ctx context.Context) (Storage, error)
	VerifyStorage(ctx context.Context) (Job, error)
	ReindexStorage(ctx context.Context) (Job, error)

	// Lifecycle / health
	Health(ctx context.Context) (Health, error)
	// GetSystem reports the backend's location, accelerator, and capabilities.
	GetSystem(ctx context.Context) (System, error)
	Close() error

	// Speaker profiles
	ListSpeakerProfiles(ctx context.Context) ([]SpeakerProfile, error)
	GetSpeakerProfile(ctx context.Context, id string) (SpeakerProfile, error)
	CreateSpeakerProfile(ctx context.Context, req CreateSpeakerProfileRequest) (SpeakerProfile, error)
	PatchSpeakerProfile(ctx context.Context, id string, patch SpeakerProfilePatch) (SpeakerProfile, error)
	DeleteSpeakerProfile(ctx context.Context, id string) error
	MergeSpeakerProfiles(ctx context.Context, targetID, sourceID string) (SpeakerProfile, error)

	// Meeting speaker mappings
	GetMeetingSpeakerMappings(ctx context.Context, meetingID string) (MeetingSpeakerMappings, error)
	PatchMeetingSpeakerMappings(ctx context.Context, meetingID string, patch MeetingSpeakerMappingsPatch) (MeetingSpeakerMappings, error)

	// Agent API — optimised for AI agent consumption
	AgentListMeetings(ctx context.Context, opts AgentListOpts) (AgentListResult, error)
	AgentGetMeeting(ctx context.Context, id string) (AgentMeetingContext, error)
}

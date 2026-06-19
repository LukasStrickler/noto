// Package apiclient hosts the noto Client implementations.
//
//	direct: in-process function calls into service.Service
//	http:   HTTP/JSON+SSE over UDS or TCP
//
// CLIs use direct for zero-latency one-shots; the TUI uses http (UDS).
package apiclient

import (
	"context"

	"github.com/lukasstrickler/noto/internal/app/service"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Compile-time proof that direct satisfies the full Client contract — a
// crisp error here beats a cryptic one at a distant call site when a
// method is added to the interface.
var _ notoapi.Client = (*direct)(nil)

// NewDirect wraps a service.Service to satisfy notoapi.Client.
func NewDirect(svc *service.Service) notoapi.Client {
	return &direct{svc: svc}
}

type direct struct {
	svc *service.Service
}

func (d *direct) ListMeetings(ctx context.Context, opts notoapi.ListMeetingsOpts) (notoapi.ListMeetingsResult, error) {
	return d.svc.ListMeetings(ctx, opts)
}
func (d *direct) GetMeeting(ctx context.Context, id string) (notoapi.Meeting, error) {
	return d.svc.GetMeeting(ctx, id)
}
func (d *direct) GetTranscript(ctx context.Context, id string) (notoapi.Transcript, error) {
	return d.svc.GetTranscript(ctx, id)
}
func (d *direct) GetSummary(ctx context.Context, id string) (notoapi.Summary, error) {
	return d.svc.GetSummary(ctx, id)
}
func (d *direct) GetMeetingFiles(ctx context.Context, id string) (notoapi.MeetingFiles, error) {
	return d.svc.GetMeetingFiles(ctx, id)
}
func (d *direct) GetAgentHandoff(ctx context.Context, id string) (notoapi.AgentHandoff, error) {
	return d.svc.GetAgentHandoff(ctx, id)
}
func (d *direct) DeleteMeeting(ctx context.Context, id string) error {
	return d.svc.DeleteMeeting(ctx, id)
}
func (d *direct) VerifyMeeting(ctx context.Context, id string) (notoapi.Job, error) {
	return d.svc.VerifyMeeting(ctx, id)
}
func (d *direct) UpdateSpeakerName(ctx context.Context, meetingID, speakerID, displayName string) error {
	return d.svc.UpdateSpeakerName(ctx, meetingID, speakerID, displayName)
}

func (d *direct) Search(ctx context.Context, opts notoapi.SearchOpts) (notoapi.SearchResult, error) {
	return d.svc.Search(ctx, opts)
}

func (d *direct) ImportAudio(ctx context.Context, opts notoapi.ImportAudioOpts) (notoapi.ImportAudioResult, error) {
	m, job, err := d.svc.ImportAudio(ctx, opts.Path, opts.Title)
	if err != nil {
		return notoapi.ImportAudioResult{}, err
	}
	return notoapi.ImportAudioResult{Meeting: m, Job: job}, nil
}
func (d *direct) StartRecording(ctx context.Context, opts notoapi.StartRecordingOpts) (notoapi.StartRecordingResult, error) {
	return d.svc.StartRecording(ctx, opts)
}
func (d *direct) StopRecording(ctx context.Context, opts notoapi.StopRecordingOpts) (notoapi.StopRecordingResult, error) {
	return d.svc.StopRecording(ctx, opts)
}
func (d *direct) PauseRecording(ctx context.Context) error  { return d.svc.PauseRecording(ctx) }
func (d *direct) ResumeRecording(ctx context.Context) error { return d.svc.ResumeRecording(ctx) }
func (d *direct) AddMarker(ctx context.Context, label string) error {
	return d.svc.AddMarker(ctx, label)
}
func (d *direct) PreflightRecording(ctx context.Context) (notoapi.PreflightResult, error) {
	return d.svc.PreflightRecording(ctx)
}
func (d *direct) GetRecording(ctx context.Context) (notoapi.RecordingState, error) {
	return d.svc.GetRecording(ctx)
}
func (d *direct) StreamMeters(ctx context.Context) (<-chan notoapi.MeterEvent, error) {
	return d.svc.StreamMeters(ctx)
}

func (d *direct) CreateJob(ctx context.Context, opts notoapi.CreateJobOpts) (notoapi.Job, error) {
	return d.svc.CreateJob(ctx, opts)
}
func (d *direct) ListJobs(ctx context.Context, opts notoapi.ListJobsOpts) ([]notoapi.Job, error) {
	return d.svc.ListJobs(ctx, opts)
}
func (d *direct) GetJob(ctx context.Context, id string) (notoapi.Job, error) {
	return d.svc.GetJob(ctx, id)
}
func (d *direct) CancelJob(ctx context.Context, id string) error {
	return d.svc.CancelJob(ctx, id)
}
func (d *direct) StreamEvents(ctx context.Context) (<-chan notoapi.Event, error) {
	return d.svc.StreamEvents(ctx)
}

func (d *direct) ListProviders(ctx context.Context) ([]notoapi.ProviderInfo, error) {
	return d.svc.ListProviders(ctx)
}
func (d *direct) SetProviderKey(ctx context.Context, providerID, value string) error {
	return d.svc.SetProviderKey(ctx, providerID, value)
}
func (d *direct) DeleteProviderKey(ctx context.Context, providerID string) error {
	return d.svc.DeleteProviderKey(ctx, providerID)
}
func (d *direct) TestProvider(ctx context.Context, providerID string) (notoapi.TestProviderResult, error) {
	return d.svc.TestProvider(ctx, providerID)
}
func (d *direct) SetActiveSpeech(ctx context.Context, providerID string) error {
	return d.svc.SetActiveSpeech(ctx, providerID)
}
func (d *direct) SetActiveLLMModel(ctx context.Context, modelID string) error {
	return d.svc.SetActiveLLMModel(ctx, modelID)
}

func (d *direct) GetConfig(ctx context.Context) (notoapi.Config, error) { return d.svc.GetConfig(ctx) }
func (d *direct) PatchConfig(ctx context.Context, patch notoapi.ConfigPatch) (notoapi.Config, error) {
	return d.svc.PatchConfig(ctx, patch)
}
func (d *direct) GetPaths(ctx context.Context) (notoapi.Paths, error) { return d.svc.GetPaths(ctx) }
func (d *direct) GetModalComputeStatus(ctx context.Context) (notoapi.ModalStatus, error) {
	return d.svc.GetModalComputeStatus(ctx)
}
func (d *direct) SetupModalCompute(ctx context.Context, req notoapi.ModalSetupRequest) (notoapi.ModalStatus, error) {
	return d.svc.SetupModalCompute(ctx, req)
}
func (d *direct) RunModalBenchmark(ctx context.Context, req notoapi.ModalBenchmarkRequest) (notoapi.ModalBenchmarkResult, error) {
	return d.svc.RunModalBenchmark(ctx, req)
}
func (d *direct) BenchEstimate(ctx context.Context, req notoapi.BenchEstimateRequest) (notoapi.BenchEstimateResult, error) {
	return d.svc.BenchEstimate(ctx, req)
}
func (d *direct) BenchPreflight(ctx context.Context, req notoapi.BenchPreflightRequest) (notoapi.BenchPreflightResult, error) {
	return d.svc.BenchPreflight(ctx, req)
}
func (d *direct) BenchRun(ctx context.Context, req notoapi.BenchRunRequest) (notoapi.BenchRunResult, error) {
	return d.svc.BenchRun(ctx, req)
}
func (d *direct) BenchCompare(ctx context.Context, req notoapi.BenchCompareRequest) (notoapi.BenchCompareResult, error) {
	return d.svc.BenchCompare(ctx, req)
}
func (d *direct) BenchLedgerWinners(ctx context.Context, opts notoapi.BenchLedgerWinnersOpts) (notoapi.BenchLedgerWinnersResult, error) {
	return d.svc.BenchLedgerWinners(ctx, opts)
}
func (d *direct) BenchLedgerAppend(ctx context.Context, req notoapi.BenchLedgerAppendRequest) (notoapi.BenchLedgerAppendResult, error) {
	return d.svc.BenchLedgerAppend(ctx, req)
}
func (d *direct) BenchDatasetList(ctx context.Context) (notoapi.BenchDatasetListResult, error) {
	return d.svc.BenchDatasetList(ctx)
}
func (d *direct) BenchAudit(ctx context.Context, runID string) (notoapi.BenchAuditResult, error) {
	return d.svc.BenchAudit(ctx, runID)
}

func (d *direct) BenchRetrace(ctx context.Context, runID string) (notoapi.BenchAuditResult, error) {
	return d.svc.BenchRetrace(ctx, runID)
}

func (d *direct) BenchScale(ctx context.Context, req notoapi.BenchScaleRequest) (notoapi.BenchScaleResult, error) {
	return d.svc.BenchScale(ctx, req)
}

func (d *direct) BenchInsights(ctx context.Context, runID string) (notoapi.BenchInsightsResult, error) {
	return d.svc.BenchInsights(ctx, runID)
}

func (d *direct) BenchRepair(ctx context.Context, runID string) (notoapi.BenchRepairResult, error) {
	return d.svc.BenchRepair(ctx, runID)
}

func (d *direct) BenchCalibration(ctx context.Context, runID string) (notoapi.BenchCalibrationResult, error) {
	return d.svc.BenchCalibration(ctx, runID)
}

func (d *direct) BenchOverlap(ctx context.Context, runID string) (notoapi.BenchOverlapResult, error) {
	return d.svc.BenchOverlap(ctx, runID)
}

func (d *direct) BenchRepairAttempt(ctx context.Context, runID, altRunID string) (notoapi.BenchRepairAttemptResult, error) {
	return d.svc.BenchRepairAttempt(ctx, runID, altRunID)
}

func (d *direct) BenchDiarRepairAttempt(ctx context.Context, runID, altRunID string) (notoapi.BenchDiarRepairAttemptResult, error) {
	return d.svc.BenchDiarRepairAttempt(ctx, runID, altRunID)
}

func (d *direct) GetSystem(ctx context.Context) (notoapi.System, error) {
	return d.svc.GetSystem(ctx)
}
func (d *direct) GetStorage(ctx context.Context) (notoapi.Storage, error) {
	return d.svc.GetStorage(ctx)
}
func (d *direct) VerifyStorage(ctx context.Context) (notoapi.Job, error) {
	return d.svc.VerifyStorage(ctx)
}
func (d *direct) ReindexStorage(ctx context.Context) (notoapi.Job, error) {
	return d.svc.ReindexStorage(ctx)
}

func (d *direct) Health(ctx context.Context) (notoapi.Health, error) { return d.svc.Health(ctx) }
func (d *direct) Close() error                                       { return nil }

func (d *direct) ListSpeakerProfiles(ctx context.Context) ([]notoapi.SpeakerProfile, error) {
	return d.svc.ListSpeakerProfiles(ctx)
}
func (d *direct) GetSpeakerProfile(ctx context.Context, id string) (notoapi.SpeakerProfile, error) {
	return d.svc.GetSpeakerProfile(ctx, id)
}
func (d *direct) CreateSpeakerProfile(ctx context.Context, req notoapi.CreateSpeakerProfileRequest) (notoapi.SpeakerProfile, error) {
	return d.svc.CreateSpeakerProfile(ctx, req)
}
func (d *direct) PatchSpeakerProfile(ctx context.Context, id string, patch notoapi.SpeakerProfilePatch) (notoapi.SpeakerProfile, error) {
	return d.svc.PatchSpeakerProfile(ctx, id, patch)
}
func (d *direct) DeleteSpeakerProfile(ctx context.Context, id string) error {
	return d.svc.DeleteSpeakerProfile(ctx, id)
}
func (d *direct) MergeSpeakerProfiles(ctx context.Context, targetID, sourceID string) (notoapi.SpeakerProfile, error) {
	return d.svc.MergeSpeakerProfiles(ctx, targetID, sourceID)
}

func (d *direct) GetMeetingSpeakerMappings(ctx context.Context, meetingID string) (notoapi.MeetingSpeakerMappings, error) {
	return d.svc.GetMeetingSpeakerMappings(ctx, meetingID)
}
func (d *direct) PatchMeetingSpeakerMappings(ctx context.Context, meetingID string, patch notoapi.MeetingSpeakerMappingsPatch) (notoapi.MeetingSpeakerMappings, error) {
	return d.svc.PatchMeetingSpeakerMappings(ctx, meetingID, patch)
}

func (d *direct) AgentListMeetings(ctx context.Context, opts notoapi.AgentListOpts) (notoapi.AgentListResult, error) {
	return d.svc.AgentListMeetings(ctx, opts)
}
func (d *direct) AgentGetMeeting(ctx context.Context, id string) (notoapi.AgentMeetingContext, error) {
	return d.svc.AgentGetMeeting(ctx, id)
}

// IsRemote is always false: the direct client runs in the same process as the backend.
func (d *direct) IsRemote() bool { return false }

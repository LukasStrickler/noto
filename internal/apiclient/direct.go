// Package apiclient hosts the noto Client implementations.
//   direct: in-process function calls into service.Service
//   http:   HTTP/JSON+SSE over UDS or TCP
// CLIs use direct for zero-latency one-shots; the TUI uses http (UDS).
package apiclient

import (
	"context"

	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/service"
)

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

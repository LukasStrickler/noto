package testutil

import (
	"context"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// FakeClient is a programmable in-memory notoapi.Client for driving TUI
// and CLI tests without standing up a real backend.
//
// It embeds the notoapi.Client interface, so it satisfies the full
// 47-method contract for free — only the methods a test actually
// exercises are implemented below. Any *other* method that gets called
// hits the nil embedded interface and panics, so a test that wanders into
// an unstubbed path fails loudly instead of getting silent zero values.
//
// The zero value works for read-only screens; use NewFakeClient for sane
// defaults (Health OK, initialized maps).
type FakeClient struct {
	notoapi.Client // embedded contract; unimplemented methods panic if called

	Meetings    []notoapi.Meeting
	MeetingByID map[string]notoapi.Meeting
	Transcripts map[string]notoapi.Transcript
	Summaries   map[string]notoapi.Summary
	Files       map[string]notoapi.MeetingFiles
	Handoffs    map[string]notoapi.AgentHandoff
	Jobs        []notoapi.Job
	Providers   []notoapi.ProviderInfo
	Config      notoapi.Config
	Paths       notoapi.Paths
	Recording   notoapi.RecordingState
	SearchValue notoapi.SearchResult
	HealthInfo  notoapi.Health
	SystemInfo  notoapi.System
	ModalInfo   notoapi.ModalStatus

	// BenchCompareValue overrides the BenchCompare stub when non-nil, so a test
	// can drive the compare CLI with a specific (e.g. incomparable) result.
	BenchCompareValue *notoapi.BenchCompareResult

	// Speaker identity (cross-meeting). Profiles is the directory; Mappings
	// is keyed by meeting id. The action methods below record their inputs so
	// tests can assert the assignment flow fired correctly.
	Profiles []notoapi.SpeakerProfile
	Mappings map[string]notoapi.MeetingSpeakerMappings

	PatchedMappings []patchedMapping
	CreatedProfiles []notoapi.CreateSpeakerProfileRequest
	RenamedSpeakers []renamedSpeaker

	// Optional overrides take precedence when set — for tests that need to
	// assert inputs or inject errors.
	SearchFn       func(context.Context, notoapi.SearchOpts) (notoapi.SearchResult, error)
	ListMeetingsFn func(context.Context, notoapi.ListMeetingsOpts) (notoapi.ListMeetingsResult, error)

	// Events / Meters are returned by the stream methods. When nil, an
	// open channel that never delivers is returned (the stream simply
	// stays quiet for the duration of the test).
	Events <-chan notoapi.Event
	Meters <-chan notoapi.MeterEvent
}

var _ notoapi.Client = (*FakeClient)(nil)

// NewFakeClient returns a FakeClient with initialized maps and a healthy
// Health response.
func NewFakeClient() *FakeClient {
	return &FakeClient{
		MeetingByID: map[string]notoapi.Meeting{},
		Transcripts: map[string]notoapi.Transcript{},
		Summaries:   map[string]notoapi.Summary{},
		Files:       map[string]notoapi.MeetingFiles{},
		Handoffs:    map[string]notoapi.AgentHandoff{},
		HealthInfo:  notoapi.Health{OK: true, Version: "fake"},
	}
}

func (f *FakeClient) Health(context.Context) (notoapi.Health, error) {
	return f.HealthInfo, nil
}

func (f *FakeClient) GetSystem(context.Context) (notoapi.System, error) {
	return f.SystemInfo, nil
}

func (f *FakeClient) ListMeetings(ctx context.Context, opts notoapi.ListMeetingsOpts) (notoapi.ListMeetingsResult, error) {
	if f.ListMeetingsFn != nil {
		return f.ListMeetingsFn(ctx, opts)
	}
	return notoapi.ListMeetingsResult{Meetings: f.Meetings}, nil
}

func (f *FakeClient) GetMeeting(_ context.Context, id string) (notoapi.Meeting, error) {
	if m, ok := f.MeetingByID[id]; ok {
		return m, nil
	}
	for _, m := range f.Meetings {
		if m.ID == id {
			return m, nil
		}
	}
	return notoapi.Meeting{}, notoapi.NewError(notoapi.CodeNotFound, "meeting not found", map[string]any{"id": id})
}

func (f *FakeClient) GetTranscript(_ context.Context, id string) (notoapi.Transcript, error) {
	return f.Transcripts[id], nil
}

func (f *FakeClient) GetSummary(_ context.Context, id string) (notoapi.Summary, error) {
	return f.Summaries[id], nil
}

func (f *FakeClient) GetMeetingFiles(_ context.Context, id string) (notoapi.MeetingFiles, error) {
	return f.Files[id], nil
}

func (f *FakeClient) GetAgentHandoff(_ context.Context, id string) (notoapi.AgentHandoff, error) {
	return f.Handoffs[id], nil
}

func (f *FakeClient) Search(ctx context.Context, opts notoapi.SearchOpts) (notoapi.SearchResult, error) {
	if f.SearchFn != nil {
		return f.SearchFn(ctx, opts)
	}
	return f.SearchValue, nil
}

func (f *FakeClient) ListJobs(context.Context, notoapi.ListJobsOpts) ([]notoapi.Job, error) {
	return f.Jobs, nil
}

func (f *FakeClient) ListProviders(context.Context) ([]notoapi.ProviderInfo, error) {
	return f.Providers, nil
}

func (f *FakeClient) GetConfig(context.Context) (notoapi.Config, error) { return f.Config, nil }
func (f *FakeClient) GetPaths(context.Context) (notoapi.Paths, error)   { return f.Paths, nil }
func (f *FakeClient) GetModalComputeStatus(context.Context) (notoapi.ModalStatus, error) {
	return f.ModalInfo, nil
}
func (f *FakeClient) SetupModalCompute(_ context.Context, req notoapi.ModalSetupRequest) (notoapi.ModalStatus, error) {
	f.ModalInfo.Configured = true
	f.ModalInfo.Provider = "modal"
	f.ModalInfo.EndpointURL = req.EndpointURL
	f.ModalInfo.Ready = req.EndpointURL != ""
	return f.ModalInfo, nil
}
func (f *FakeClient) RunModalBenchmark(context.Context, notoapi.ModalBenchmarkRequest) (notoapi.ModalBenchmarkResult, error) {
	return notoapi.ModalBenchmarkResult{Accepted: false, Message: "fake"}, nil
}
func (f *FakeClient) BenchPreflight(context.Context, notoapi.BenchPreflightRequest) (notoapi.BenchPreflightResult, error) {
	return notoapi.BenchPreflightResult{SchemaVersion: "bench_preflight.v1", ComparableToWinner: true}, nil
}
func (f *FakeClient) BenchRun(context.Context, notoapi.BenchRunRequest) (notoapi.BenchRunResult, error) {
	return notoapi.BenchRunResult{SchemaVersion: "bench_run.v1", RunID: "fake", TraceValid: true}, nil
}
func (f *FakeClient) BenchCompare(context.Context, notoapi.BenchCompareRequest) (notoapi.BenchCompareResult, error) {
	if f.BenchCompareValue != nil {
		return *f.BenchCompareValue, nil
	}
	return notoapi.BenchCompareResult{SchemaVersion: "compare.v1", Decision: "retry"}, nil
}
func (f *FakeClient) BenchLedgerWinners(context.Context, notoapi.BenchLedgerWinnersOpts) (notoapi.BenchLedgerWinnersResult, error) {
	return notoapi.BenchLedgerWinnersResult{}, nil
}
func (f *FakeClient) BenchLedgerAppend(context.Context, notoapi.BenchLedgerAppendRequest) (notoapi.BenchLedgerAppendResult, error) {
	return notoapi.BenchLedgerAppendResult{SchemaVersion: "ledger_entry.v1"}, nil
}
func (f *FakeClient) BenchDatasetList(context.Context) (notoapi.BenchDatasetListResult, error) {
	return notoapi.BenchDatasetListResult{}, nil
}
func (f *FakeClient) BenchAudit(context.Context, string) (notoapi.BenchAuditResult, error) {
	return notoapi.BenchAuditResult{SchemaVersion: "bench_audit.v1"}, nil
}

func (f *FakeClient) GetRecording(context.Context) (notoapi.RecordingState, error) {
	return f.Recording, nil
}

func (f *FakeClient) StreamEvents(context.Context) (<-chan notoapi.Event, error) {
	if f.Events != nil {
		return f.Events, nil
	}
	return make(chan notoapi.Event), nil
}

func (f *FakeClient) StreamMeters(context.Context) (<-chan notoapi.MeterEvent, error) {
	if f.Meters != nil {
		return f.Meters, nil
	}
	return make(chan notoapi.MeterEvent), nil
}

func (f *FakeClient) Close() error { return nil }

// --- Speaker identity ---

type patchedMapping struct {
	MeetingID string
	Patch     notoapi.MeetingSpeakerMappingsPatch
}

type renamedSpeaker struct {
	MeetingID   string
	SpeakerID   string
	DisplayName string
}

func (f *FakeClient) ListSpeakerProfiles(context.Context) ([]notoapi.SpeakerProfile, error) {
	return f.Profiles, nil
}

func (f *FakeClient) GetSpeakerProfile(_ context.Context, id string) (notoapi.SpeakerProfile, error) {
	for _, p := range f.Profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "profile not found", nil)
}

func (f *FakeClient) CreateSpeakerProfile(_ context.Context, req notoapi.CreateSpeakerProfileRequest) (notoapi.SpeakerProfile, error) {
	f.CreatedProfiles = append(f.CreatedProfiles, req)
	p := notoapi.SpeakerProfile{ID: "new-" + req.DisplayName, DisplayName: req.DisplayName, Notes: req.Notes, Affiliations: req.Affiliations}
	f.Profiles = append(f.Profiles, p)
	return p, nil
}

func (f *FakeClient) PatchSpeakerProfile(_ context.Context, id string, patch notoapi.SpeakerProfilePatch) (notoapi.SpeakerProfile, error) {
	for i := range f.Profiles {
		if f.Profiles[i].ID != id {
			continue
		}
		if patch.DisplayName != nil {
			f.Profiles[i].DisplayName = *patch.DisplayName
		}
		if patch.Pronouns != nil {
			f.Profiles[i].Pronouns = *patch.Pronouns
		}
		if patch.Notes != nil {
			f.Profiles[i].Notes = *patch.Notes
		}
		if patch.Affiliations != nil {
			f.Profiles[i].Affiliations = *patch.Affiliations
		}
		return f.Profiles[i], nil
	}
	return notoapi.SpeakerProfile{}, notoapi.NewError(notoapi.CodeNotFound, "profile not found", nil)
}

func (f *FakeClient) DeleteSpeakerProfile(_ context.Context, id string) error {
	out := f.Profiles[:0]
	for _, p := range f.Profiles {
		if p.ID != id {
			out = append(out, p)
		}
	}
	f.Profiles = out
	return nil
}

func (f *FakeClient) MergeSpeakerProfiles(_ context.Context, targetID, sourceID string) (notoapi.SpeakerProfile, error) {
	var target notoapi.SpeakerProfile
	out := f.Profiles[:0]
	for _, p := range f.Profiles {
		if p.ID == sourceID {
			continue
		}
		if p.ID == targetID {
			target = p
		}
		out = append(out, p)
	}
	f.Profiles = out
	return target, nil
}

func (f *FakeClient) GetMeetingSpeakerMappings(_ context.Context, meetingID string) (notoapi.MeetingSpeakerMappings, error) {
	if f.Mappings == nil {
		return notoapi.MeetingSpeakerMappings{MeetingID: meetingID}, nil
	}
	return f.Mappings[meetingID], nil
}

func (f *FakeClient) PatchMeetingSpeakerMappings(_ context.Context, meetingID string, patch notoapi.MeetingSpeakerMappingsPatch) (notoapi.MeetingSpeakerMappings, error) {
	f.PatchedMappings = append(f.PatchedMappings, patchedMapping{MeetingID: meetingID, Patch: patch})
	if f.Mappings == nil {
		return notoapi.MeetingSpeakerMappings{MeetingID: meetingID}, nil
	}
	return f.Mappings[meetingID], nil
}

func (f *FakeClient) UpdateSpeakerName(_ context.Context, meetingID, speakerID, displayName string) error {
	f.RenamedSpeakers = append(f.RenamedSpeakers, renamedSpeaker{meetingID, speakerID, displayName})
	return nil
}

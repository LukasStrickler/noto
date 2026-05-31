package testutil

import (
	"context"

	"github.com/lukasstrickler/noto/internal/notoapi"
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

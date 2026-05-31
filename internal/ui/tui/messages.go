package tui

import (
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// All messages the root model can receive from background commands.
// Kept in one place so screens can match on them without circular imports.

type meetingsLoadedMsg struct {
	Result notoapi.ListMeetingsResult
	Err    error
}

type meetingLoadedMsg struct {
	Meeting notoapi.Meeting
	Err     error
}

type transcriptLoadedMsg struct {
	Transcript notoapi.Transcript
	Err        error
}

type summaryLoadedMsg struct {
	Summary notoapi.Summary
	Err     error
}

type filesLoadedMsg struct {
	Files notoapi.MeetingFiles
	Err   error
}

type agentLoadedMsg struct {
	Agent notoapi.AgentHandoff
	Err   error
}

type searchResultMsg struct {
	Query  string
	Result notoapi.SearchResult
	Err    error
}

type providersLoadedMsg struct {
	Providers []notoapi.ProviderInfo
	Err       error
}

type recordingStartedMsg struct {
	Res notoapi.StartRecordingResult
	Err error
}

type recordingStoppedMsg struct {
	Res notoapi.StopRecordingResult
	Err error
}

type recordingStateMsg struct {
	State notoapi.RecordingState
	Err   error
}

type jobsLoadedMsg struct {
	Jobs []notoapi.Job
	Err  error
}

type storageLoadedMsg struct {
	Storage notoapi.Storage
	Err     error
}

type configLoadedMsg struct {
	Cfg notoapi.Config
	Err error
}

type providerKeyResultMsg struct {
	ProviderID string
	Err        error
}

type providerTestMsg struct {
	ProviderID string
	Result     notoapi.TestProviderResult
	Err        error
}

// eventStreamMsg arrives once per server event (job/recorder/meter/etc).
type eventStreamMsg struct {
	Event  notoapi.Event
	Closed bool
}

// bannerMsg is published by screens to show a transient status line.
type bannerMsg struct {
	Kind string // "info" | "warn" | "error"
	Text string
}

// switchScreenMsg requests the root model to swap the active screen.
type switchScreenMsg struct {
	ID    screenID
	Param string // optional opaque parameter (e.g. meeting id)
}

// pushScreenMsg pushes a new screen on the stack.
type pushScreenMsg struct {
	ID    screenID
	Param string
}

// popScreenMsg pops the top screen.
type popScreenMsg struct{}

// quitMsg requests a clean shutdown.
type quitMsg struct{}

// commandMsg is what the palette emits when the user picks an entry.
type commandMsg struct {
	Action string
	Param  string
}

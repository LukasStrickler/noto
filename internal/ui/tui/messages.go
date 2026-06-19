package tui

import (
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// All messages the root model can receive from background commands.
// Kept in one place so screens can match on them without circular imports.

// mouseWheelMsg is the root's translation of a raw scroll-wheel event into a
// screen-facing intent. The root resolves which clickable region sits under the
// pointer (the same hit map clicks use) and tags the event with it, so a screen
// scrolls whatever the cursor is over — the pane vs the list — WITHOUT first
// being focused (hover-to-scroll, like a native app). over is "" when the wheel
// is over empty space; up is the scroll direction.
type mouseWheelMsg struct {
	over string
	up   bool
}

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

type systemLoadedMsg struct {
	System notoapi.System
	Err    error
}

// topoTickMsg advances the deployment-diagram animation (border pulse + flowing
// packets). It only fires while the Config screen is active; see configScreen.
type topoTickMsg struct{}

type providerKeyResultMsg struct {
	ProviderID string
	Err        error
}

type providerTestMsg struct {
	ProviderID string
	Result     notoapi.TestProviderResult
	Err        error
}

// --- People (speaker profiles) ---

type profilesLoadedMsg struct {
	Profiles []notoapi.SpeakerProfile
	Err      error
}

// speakerMappingsLoadedMsg carries a meeting's resolved speaker→person links
// plus ranked "who is this?" candidates for the unresolved ones.
type speakerMappingsLoadedMsg struct {
	MeetingID string
	Mappings  []notoapi.MeetingSpeakerMapping
	Err       error
}

// speakerIdentityMsg reports the result of an assign / new-person / rename
// action on the Speakers tab so the pane can refresh names + banner.
type speakerIdentityMsg struct {
	MeetingID string
	Note      string
	Err       error
}

// speakerPersonOpenMsg reports the profile a Speakers-tab "open person" jump
// resolved to (creating one from the voiceprint first when the speaker was
// unresolved), so the pane can navigate to the People screen for it. Edit ⇒
// open that person's edit form on arrival (the just-created, needs-a-name case).
type speakerPersonOpenMsg struct {
	MeetingID string
	ProfileID string
	Edit      bool
	Err       error
}

type profileLoadedMsg struct {
	Profile notoapi.SpeakerProfile
	Err     error
}

// profileSavedMsg reports the result of a create/patch/delete/merge so the
// People screen can refresh its list and surface a banner.
type profileSavedMsg struct {
	Action string // "saved" | "deleted" | "merged"
	Name   string
	Err    error
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

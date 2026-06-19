package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// --- async fetch commands. Each returns a tea.Cmd that talks to the
//     notoapi.Client in a goroutine and wraps the result in a typed msg.

func fetchMeetings(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		res, err := ctx.client.ListMeetings(c, notoapi.ListMeetingsOpts{Limit: 100})
		return meetingsLoadedMsg{Result: res, Err: err}
	}
}

func fetchMeeting(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		m, err := ctx.client.GetMeeting(c, id)
		return meetingLoadedMsg{Meeting: m, Err: err}
	}
}

func fetchTranscript(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		t, err := ctx.client.GetTranscript(c, id)
		return transcriptLoadedMsg{Transcript: t, Err: err}
	}
}

func fetchSummary(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		s, err := ctx.client.GetSummary(c, id)
		return summaryLoadedMsg{Summary: s, Err: err}
	}
}

func fetchFiles(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		f, err := ctx.client.GetMeetingFiles(c, id)
		return filesLoadedMsg{Files: f, Err: err}
	}
}

func fetchAgent(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		a, err := ctx.client.GetAgentHandoff(c, id)
		return agentLoadedMsg{Agent: a, Err: err}
	}
}

func fetchProviders(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		list, err := ctx.client.ListProviders(c)
		return providersLoadedMsg{Providers: list, Err: err}
	}
}

func fetchJobs(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		jobs, err := ctx.client.ListJobs(c, notoapi.ListJobsOpts{Limit: 20})
		return jobsLoadedMsg{Jobs: jobs, Err: err}
	}
}

func fetchRecording(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		st, err := ctx.client.GetRecording(c)
		return recordingStateMsg{State: st, Err: err}
	}
}

func fetchStorage(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		st, err := ctx.client.GetStorage(c)
		return storageLoadedMsg{Storage: st, Err: err}
	}
}

func fetchConfig(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		cfg, err := ctx.client.GetConfig(c)
		return configLoadedMsg{Cfg: cfg, Err: err}
	}
}

func fetchSystem(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		sys, err := ctx.client.GetSystem(c)
		return systemLoadedMsg{System: sys, Err: err}
	}
}

// topoTickInterval paces the deployment-diagram animation: slow enough to be
// calm and cheap (~5.5 frames/s), fast enough to read as motion. The packets
// drift rather than dart — a hectic tick reads as flicker, not flow.
const topoTickInterval = 180 * time.Millisecond

func topoTickCmd() tea.Cmd {
	return tea.Tick(topoTickInterval, func(time.Time) tea.Msg { return topoTickMsg{} })
}

func runSearch(ctx screenCtx, q string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		res, err := ctx.client.Search(c, notoapi.SearchOpts{Query: q, Limit: 100})
		return searchResultMsg{Query: q, Result: res, Err: err}
	}
}

func startRecordingCmd(ctx screenCtx, opts notoapi.StartRecordingOpts) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		res, err := ctx.client.StartRecording(c, opts)
		return recordingStartedMsg{Res: res, Err: err}
	}
}

func stopRecordingCmd(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		res, err := ctx.client.StopRecording(c, notoapi.StopRecordingOpts{})
		return recordingStoppedMsg{Res: res, Err: err}
	}
}

func markerCmd(ctx screenCtx, label string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		err := ctx.client.AddMarker(c, label)
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "marker added"}
	}
}

func setProviderKeyCmd(ctx screenCtx, id, value string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		err := ctx.client.SetProviderKey(c, id, value)
		return providerKeyResultMsg{ProviderID: id, Err: err}
	}
}

func testProviderCmd(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		res, err := ctx.client.TestProvider(c, id)
		return providerTestMsg{ProviderID: id, Result: res, Err: err}
	}
}

// --- People (speaker profiles) ---

func fetchProfiles(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		list, err := ctx.client.ListSpeakerProfiles(c)
		return profilesLoadedMsg{Profiles: list, Err: err}
	}
}

// fetchProfile pulls one profile in full (incl. its meetings list, which the
// list endpoint omits).
func fetchProfile(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		p, err := ctx.client.GetSpeakerProfile(c, id)
		return profileLoadedMsg{Profile: p, Err: err}
	}
}

func patchProfileCmd(ctx screenCtx, id string, patch notoapi.SpeakerProfilePatch) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		p, err := ctx.client.PatchSpeakerProfile(c, id, patch)
		return profileSavedMsg{Action: "saved", Name: p.DisplayName, Err: err}
	}
}

func deleteProfileCmd(ctx screenCtx, id, name string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		err := ctx.client.DeleteSpeakerProfile(c, id)
		return profileSavedMsg{Action: "deleted", Name: name, Err: err}
	}
}

// mergeProfilesCmd folds source into target (target keeps its identity), then
// reports the surviving name.
func mergeProfilesCmd(ctx screenCtx, targetID, sourceID, targetName string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		p, err := ctx.client.MergeSpeakerProfiles(c, targetID, sourceID)
		name := targetName
		if err == nil {
			name = p.DisplayName
		}
		return profileSavedMsg{Action: "merged", Name: name, Err: err}
	}
}

// --- rendering helpers ---

// fit truncates s to width, padding short ones to width with spaces.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(s) > width {
		if width < 2 {
			return s[:width]
		}
		return s[:width-1] + "…"
	}
	return s + strings.Repeat(" ", width-len(s))
}

// renderMeter draws a labeled dBFS bar. value is in [-60, 0].
func renderMeter(s theme.Styles, label string, value int) string {
	const width = 22
	// Map -60..0 to 0..width.
	if value < -60 {
		value = -60
	}
	if value > 0 {
		value = 0
	}
	filled := width + value*width/60 // value is negative
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("·", width-filled)
	// Classic level-meter ramp: green (safe) → amber (hot) → red (clipping).
	style := s.MeterFilled
	if value >= -6 {
		style = s.MeterClip
	} else if value >= -18 {
		style = s.Warning
	}
	return s.Muted.Render(label) + style.Render(bar) + " " + s.Muted.Render(fmt.Sprintf("%+3d dB", value))
}

// ternary returns a when cond is true, otherwise b. Cleaner than an
// inline if/else for one-liners.
func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// oscLink wraps label in an OSC 8 terminal hyperlink to url. Terminals
// that don't understand OSC 8 ignore the escapes and show label verbatim,
// so this is safe to emit unconditionally (and ansi.Strip removes it).
func oscLink(url, label string) string {
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// fileLink renders a local filesystem path as a clickable file:// link,
// so agents and humans can open artifacts straight from the handoff view.
func fileLink(path string) string {
	if path == "" {
		return ""
	}
	return oscLink("file://"+path, path)
}

// --- badge helpers used by screens ---

func badgeWarn(stylesAny any, text string) string   { return styleAny(stylesAny, "warn", text) }
func badgeDanger(stylesAny any, text string) string { return styleAny(stylesAny, "danger", text) }

// jobBar renders a horizontal progress bar (0.0..1.0) with the given
// total width using ▰ / ▱ glyphs.
func jobBar(progress float64, width int) string {
	filled := int(progress * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("▰", filled) + strings.Repeat("▱", width-filled)
}

// jobKindStyled returns a fixed-width label for a job kind so columns
// stay aligned when many jobs render in a list.
func jobKindStyled(_ any, kind notoapi.JobKind) string {
	return fmt.Sprintf("%-10s", kind)
}

// formatSec turns a fractional second value into a timestamp label for
// transcript segment headers. It defers to formatDuration so a clip longer
// than an hour reads as h:mm:ss rather than an overflowing minute count.
func formatSec(s float64) string {
	return formatDuration(int(s))
}

func styleAny(stylesAny any, kind, text string) string {
	st, ok := stylesAny.(theme.Styles)
	if !ok {
		return text
	}
	switch kind {
	case "ok":
		return st.BadgeOK.Render(text)
	case "warn":
		return st.BadgeWarn.Render(text)
	case "danger":
		return st.BadgeDanger.Render(text)
	case "info":
		return st.BadgeInfo.Render(text)
	default:
		return st.Muted.Render(text)
	}
}

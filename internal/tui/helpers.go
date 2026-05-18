package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/theme"
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

func verifyStorageCmd(ctx screenCtx) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		_, err := ctx.client.VerifyStorage(c)
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "verify job queued"}
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
	style := s.MeterFilled
	if value >= -6 {
		style = s.MeterClip
	} else if value >= -18 {
		style = s.Action
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

// --- empty state helper -----------------------------------------

type emptyAction struct {
	Key   string
	Label string
}

func renderEmptyState(s theme.Styles, headline string, actions []emptyAction) string {
	rows := []string{
		s.HeaderEm.Render("◌ " + headline),
		"",
	}
	for _, a := range actions {
		rows = append(rows, "  "+s.ChipKey.Render(a.Key)+"   "+s.Muted.Render(a.Label))
	}
	if len(actions) == 0 {
		rows = append(rows, s.Muted.Render("Nothing to show yet."))
	}
	return strings.Join(rows, "\n")
}

// --- badge helpers used by screens ---

func badgeOK(stylesAny any, text string) string     { return styleAny(stylesAny, "ok", text) }
func badgeWarn(stylesAny any, text string) string   { return styleAny(stylesAny, "warn", text) }
func badgeDanger(stylesAny any, text string) string { return styleAny(stylesAny, "danger", text) }
func badgeInfo(stylesAny any, text string) string   { return styleAny(stylesAny, "info", text) }
func badgeMuted(stylesAny any, text string) string  { return styleAny(stylesAny, "muted", text) }

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

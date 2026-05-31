package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/layout"
)

// agentScreen renders the per-meeting "agent handoff" view: paths,
// copyable CLI commands, ready to paste into an LLM workflow.
type agentScreen struct {
	id_     string
	a       notoapi.AgentHandoff
	loading bool
	err     error
}

func newAgentScreen() screen { return &agentScreen{loading: true} }

func (a *agentScreen) id() screenID      { return sAgent }
func (a *agentScreen) title() string     { return "agent" }
func (a *agentScreen) inputActive() bool { return false }
func (a *agentScreen) meetingID() string { return a.id_ }

func (a *agentScreen) enter(ctx screenCtx, param string) tea.Cmd {
	a.id_ = param
	return fetchAgent(ctx, param)
}
func (a *agentScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (a *agentScreen) update(_ screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case agentLoadedMsg:
		a.loading = false
		a.err = v.Err
		a.a = v.Agent
	}
	return a, nil
}

func (a *agentScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if a.loading {
		return s.Muted.Render("loading agent handoff…")
	}
	if a.err != nil {
		return s.BadgeDanger.Render(a.err.Error())
	}
	lines := []string{
		s.HeaderEm.Render("Agent handoff"),
		"",
		fmt.Sprintf("  meeting_id  %s", a.a.MeetingID),
		fmt.Sprintf("  version_id  %s", a.a.VersionID),
		"",
		s.PanelTitle.Render("files"),
		"  manifest   " + nilIfEmpty(s, a.a.Files.Manifest),
		"  transcript " + nilIfEmpty(s, a.a.Files.Transcript),
		"  summary    " + nilIfEmpty(s, a.a.Files.SummaryJSON),
		"  audio      " + nilIfEmpty(s, a.a.Files.Audio),
		"",
		s.PanelTitle.Render("commands"),
	}
	for _, c := range a.a.Commands {
		lines = append(lines, "  $ "+s.Secondary.Render(c))
	}
	body := strings.Join(lines, "\n")
	return layout.Panel{Title: "agent handoff", Width: ctx.width, Height: ctx.height, Focused: true, Body: body}.Render(s)
}

func nilIfEmpty(s any, v string) string {
	if v == "" {
		return styleAny(s, "muted", "(not yet generated)")
	}
	// Make the artifact path a clickable file:// link for terminals that
	// support OSC 8 — the path text still renders everywhere else.
	return fileLink(v)
}

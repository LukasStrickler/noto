package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
)

type detailScreen struct {
	id_     string
	meeting notoapi.Meeting
	summary notoapi.Summary
	files   notoapi.MeetingFiles
	err     error
	loading bool
	tab     int // 0 summary, 1 actions, 2 decisions, 3 risks, 4 questions
}

func newDetailScreen() screen { return &detailScreen{loading: true} }

func (d *detailScreen) id() screenID    { return sDetail }
func (d *detailScreen) title() string   { return "meeting" }
func (d *detailScreen) helpKeys() []keys.Map { return nil }
func (d *detailScreen) inputActive() bool    { return false }
func (d *detailScreen) meetingID() string    { return d.id_ }

func (d *detailScreen) enter(ctx screenCtx, param string) tea.Cmd {
	d.id_ = param
	return tea.Batch(
		fetchMeeting(ctx, param),
		fetchSummary(ctx, param),
		fetchFiles(ctx, param),
	)
}
func (d *detailScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (d *detailScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case meetingLoadedMsg:
		d.loading = false
		d.err = v.Err
		d.meeting = v.Meeting
	case summaryLoadedMsg:
		if v.Err == nil {
			d.summary = v.Summary
		}
	case filesLoadedMsg:
		if v.Err == nil {
			d.files = v.Files
		}
	case tea.KeyMsg:
		switch {
		case key.Matches(v, ctx.keys.Right):
			if d.tab < 4 {
				d.tab++
			}
		case key.Matches(v, ctx.keys.Left):
			if d.tab > 0 {
				d.tab--
			}
		case v.String() == "t":
			return d, push(sTranscript, d.id_)
		case v.String() == "k":
			return d, push(sSpeakers, d.id_)
		case key.Matches(v, ctx.keys.OpenAgent):
			return d, push(sAgent, d.id_)
		case key.Matches(v, ctx.keys.Verify):
			return d, verifyMeetingCmd(ctx, d.id_)
		case key.Matches(v, ctx.keys.Delete):
			return d, deleteMeetingCmd(ctx, d.id_)
		}
	}
	return d, nil
}

func (d *detailScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if d.loading {
		return s.Muted.Render("loading…")
	}
	if d.err != nil {
		return s.BadgeDanger.Render(d.err.Error())
	}
	tabsNames := []string{"summary", "actions", "decisions", "risks", "questions"}
	var tabs []string
	for i, n := range tabsNames {
		if i == d.tab {
			tabs = append(tabs, s.HeaderEm.Render(n))
		} else {
			tabs = append(tabs, s.Muted.Render(n))
		}
	}
	tabsRow := strings.Join(tabs, "  ·  ")
	meta := fmt.Sprintf("%s   %dm   %d speakers   D %d   A %d   R %d",
		d.meeting.CreatedAt.Format("Jan 02 15:04"),
		d.meeting.DurationSeconds/60,
		d.meeting.Speakers, d.meeting.DecisionCount, d.meeting.ActionCount, d.meeting.RiskCount)
	header := lipgloss.JoinVertical(lipgloss.Left,
		s.HeaderEm.Render(d.meeting.Title),
		s.Muted.Render(meta),
		"",
		tabsRow,
		"",
	)
	body := d.renderTab(ctx)
	chips := strings.Join([]string{
		hint(s, "t", "transcript"),
		hint(s, "k", "speakers"),
		hint(s, "a", "agent"),
		hint(s, "v", "verify"),
		hint(s, "d", "delete"),
		hint(s, "←/→", "tab"),
	}, "   ")
	return layout.Panel{
		Title: d.meeting.Title,
		Subtitle: d.meeting.ID,
		Width: ctx.width, Height: ctx.height, Focused: true,
		Body: header + body + "\n\n" + s.HintBar.Render(chips),
	}.Render(s)
}

func (d *detailScreen) renderTab(ctx screenCtx) string {
	s := ctx.styles
	switch d.tab {
	case 0:
		if d.summary.Markdown != "" {
			return d.summary.Markdown
		}
		if d.summary.ShortSummary != "" {
			return d.summary.ShortSummary
		}
		return s.Muted.Render("No summary yet. Run `noto summarize` or kick a pipeline job.")
	case 1:
		return renderItems(s, "Action items", actionToItems(d.summary.ActionItems))
	case 2:
		return renderItems(s, "Decisions", d.summary.Decisions)
	case 3:
		return renderItems(s, "Risks", d.summary.Risks)
	case 4:
		return renderItems(s, "Open questions", d.summary.OpenQuestions)
	}
	return ""
}

func renderItems(s any, title string, items []notoapi.SummaryItem) string {
	if len(items) == 0 {
		return styleAny(s, "muted", "No "+title+" yet.")
	}
	var rows []string
	for i, it := range items {
		row := fmt.Sprintf("  %d. %s", i+1, it.Text)
		if len(it.SegmentRefs) > 0 {
			row += "  " + styleAny(s, "info", "["+strings.Join(it.SegmentRefs, ",")+"]")
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func actionToItems(actions []notoapi.ActionItem) []notoapi.SummaryItem {
	out := make([]notoapi.SummaryItem, 0, len(actions))
	for _, a := range actions {
		text := a.Text
		if a.Owner != "" {
			text += "  (owner: " + a.Owner + ")"
		}
		out = append(out, notoapi.SummaryItem{Text: text, SegmentRefs: a.SegmentRefs})
	}
	return out
}

func verifyMeetingCmd(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		if _, err := ctx.client.VerifyMeeting(ctx.ctx, id); err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "verify job queued"}
	}
}

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

type dashboardScreen struct {
	loading bool
	meetings []notoapi.Meeting
	jobs     []notoapi.Job
	err      error
	cursor   int
	rec      notoapi.RecordingState
}

func newDashboardScreen() screen {
	return &dashboardScreen{loading: true}
}

func (d *dashboardScreen) id() screenID { return sDashboard }
func (d *dashboardScreen) title() string { return "dashboard" }
func (d *dashboardScreen) helpKeys() []keys.Map { return nil }
func (d *dashboardScreen) inputActive() bool    { return false }
func (d *dashboardScreen) meetingID() string {
	if d.cursor < len(d.meetings) {
		return d.meetings[d.cursor].ID
	}
	return ""
}

func (d *dashboardScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	return tea.Batch(
		fetchMeetings(ctx),
		fetchJobs(ctx),
		fetchRecording(ctx),
	)
}

func (d *dashboardScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (d *dashboardScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case meetingsLoadedMsg:
		d.loading = false
		d.err = m.Err
		d.meetings = m.Result.Meetings
		if d.cursor >= len(d.meetings) {
			d.cursor = 0
		}
		return d, nil
	case jobsLoadedMsg:
		d.jobs = m.Jobs
		return d, nil
	case recordingStateMsg:
		d.rec = m.State
		return d, nil
	case eventStreamMsg:
		// React to job/recorder updates: refresh meetings on terminal job state.
		if m.Event.Kind == notoapi.EventJob && m.Event.Job != nil {
			switch m.Event.Job.Status {
			case notoapi.JobSucceeded, notoapi.JobFailed, notoapi.JobCanceled, notoapi.JobInterrupted:
				return d, tea.Batch(fetchMeetings(ctx), fetchJobs(ctx))
			}
			return d, fetchJobs(ctx)
		}
		if m.Event.Kind == notoapi.EventRecorder && m.Event.Recorder != nil {
			d.rec = *m.Event.Recorder
		}
		return d, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(m, ctx.keys.Up):
			if d.cursor > 0 {
				d.cursor--
			}
		case key.Matches(m, ctx.keys.Down):
			if d.cursor < len(d.meetings)-1 {
				d.cursor++
			}
		case key.Matches(m, ctx.keys.Enter):
			if d.cursor < len(d.meetings) {
				return d, push(sDetail, d.meetings[d.cursor].ID)
			}
		case key.Matches(m, ctx.keys.Refresh):
			return d, tea.Batch(fetchMeetings(ctx), fetchJobs(ctx), fetchRecording(ctx))
		}
	}
	return d, nil
}

func (d *dashboardScreen) view(ctx screenCtx) string {
	s := ctx.styles
	bp := layout.BreakpointFor(ctx.width)
	height := ctx.height

	left := d.renderMeetings(ctx)
	rightTop := d.renderRecording(ctx)
	rightBottom := d.renderJobs(ctx)

	switch bp {
	case layout.Narrow:
		// Just the list, with a slim recorder strip up top.
		strip := layout.Panel{
			Title:  "recording",
			Width:  ctx.width,
			Height: 4,
			Body:   d.renderRecordingBody(ctx, ctx.width-4),
		}.Render(s)
		main := layout.Panel{
			Title:    "meetings",
			Subtitle: fmt.Sprintf("%d total", len(d.meetings)),
			Width:    ctx.width,
			Height:   height - 4,
			Focused:  true,
			Body:     d.renderMeetingRows(ctx, ctx.width-6),
		}.Render(s)
		return lipgloss.JoinVertical(lipgloss.Left, strip, main)
	case layout.Regular:
		leftW := ctx.width * 6 / 10
		rightW := ctx.width - leftW - 1
		topH := height / 2
		bottomH := height - topH
		mPanel := layout.Panel{Title: "meetings", Subtitle: fmt.Sprintf("%d", len(d.meetings)), Width: leftW, Height: height, Focused: true, Body: left}.Render(s)
		rTop := layout.Panel{Title: "recording", Width: rightW, Height: topH, Body: rightTop}.Render(s)
		rBot := layout.Panel{Title: "jobs", Width: rightW, Height: bottomH, Body: rightBottom}.Render(s)
		right := lipgloss.JoinVertical(lipgloss.Left, rTop, rBot)
		return lipgloss.JoinHorizontal(lipgloss.Top, mPanel, " ", right)
	}
	// Wide: three columns.
	leftW := ctx.width * 5 / 12
	midW := ctx.width * 4 / 12
	rightW := ctx.width - leftW - midW - 2
	mPanel := layout.Panel{Title: "meetings", Subtitle: fmt.Sprintf("%d total", len(d.meetings)), Width: leftW, Height: height, Focused: true, Body: left}.Render(s)
	rTop := layout.Panel{Title: "recording", Width: midW, Height: height, Body: rightTop}.Render(s)
	rBot := layout.Panel{Title: "jobs", Width: rightW, Height: height, Body: rightBottom}.Render(s)
	return lipgloss.JoinHorizontal(lipgloss.Top, mPanel, " ", rTop, " ", rBot)
}

func (d *dashboardScreen) renderMeetings(ctx screenCtx) string {
	return d.renderMeetingRows(ctx, ctx.width*5/12-4)
}

func (d *dashboardScreen) renderMeetingRows(ctx screenCtx, width int) string {
	s := ctx.styles
	if d.loading {
		return s.Muted.Render("loading meetings…")
	}
	if d.err != nil {
		return s.Danger.Render("✗ " + d.err.Error())
	}
	if len(d.meetings) == 0 {
		return renderEmptyState(s, "No meetings yet", []emptyAction{
			{Key: "r", Label: "record a meeting"},
			{Key: "i", Label: "import audio (CLI: noto import-audio path.m4a)"},
			{Key: ":", Label: "open the command menu"},
		})
	}
	titleW := max(10, width-30)
	var out []string
	visible := ctx.height - 4
	if visible < 1 {
		visible = 1
	}
	for i, m := range d.meetings {
		if i >= visible {
			break
		}
		when := m.CreatedAt.Format("Jan 02")
		dur := fmt.Sprintf("%3dm", m.DurationSeconds/60)
		row := fmt.Sprintf("%s  %s  %s  %s",
			fit(m.Title, titleW),
			s.Muted.Render(when),
			s.Muted.Render(dur),
			statusBadge(s, m.Status),
		)
		if i == d.cursor {
			row = s.RowSelected.Render(" ▸ " + fit(m.Title, titleW+8))
		} else {
			row = "   " + row
		}
		out = append(out, row)
	}
	return strings.Join(out, "\n")
}

func (d *dashboardScreen) renderRecording(ctx screenCtx) string {
	return d.renderRecordingBody(ctx, ctx.width/3-4)
}

func (d *dashboardScreen) renderRecordingBody(ctx screenCtx, _ int) string {
	s := ctx.styles
	if !d.rec.Active {
		return strings.Join([]string{
			s.Muted.Render("○ idle"),
			"",
			s.Muted.Render("Press ") + s.ChipKey.Render("r") + s.Muted.Render(" to record."),
		}, "\n")
	}
	lines := []string{
		s.Recording.Render(fmt.Sprintf("● REC  %s", formatDuration(d.rec.ElapsedSec))),
		s.HeaderEm.Render(def(d.rec.Title, "Untitled meeting")),
		"",
		renderMeter(s, "me/mic ", d.rec.MicDB),
		renderMeter(s, "system ", d.rec.ParticipantDB),
	}
	return strings.Join(lines, "\n")
}

func (d *dashboardScreen) renderJobs(ctx screenCtx) string {
	s := ctx.styles
	if len(d.jobs) == 0 {
		return s.Muted.Render("No jobs running.\n\nJobs appear here when\nyou record or import audio.")
	}
	var out []string
	for i, j := range d.jobs {
		if i >= 8 {
			break
		}
		bar := jobBar(j.Progress, 12)
		var status string
		switch j.Status {
		case notoapi.JobRunning:
			status = s.Info.Render("running")
		case notoapi.JobSucceeded:
			status = s.Success.Render("done")
		case notoapi.JobFailed:
			status = s.Danger.Render("failed")
		case notoapi.JobInterrupted:
			status = s.Warning.Render("interrupted")
		default:
			status = s.Muted.Render(string(j.Status))
		}
		line := fmt.Sprintf("%s  %s  %s", jobKindStyled(s, j.Kind), bar, status)
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

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

func jobKindStyled(s any, kind notoapi.JobKind) string {
	_ = s
	return fmt.Sprintf("%-10s", kind)
}

func statusBadge(s any, status notoapi.MeetingStatus) string {
	switch status {
	case notoapi.StatusSummarized:
		return badgeOK(s, "✓ done")
	case notoapi.StatusTranscribed:
		return badgeInfo(s, "transcribed")
	case notoapi.StatusRecording:
		return badgeDanger(s, "● rec")
	case notoapi.StatusFailed:
		return badgeDanger(s, "✗ failed")
	default:
		return badgeMuted(s, string(status))
	}
}

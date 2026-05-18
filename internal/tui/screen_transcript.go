package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
)

type transcriptScreen struct {
	id_     string
	tr      notoapi.Transcript
	err     error
	loading bool
	vp      viewport.Model
	ready   bool
}

func newTranscriptScreen() screen {
	return &transcriptScreen{loading: true}
}

func (t *transcriptScreen) id() screenID    { return sTranscript }
func (t *transcriptScreen) title() string   { return "transcript" }
func (t *transcriptScreen) helpKeys() []keys.Map { return nil }
func (t *transcriptScreen) inputActive() bool    { return false }
func (t *transcriptScreen) meetingID() string    { return t.id_ }

func (t *transcriptScreen) enter(ctx screenCtx, param string) tea.Cmd {
	t.id_ = param
	return fetchTranscript(ctx, param)
}
func (t *transcriptScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (t *transcriptScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case transcriptLoadedMsg:
		t.loading = false
		t.err = v.Err
		t.tr = v.Transcript
		t.vp = viewport.New(ctx.width-4, ctx.height-4)
		t.vp.SetContent(t.renderBody(ctx))
		t.ready = true
		return t, nil
	case tea.KeyMsg:
		if t.ready {
			switch {
			case key.Matches(v, ctx.keys.CopyCitation):
				return t, copyToClipboard(t.tr.MeetingID + "  segment selection not yet wired")
			}
			var cmd tea.Cmd
			t.vp, cmd = t.vp.Update(msg)
			return t, cmd
		}
	}
	return t, nil
}

func (t *transcriptScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if t.loading {
		return s.Muted.Render("loading transcript…")
	}
	if t.err != nil {
		return s.BadgeDanger.Render(t.err.Error())
	}
	if !t.ready || len(t.tr.Segments) == 0 {
		return layout.Panel{
			Title: "transcript", Subtitle: t.id_, Width: ctx.width, Height: ctx.height, Focused: true,
			Body: s.Muted.Render("No transcript yet."),
		}.Render(s)
	}
	t.vp.Width = ctx.width - 4
	t.vp.Height = ctx.height - 4
	t.vp.SetContent(t.renderBody(ctx))
	return layout.Panel{
		Title: "transcript", Subtitle: fmt.Sprintf("%d segments", len(t.tr.Segments)),
		Width: ctx.width, Height: ctx.height, Focused: true, Body: t.vp.View(),
	}.Render(s)
}

func (t *transcriptScreen) renderBody(ctx screenCtx) string {
	s := ctx.styles
	var rows []string
	speakerStyle := map[string]int{}
	next := 0
	for _, seg := range t.tr.Segments {
		idx, ok := speakerStyle[seg.SpeakerID]
		if !ok {
			idx = next % 3
			speakerStyle[seg.SpeakerID] = idx
			next++
		}
		spStyle := s.SpeakerA
		switch idx {
		case 1:
			spStyle = s.SpeakerB
		case 2:
			spStyle = s.SpeakerC
		}
		ts := s.Muted.Render(fmt.Sprintf("[%s]", formatSec(seg.StartSec)))
		role := s.Muted.Render(fmt.Sprintf("[%s]", seg.Role))
		header := strings.Join([]string{ts, spStyle.Render(seg.Speaker), role, s.Citation.Render(seg.ID)}, " ")
		body := "  " + seg.Text
		rows = append(rows, header, body, "")
	}
	return strings.Join(rows, "\n")
}

func formatSec(s float64) string {
	total := int(s)
	mn := total / 60
	sc := total % 60
	return fmt.Sprintf("%02d:%02d", mn, sc)
}

func copyToClipboard(_ string) tea.Cmd {
	// V1: just emit a banner; OSC52 copy is a follow-up.
	return func() tea.Msg {
		return bannerMsg{Kind: "info", Text: "clipboard not wired yet — citation noted"}
	}
}

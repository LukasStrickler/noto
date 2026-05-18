package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
)

type searchScreen struct {
	input    textinput.Model
	hits     []notoapi.SearchHit
	cursor   int
	err      error
	lastQuery string
}

func newSearchScreen() screen {
	ti := textinput.New()
	ti.Placeholder = "search transcripts, decisions, action items…"
	ti.Focus()
	ti.CharLimit = 200
	return &searchScreen{input: ti}
}

func (s *searchScreen) id() screenID    { return sSearch }
func (s *searchScreen) title() string   { return "search" }
func (s *searchScreen) helpKeys() []keys.Map { return nil }
func (s *searchScreen) inputActive() bool    { return s.input.Focused() }
func (s *searchScreen) meetingID() string {
	if s.cursor < len(s.hits) {
		return s.hits[s.cursor].MeetingID
	}
	return ""
}

func (s *searchScreen) enter(_ screenCtx, _ string) tea.Cmd { return nil }
func (s *searchScreen) leave(_ screenCtx) tea.Cmd            { return nil }

func (s *searchScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case searchResultMsg:
		if v.Query == s.lastQuery {
			s.err = v.Err
			s.hits = v.Result.Hits
			if s.cursor >= len(s.hits) {
				s.cursor = 0
			}
		}
		return s, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(v, ctx.keys.Up):
			if s.cursor > 0 {
				s.cursor--
			}
			return s, nil
		case key.Matches(v, ctx.keys.Down):
			if s.cursor < len(s.hits)-1 {
				s.cursor++
			}
			return s, nil
		case key.Matches(v, ctx.keys.Enter):
			if s.cursor < len(s.hits) {
				return s, push(sDetail, s.hits[s.cursor].MeetingID)
			}
			return s, nil
		}
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(msg)
		newQ := strings.TrimSpace(s.input.Value())
		if newQ != s.lastQuery {
			s.lastQuery = newQ
			if newQ == "" {
				s.hits = nil
			} else {
				return s, tea.Batch(cmd, runSearch(ctx, newQ))
			}
		}
		return s, cmd
	}
	return s, nil
}

func (s *searchScreen) view(ctx screenCtx) string {
	st := ctx.styles
	bp := layout.BreakpointFor(ctx.width)
	header := st.ChipKey.Render("/") + " " + s.input.View()
	results := s.renderResults(ctx)
	preview := s.renderPreview(ctx)
	hints := strings.Join([]string{
		hint(st, "↑/↓", "navigate"),
		hint(st, "⏎", "open meeting"),
		hint(st, "esc", "back"),
	}, "   ")
	if bp != layout.Wide {
		body := strings.Join([]string{header, "", results, "", st.HintBar.Render(hints)}, "\n")
		return layout.Panel{
			Title: "search", Subtitle: fmt.Sprintf("%d hits", len(s.hits)),
			Width: ctx.width, Height: ctx.height, Focused: true, Body: body,
		}.Render(st)
	}
	leftW := ctx.width * 6 / 10
	rightW := ctx.width - leftW - 1
	left := layout.Panel{
		Title: "results", Subtitle: fmt.Sprintf("%d", len(s.hits)),
		Width: leftW, Height: ctx.height, Focused: true,
		Body: header + "\n\n" + results + "\n\n" + st.HintBar.Render(hints),
	}.Render(st)
	right := layout.Panel{
		Title: "evidence", Width: rightW, Height: ctx.height,
		Body: preview,
	}.Render(st)
	return left + " " + right
}

func (s *searchScreen) renderResults(ctx screenCtx) string {
	st := ctx.styles
	if s.err != nil {
		return st.Danger.Render("✗ " + s.err.Error())
	}
	if s.lastQuery == "" {
		return renderEmptyState(st, "Search transcripts, decisions, action items", []emptyAction{
			{Key: "type", Label: "search runs as you type"},
			{Key: "⏎", Label: "open the highlighted meeting"},
			{Key: "esc", Label: "back to dashboard"},
		})
	}
	if len(s.hits) == 0 {
		return st.Muted.Render(fmt.Sprintf("No matches for %q.", s.lastQuery))
	}
	titleW := max(16, ctx.width/5)
	snippetW := max(20, ctx.width-titleW-30)
	var rows []string
	for i, h := range s.hits {
		title := fit(h.MeetingTitle, titleW)
		ts := fmt.Sprintf("%5s", formatSec(h.Timestamp))
		speaker := fit(def(h.Speaker, "—"), 10)
		snippet := strings.ReplaceAll(strings.ReplaceAll(h.Snippet, "[", ""), "]", "")
		snippet = fit(snippet, snippetW)
		row := fmt.Sprintf("%s   %s   %s   %s", title, st.Muted.Render(ts), st.Secondary.Render(speaker), snippet)
		if i == s.cursor {
			row = st.RowSelected.Render(" ▸ " + row)
		} else {
			row = "   " + row
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func (s *searchScreen) renderPreview(ctx screenCtx) string {
	st := ctx.styles
	if s.cursor >= len(s.hits) {
		return st.Muted.Render("select a hit to preview")
	}
	h := s.hits[s.cursor]
	lines := []string{
		st.HeaderEm.Render(h.MeetingTitle),
		st.Muted.Render(h.MeetingID),
		"",
		st.Muted.Render(fmt.Sprintf("segment %s · %.1fs · %s", h.SegmentID, h.Timestamp, h.Speaker)),
		"",
		h.Snippet,
	}
	return strings.Join(lines, "\n")
}

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

type meetingsScreen struct {
	loading bool
	all     []notoapi.Meeting
	view_   []notoapi.Meeting
	cursor  int
	err     error
	filter  textinput.Model
	filterFocus bool
}

func newMeetingsScreen() screen {
	ti := textinput.New()
	ti.Placeholder = "filter…"
	ti.CharLimit = 100
	return &meetingsScreen{loading: true, filter: ti}
}

func (m *meetingsScreen) id() screenID    { return sMeetings }
func (m *meetingsScreen) title() string   { return "meetings" }
func (m *meetingsScreen) helpKeys() []keys.Map { return nil }
func (m *meetingsScreen) inputActive() bool    { return m.filterFocus }
func (m *meetingsScreen) meetingID() string {
	if m.cursor < len(m.view_) {
		return m.view_[m.cursor].ID
	}
	return ""
}

func (m *meetingsScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	return fetchMeetings(ctx)
}
func (m *meetingsScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (m *meetingsScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case meetingsLoadedMsg:
		m.loading = false
		m.err = v.Err
		m.all = v.Result.Meetings
		m.applyFilter()
		return m, nil
	case tea.KeyMsg:
		if m.filterFocus {
			switch v.String() {
			case "esc":
				m.filterFocus = false
				m.filter.Blur()
				return m, nil
			case "enter":
				m.filterFocus = false
				m.filter.Blur()
				m.applyFilter()
				return m, nil
			}
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.applyFilter()
			return m, cmd
		}
		switch {
		case key.Matches(v, ctx.keys.Search):
			m.filterFocus = true
			m.filter.Focus()
			return m, nil
		case key.Matches(v, ctx.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(v, ctx.keys.Down):
			if m.cursor < len(m.view_)-1 {
				m.cursor++
			}
		case key.Matches(v, ctx.keys.Enter):
			if m.cursor < len(m.view_) {
				return m, push(sDetail, m.view_[m.cursor].ID)
			}
		case key.Matches(v, ctx.keys.Delete):
			if m.cursor < len(m.view_) {
				id := m.view_[m.cursor].ID
				return m, deleteMeetingCmd(ctx, id)
			}
		}
	}
	return m, nil
}

func (m *meetingsScreen) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	if q == "" {
		m.view_ = m.all
	} else {
		out := make([]notoapi.Meeting, 0, len(m.all))
		for _, mtg := range m.all {
			if strings.Contains(strings.ToLower(mtg.Title), q) ||
				strings.Contains(strings.ToLower(mtg.ShortSummary), q) {
				out = append(out, mtg)
			}
		}
		m.view_ = out
	}
	if m.cursor >= len(m.view_) {
		m.cursor = 0
	}
}

func (m *meetingsScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if m.loading {
		return panelEmpty(ctx, "meetings", s.Muted.Render("loading meetings…"))
	}
	if m.err != nil {
		return panelEmpty(ctx, "meetings", s.Danger.Render("✗ "+m.err.Error()))
	}

	var body string
	if len(m.all) == 0 {
		body = renderEmptyState(s, "No meetings yet", []emptyAction{
			{Key: "r", Label: "record a meeting"},
			{Key: ":", Label: "open the command menu (search, settings, providers)"},
		})
	} else {
		titleW := max(20, ctx.width/3)
		headerRow := s.Muted.Render(fmt.Sprintf("   %-*s   %-12s   %-5s   %-10s   %-7s",
			titleW, "TITLE", "WHEN", "DUR", "STATUS", "D/A/R"))
		rows := []string{headerRow}
		for i, mtg := range m.view_ {
			title := fit(mtg.Title, titleW)
			when := mtg.CreatedAt.Format("Jan 02 15:04")
			dur := fmt.Sprintf("%dm", mtg.DurationSeconds/60)
			counts := fmt.Sprintf("%d/%d/%d", mtg.DecisionCount, mtg.ActionCount, mtg.RiskCount)
			row := fmt.Sprintf("%s   %s   %-5s   %-10s   %s",
				title, fit(when, 12), dur, statusBadge(s, mtg.Status), counts)
			if i == m.cursor {
				row = s.RowSelected.Render(" ▸ " + row)
			} else {
				row = "   " + row
			}
			rows = append(rows, row)
		}
		body = strings.Join(rows, "\n")
		if len(m.view_) == 0 {
			body += "\n\n" + s.Muted.Render("no matches — clear filter with esc")
		}
	}

	filterPrefix := s.ChipKey.Render("/") + " "
	filterBox := filterPrefix + m.filter.View()
	if !m.filterFocus && m.filter.Value() == "" {
		filterBox = filterPrefix + s.Muted.Render(m.filter.Placeholder)
	}

	chips := strings.Join([]string{
		hint(s, "⏎", "open"),
		hint(s, "/", "filter"),
		hint(s, "d", "delete"),
		hint(s, "ctrl+r", "refresh"),
	}, "   ")

	body = filterBox + "\n\n" + body + "\n\n" + s.HintBar.Render(chips)
	return layout.Panel{
		Title:    "meetings",
		Subtitle: fmt.Sprintf("%d / %d", len(m.view_), len(m.all)),
		Width:    ctx.width,
		Height:   ctx.height,
		Focused:  true,
		Body:     body,
	}.Render(s)
}

func deleteMeetingCmd(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		err := ctx.client.DeleteMeeting(ctx.ctx, id)
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "meeting deleted"}
	}
}

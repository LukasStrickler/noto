package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// configScreen is the unified configuration surface. Left pane =
// routing/paths (what's active, where artifacts live, theme). Right
// pane = provider catalog with per-provider API key management. Tab
// switches focus between the two panes. Replaces the older standalone
// `settings` + `providers` screens — one screen, two columns, one
// mental model.
type configScreen struct {
	focus int // 0 = routing (left), 1 = providers (right)

	cfg     notoapi.Config
	cfgLoad bool

	provs    []notoapi.ProviderInfo
	provLoad bool

	provCursor    int
	routingCursor int

	keyEdit  bool
	editID   string
	keyInput textinput.Model

	err error
}

func newConfigScreen() screen {
	ti := textinput.New()
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Placeholder = "paste API key…"
	return &configScreen{cfgLoad: true, provLoad: true, keyInput: ti}
}

func (c *configScreen) id() screenID         { return sConfig }
func (c *configScreen) title() string        { return "config" }
func (c *configScreen) helpKeys() []keys.Map { return nil }
func (c *configScreen) inputActive() bool    { return c.keyEdit }

func (c *configScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	return tea.Batch(fetchConfig(ctx), fetchProviders(ctx))
}
func (c *configScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (c *configScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case configLoadedMsg:
		c.cfgLoad = false
		c.cfg = v.Cfg
		if v.Err != nil {
			c.err = v.Err
		}
	case providersLoadedMsg:
		c.provLoad = false
		c.provs = v.Providers
		if v.Err != nil {
			c.err = v.Err
		}
	case providerKeyResultMsg:
		c.keyEdit = false
		c.keyInput.SetValue("")
		c.keyInput.Blur()
		if v.Err != nil {
			return c, func() tea.Msg { return bannerMsg{Kind: "error", Text: v.Err.Error()} }
		}
		return c, tea.Batch(fetchProviders(ctx), func() tea.Msg {
			return bannerMsg{Kind: "info", Text: "key saved for " + v.ProviderID}
		})
	case providerTestMsg:
		text := fmt.Sprintf("%s: %s (%dms)", v.ProviderID,
			ternary(v.Result.OK, "ok", "failed"),
			v.Result.LatencyMS)
		return c, func() tea.Msg { return bannerMsg{Kind: "info", Text: text} }
	case tea.KeyMsg:
		if c.keyEdit {
			switch v.String() {
			case "esc":
				c.keyEdit = false
				c.keyInput.SetValue("")
				c.keyInput.Blur()
				return c, nil
			case "enter":
				val := strings.TrimSpace(c.keyInput.Value())
				if val == "" {
					return c, nil
				}
				return c, setProviderKeyCmd(ctx, c.editID, val)
			}
			var cmd tea.Cmd
			c.keyInput, cmd = c.keyInput.Update(msg)
			return c, cmd
		}
		switch {
		case key.Matches(v, ctx.keys.Tab), key.Matches(v, ctx.keys.ShiftTab):
			c.focus = 1 - c.focus
			return c, nil
		case key.Matches(v, ctx.keys.Up):
			c.moveCursor(-1)
		case key.Matches(v, ctx.keys.Down):
			c.moveCursor(1)
		case key.Matches(v, ctx.keys.Enter):
			return c, c.activate()
		case v.String() == "e":
			if c.focus == 1 && c.provCursor < len(c.provs) && c.provs[c.provCursor].Kind != "fake" {
				c.editID = c.provs[c.provCursor].ID
				c.keyEdit = true
				c.keyInput.Focus()
			}
		case v.String() == "t":
			if c.focus == 1 && c.provCursor < len(c.provs) {
				return c, testProviderCmd(ctx, c.provs[c.provCursor].ID)
			}
		case v.String() == "x":
			if c.focus == 1 && c.provCursor < len(c.provs) {
				id := c.provs[c.provCursor].ID
				return c, func() tea.Msg {
					if err := ctx.client.DeleteProviderKey(ctx.ctx, id); err != nil {
						return bannerMsg{Kind: "error", Text: err.Error()}
					}
					return bannerMsg{Kind: "info", Text: "key removed"}
				}
			}
		case v.String() == "a":
			return c, c.setActiveFromCursor(ctx)
		}
	}
	return c, nil
}

func (c *configScreen) moveCursor(delta int) {
	if c.focus == 0 {
		n := len(c.routingRows())
		if n == 0 {
			return
		}
		c.routingCursor = layout.Clamp(c.routingCursor+delta, 0, n-1)
	} else {
		n := len(c.provs)
		if n == 0 {
			return
		}
		c.provCursor = layout.Clamp(c.provCursor+delta, 0, n-1)
	}
}

func (c *configScreen) activate() tea.Cmd {
	if c.focus == 1 && c.provCursor < len(c.provs) && c.provs[c.provCursor].Kind != "fake" {
		c.editID = c.provs[c.provCursor].ID
		c.keyEdit = true
		c.keyInput.Focus()
	}
	return nil
}

func (c *configScreen) setActiveFromCursor(ctx screenCtx) tea.Cmd {
	if c.focus != 1 || c.provCursor >= len(c.provs) {
		return nil
	}
	p := c.provs[c.provCursor]
	return func() tea.Msg {
		var err error
		switch p.Kind {
		case "speech":
			err = ctx.client.SetActiveSpeech(ctx.ctx, p.ID)
		case "llm":
			model := ""
			if len(p.Models) > 0 {
				model = p.Models[0].ID
			}
			err = ctx.client.SetActiveLLMModel(ctx.ctx, model)
		default:
			return bannerMsg{Kind: "warn", Text: "fake providers can't be set active"}
		}
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "active " + p.Kind + " set to " + p.ID}
	}
}

type configRow struct {
	label string
	value string
}

func (c *configScreen) routingRows() []configRow {
	return []configRow{
		{"active speech", c.cfg.Routing.SpeechProvider},
		{"active llm", c.cfg.Routing.LLMProvider},
		{"llm model", c.cfg.Routing.LLMModel},
		{"theme", c.cfg.UI.Theme},
		{"config dir", c.cfg.ConfigDir},
		{"artifact root", c.cfg.ArtifactRoot},
		{"recordings dir", c.cfg.RecordingsDir},
	}
}

func (c *configScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if c.cfgLoad && c.provLoad {
		return panelEmpty(ctx, "config", s.Muted.Render("loading…"))
	}
	if c.err != nil {
		return panelEmpty(ctx, "config", s.Danger.Render("✗ "+c.err.Error()))
	}

	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		body := c.renderRouting(s, ctx.width-6) + "\n\n" +
			c.renderProviders(s, ctx.width-6) +
			c.renderKeyOverlay(s) + "\n\n" +
			c.renderHints(s)
		return layout.Panel{
			Title:    "config",
			Subtitle: "tab to switch panes",
			Width:    ctx.width, Height: ctx.height,
			Focused: true, Body: body,
		}.Render(s)
	}
	leftW := ctx.width * 5 / 12
	rightW := ctx.width - leftW - 1
	leftPanel := layout.Panel{
		Title: "routing & paths", Subtitle: "tab to providers",
		Width: leftW, Height: ctx.height,
		Focused: c.focus == 0,
		Body:    c.renderRouting(s, leftW-4) + "\n\n" + c.renderHints(s),
	}.Render(s)
	rightPanel := layout.Panel{
		Title:    "providers",
		Subtitle: fmt.Sprintf("%d available", len(c.provs)),
		Width:    rightW, Height: ctx.height,
		Focused: c.focus == 1,
		Body:    c.renderProviders(s, rightW-4) + c.renderKeyOverlay(s),
	}.Render(s)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)
}

func (c *configScreen) renderRouting(s theme.Styles, width int) string {
	rows := []string{
		s.HeaderEm.Render("What's active · where files live"),
		"",
	}
	for i, r := range c.routingRows() {
		valW := max(10, width-22)
		line := fmt.Sprintf("  %-15s %s",
			s.Muted.Render(r.label),
			s.HeaderEm.Render(fit(r.value, valW)),
		)
		if c.focus == 0 && i == c.routingCursor {
			line = s.RowSelected.Render(" ▸ " + r.label + "  " + fit(r.value, valW))
		}
		rows = append(rows, line)
	}
	rows = append(rows,
		"",
		s.Muted.Render("To change a provider:"),
		"  "+s.ChipKey.Render("tab")+"  "+s.Muted.Render("focus the providers pane"),
		"  "+s.ChipKey.Render("↑/↓")+"  "+s.Muted.Render("pick a row"),
		"  "+s.ChipKey.Render("a")+"  "+s.Muted.Render("set as active speech / llm"),
		"  "+s.ChipKey.Render("e")+"  "+s.Muted.Render("edit API key"),
	)
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderProviders(s theme.Styles, width int) string {
	if len(c.provs) == 0 {
		return s.Muted.Render("no providers registered")
	}
	idW := 12
	notesW := max(14, width-idW-22)
	header := s.Muted.Render(fmt.Sprintf("   %-*s  %-8s  %s",
		idW, "PROVIDER", "KEY", "NOTES"))
	rows := []string{header}
	for i, pr := range c.provs {
		var keyCell string
		switch {
		case pr.Kind == "fake":
			keyCell = s.Muted.Render("—")
		case pr.HasKey:
			keyCell = s.Success.Render("✓ " + pr.KeySource)
		default:
			keyCell = s.Warning.Render("△ missing")
		}
		notes := pr.Notes
		switch {
		case pr.IsActiveSpeech:
			notes = s.Success.Render("active speech · ") + notes
		case pr.IsActiveLLM:
			notes = s.Success.Render("active llm · ") + notes
		}
		idCell := s.HeaderEm.Render(fit(pr.ID, idW))
		row := fmt.Sprintf("%s  %-14s  %s",
			idCell, keyCell, fit(notes, notesW))
		if c.focus == 1 && i == c.provCursor {
			row = s.RowSelected.Render(" ▸ " + row)
		} else {
			row = "   " + row
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderKeyOverlay(s theme.Styles) string {
	if !c.keyEdit {
		return ""
	}
	return "\n\n" + strings.Join([]string{
		s.OverlayTitle.Render("Set API key for " + c.editID),
		c.keyInput.View(),
		s.Muted.Render("enter to save · esc to cancel"),
	}, "\n")
}

func (c *configScreen) renderHints(s theme.Styles) string {
	chips := []string{
		hint(s, "tab", "switch pane"),
		hint(s, "↑/↓", "navigate"),
		hint(s, "⏎", "edit key"),
		hint(s, "a", "set active"),
		hint(s, "e", "edit key"),
		hint(s, "t", "test"),
		hint(s, "x", "remove"),
		hint(s, "esc", "back"),
	}
	return s.HintBar.Render(strings.Join(chips, "   "))
}

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
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// configScreen is the unified configuration surface (screen 3). Left
// pane is a section nav; right pane shows the selected section's
// content + actions. Tab/Shift-Tab moves focus between the two panes.
//
// Sections are intentionally small and orthogonal:
//   - Active routes  → which speech / LLM provider is in use
//   - API keys       → manage credentials for cloud providers
//   - Storage        → recordings dir, index state, retention
//   - Paths          → read-only filesystem paths
//
// Replaces the older standalone `providers` / `settings` / `storage`
// screens (formerly screens 4/5/6). All three entry keys now collapse
// to this one surface.
type configScreen struct {
	section    int // index into sections[]
	rightFocus bool

	cfg     notoapi.Config
	cfgLoad bool

	provs    []notoapi.ProviderInfo
	provLoad bool

	storage     notoapi.Storage
	storageLoad bool

	// cursors per-section (0 = active routes, 1 = api keys, 2 = storage, 3 = paths)
	cursors [4]int

	keyEdit  bool
	editID   string
	keyInput textinput.Model

	err error
}

const (
	secActive = iota
	secAPIKeys
	secStorage
	secPaths
)

type configSection struct {
	id    int
	label string
	hint  string
}

func (c *configScreen) sections() []configSection {
	return []configSection{
		{secActive, "Active routes", "speech + LLM"},
		{secAPIKeys, "API keys", fmt.Sprintf("%d cloud providers", c.countKeyProviders())},
		{secStorage, "Storage", "health · retention"},
		{secPaths, "Paths", "where data lives"},
	}
}

func (c *configScreen) countKeyProviders() int {
	n := 0
	for _, p := range c.provs {
		if p.Kind == "fake" {
			continue
		}
		n++
	}
	return n
}

func newConfigScreen() screen {
	ti := textinput.New()
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 256
	ti.Placeholder = "paste API key…"
	return &configScreen{
		cfgLoad:     true,
		provLoad:    true,
		storageLoad: true,
		keyInput:    ti,
	}
}

func (c *configScreen) id() screenID      { return sConfig }
func (c *configScreen) title() string     { return "config" }
func (c *configScreen) inputActive() bool { return c.keyEdit }

func (c *configScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	return tea.Batch(
		fetchConfig(ctx),
		fetchProviders(ctx),
		fetchStorage(ctx),
	)
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
	case storageLoadedMsg:
		c.storageLoad = false
		c.storage = v.Storage
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
			return c.updateKeyInput(ctx, v)
		}
		return c.updateKey(ctx, v)
	}
	return c, nil
}

func (c *configScreen) updateKeyInput(ctx screenCtx, v tea.KeyMsg) (screen, tea.Cmd) {
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
	c.keyInput, cmd = c.keyInput.Update(v)
	return c, cmd
}

func (c *configScreen) updateKey(ctx screenCtx, v tea.KeyMsg) (screen, tea.Cmd) {
	// Left/right + tab toggle pane focus. Up/down move within the
	// focused pane.
	switch {
	case key.Matches(v, ctx.keys.Tab), key.Matches(v, ctx.keys.ShiftTab),
		key.Matches(v, ctx.keys.Left), key.Matches(v, ctx.keys.Right):
		c.rightFocus = !c.rightFocus
		return c, nil
	case key.Matches(v, ctx.keys.Up):
		c.moveCursor(-1)
		return c, nil
	case key.Matches(v, ctx.keys.Down):
		c.moveCursor(1)
		return c, nil
	}
	// Section-specific actions only fire when the right pane has focus.
	if !c.rightFocus {
		return c, nil
	}
	switch c.sections()[c.section].id {
	case secActive:
		return c.handleActiveKey(ctx, v)
	case secAPIKeys:
		return c.handleAPIKeyKey(ctx, v)
	}
	return c, nil
}

func (c *configScreen) moveCursor(delta int) {
	if !c.rightFocus {
		n := len(c.sections())
		c.section = layout.Clamp(c.section+delta, 0, n-1)
		return
	}
	id := c.sections()[c.section].id
	var n int
	switch id {
	case secActive:
		n = len(c.activeRouteRows())
	case secAPIKeys:
		n = len(c.keyProviders())
	}
	if n == 0 {
		return
	}
	c.cursors[id] = layout.Clamp(c.cursors[id]+delta, 0, n-1)
}

// --- Active routes ---

type routeRow struct {
	kind  string // "speech" | "llm"
	label string
}

func (c *configScreen) activeRouteRows() []routeRow {
	return []routeRow{
		{"speech", "Active speech provider"},
		{"llm", "Active LLM provider"},
	}
}

func (c *configScreen) handleActiveKey(ctx screenCtx, v tea.KeyMsg) (screen, tea.Cmd) {
	if !key.Matches(v, ctx.keys.Enter) {
		return c, nil
	}
	// Cycle through eligible providers for the focused route kind.
	row := c.activeRouteRows()[c.cursors[secActive]]
	current := ""
	if row.kind == "speech" {
		current = c.cfg.Routing.SpeechProvider
	} else {
		current = c.cfg.Routing.LLMProvider
	}
	eligible := c.eligibleForKind(row.kind)
	if len(eligible) == 0 {
		return c, func() tea.Msg { return bannerMsg{Kind: "warn", Text: "no eligible providers"} }
	}
	next := eligible[0].ID
	for i, p := range eligible {
		if p.ID == current {
			next = eligible[(i+1)%len(eligible)].ID
			break
		}
	}
	chosen := next
	return c, func() tea.Msg {
		var err error
		if row.kind == "speech" {
			err = ctx.client.SetActiveSpeech(ctx.ctx, chosen)
		} else {
			model := ""
			for _, p := range eligible {
				if p.ID == chosen && len(p.Models) > 0 {
					model = p.Models[0].ID
					break
				}
			}
			err = ctx.client.SetActiveLLMModel(ctx.ctx, model)
		}
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		// Refetch config so the screen reflects the change.
		return tea.Batch(fetchConfig(ctx), func() tea.Msg {
			return bannerMsg{Kind: "info", Text: row.kind + " → " + chosen}
		})()
	}
}

func (c *configScreen) eligibleForKind(kind string) []notoapi.ProviderInfo {
	out := []notoapi.ProviderInfo{}
	for _, p := range c.provs {
		switch kind {
		case "speech":
			if p.Kind == "speech" {
				out = append(out, p)
			}
		case "llm":
			if p.Kind == "llm" {
				out = append(out, p)
			}
		}
	}
	return out
}

// --- API keys ---

// keyProviders returns providers that actually take an API key. Fake
// is excluded by construction so it never appears on this screen.
func (c *configScreen) keyProviders() []notoapi.ProviderInfo {
	out := []notoapi.ProviderInfo{}
	for _, p := range c.provs {
		if p.Kind == "fake" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (c *configScreen) handleAPIKeyKey(ctx screenCtx, v tea.KeyMsg) (screen, tea.Cmd) {
	rows := c.keyProviders()
	if len(rows) == 0 {
		return c, nil
	}
	cur := c.cursors[secAPIKeys]
	if cur >= len(rows) {
		return c, nil
	}
	pr := rows[cur]
	switch {
	case key.Matches(v, ctx.keys.Edit, ctx.keys.Enter):
		c.editID = pr.ID
		c.keyEdit = true
		c.keyInput.Focus()
	case key.Matches(v, ctx.keys.Test):
		return c, testProviderCmd(ctx, pr.ID)
	case key.Matches(v, ctx.keys.Remove):
		return c, func() tea.Msg {
			if err := ctx.client.DeleteProviderKey(ctx.ctx, pr.ID); err != nil {
				return bannerMsg{Kind: "error", Text: err.Error()}
			}
			return bannerMsg{Kind: "info", Text: "key removed for " + pr.ID}
		}
	}
	return c, nil
}

// --- View ---

func (c *configScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if c.cfgLoad && c.provLoad {
		return panelEmpty(ctx, "config", s.Muted.Render("loading…"))
	}

	k := ctx.keys
	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		body := c.renderLeft(s, k, ctx.width-6) + "\n\n" +
			c.renderRight(s, k, ctx.width-6) +
			c.renderKeyOverlay(s)
		return layout.Panel{
			Title: "config", Subtitle: "tab toggles pane",
			Width: ctx.width, Height: ctx.height,
			Focused: true, Body: body,
		}.Render(s)
	}
	cols := layout.Split(ctx.width, 1, layout.FlexMin(1, 26), layout.Flex(2))
	leftW, rightW := cols[0], cols[1]
	left := layout.Panel{
		Title: "sections", Subtitle: "↑↓ + tab",
		Width: leftW, Height: ctx.height,
		Focused: !c.rightFocus,
		Body:    c.renderLeft(s, k, leftW-4),
	}.Render(s)
	right := layout.Panel{
		Title: c.sections()[c.section].label, Subtitle: c.sections()[c.section].hint,
		Width: rightW, Height: ctx.height,
		Focused: c.rightFocus,
		Body:    c.renderRight(s, k, rightW-4) + c.renderKeyOverlay(s),
	}.Render(s)
	return layout.HStack(left, right)
}

func (c *configScreen) renderLeft(s theme.Styles, k keys.Map, width int) string {
	rows := []string{s.HeaderEm.Render("Configuration"), ""}
	for i, sec := range c.sections() {
		row := fmt.Sprintf("%-18s %s", sec.label, s.Muted.Render(fit(sec.hint, max(8, width-22))))
		if i == c.section {
			marker := " ▸ "
			if !c.rightFocus {
				row = s.RowSelected.Render(marker + row)
			} else {
				row = s.HeaderEm.Render(marker) + row
			}
		} else {
			row = "   " + row
		}
		rows = append(rows, row)
	}
	rows = append(rows,
		"",
		s.Muted.Render(k.Left.Help().Key+"/"+k.Right.Help().Key+" or "+k.Tab.Help().Key),
		s.Muted.Render("toggle pane focus"),
	)
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderRight(s theme.Styles, k keys.Map, width int) string {
	switch c.sections()[c.section].id {
	case secActive:
		return c.renderActive(s, k, width)
	case secAPIKeys:
		return c.renderAPIKeys(s, k, width)
	case secStorage:
		return c.renderStorage(s, width)
	case secPaths:
		return c.renderPaths(s, width)
	}
	return ""
}

func (c *configScreen) renderActive(s theme.Styles, k keys.Map, width int) string {
	rows := []string{
		s.HeaderEm.Render("What's actively routing your audio + LLM work"),
		"",
	}
	cur := c.cursors[secActive]
	for i, r := range c.activeRouteRows() {
		var val string
		if r.kind == "speech" {
			val = c.cfg.Routing.SpeechProvider
		} else {
			val = c.cfg.Routing.LLMProvider
			if c.cfg.Routing.LLMModel != "" {
				val += " / " + c.cfg.Routing.LLMModel
			}
		}
		line := fmt.Sprintf("  %-26s %s",
			s.Muted.Render(r.label), s.HeaderEm.Render(fit(val, max(10, width-30))))
		if c.rightFocus && i == cur {
			line = s.RowSelected.Render(" ▸ " + r.label + "  " + fit(val, max(10, width-30)))
		}
		rows = append(rows, line)
	}
	rows = append(rows,
		"",
		s.Muted.Render("Enter cycles through eligible providers for the focused row."),
		"  "+chipAs(s, k.Enter, "next provider"),
	)
	return strings.Join(rows, "\n")
}

func (c *configScreen) renderAPIKeys(s theme.Styles, k keys.Map, width int) string {
	rows := c.keyProviders()
	if len(rows) == 0 {
		return s.Muted.Render("no cloud providers registered")
	}
	out := []string{
		s.HeaderEm.Render("API keys for cloud providers"),
		"",
	}
	idW := 14
	notesW := max(14, width-idW-22)
	out = append(out, s.Muted.Render(fmt.Sprintf("   %-*s  %-8s  %s", idW, "PROVIDER", "KEY", "NOTES")))
	cur := c.cursors[secAPIKeys]
	for i, pr := range rows {
		var keyCell string
		if pr.HasKey {
			keyCell = s.Success.Render("✓ " + pr.KeySource)
		} else {
			keyCell = s.Warning.Render("△ missing")
		}
		notes := pr.Notes
		if pr.IsActiveSpeech {
			notes = s.Success.Render("active speech · ") + notes
		} else if pr.IsActiveLLM {
			notes = s.Success.Render("active llm · ") + notes
		}
		idCell := s.HeaderEm.Render(fit(pr.ID, idW))
		line := fmt.Sprintf("%s  %-14s  %s", idCell, keyCell, fit(notes, notesW))
		if c.rightFocus && i == cur {
			line = s.RowSelected.Render(" ▸ " + line)
		} else {
			line = "   " + line
		}
		out = append(out, line)
	}
	out = append(out, "",
		"  "+chipPair(s, k.Enter, k.Edit, "edit key"),
		"  "+chipAs(s, k.Test, "test connectivity"),
		"  "+chipAs(s, k.Remove, "remove key"),
	)
	return strings.Join(out, "\n")
}

func (c *configScreen) renderStorage(s theme.Styles, width int) string {
	_ = width
	if c.storageLoad {
		return s.Muted.Render("loading storage…")
	}
	row := func(label, value string) string {
		return s.Muted.Render(fmt.Sprintf("  %-18s", label)) + s.HeaderEm.Render(value)
	}
	indexCell := def(c.storage.IndexState, "clean")
	switch indexCell {
	case "clean":
		indexCell = s.Success.Render("✓ clean")
	case "indexing":
		indexCell = s.Info.Render("⟳ indexing")
	default:
		indexCell = s.Warning.Render("△ " + indexCell)
	}
	lines := []string{
		s.HeaderEm.Render("Local artifact health"),
		"",
		row("recordings dir", c.storage.RecordingsDir),
		s.Muted.Render(fmt.Sprintf("  %-18s", "meetings")) + s.HeaderEm.Render(fmt.Sprintf("%d", c.storage.MeetingCount)),
		s.Muted.Render(fmt.Sprintf("  %-18s", "index")) + indexCell,
		row("schema version", def(c.storage.SchemaVersion, "config.v1")),
		"",
		s.HeaderEm.Render("Retention"),
		"  " + s.Muted.Render("delete raw audio after valid transcript:") + "  " + s.Warning.Render("off"),
	}
	return strings.Join(lines, "\n")
}

func (c *configScreen) renderPaths(s theme.Styles, _ int) string {
	row := func(label, value string) string {
		return s.Muted.Render(fmt.Sprintf("  %-18s", label)) + s.HeaderEm.Render(value)
	}
	return strings.Join([]string{
		s.HeaderEm.Render("Filesystem"),
		"",
		row("config dir", c.cfg.ConfigDir),
		row("artifact root", c.cfg.ArtifactRoot),
		row("recordings dir", c.cfg.RecordingsDir),
		"",
		s.HeaderEm.Render("UI"),
		row("theme", c.cfg.UI.Theme),
	}, "\n")
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

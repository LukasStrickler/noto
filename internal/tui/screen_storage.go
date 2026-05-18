package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/layout"
)

type storageScreen struct {
	st     notoapi.Storage
	cfg    notoapi.Config
	loading bool
	err    error
}

func newStorageScreen() screen { return &storageScreen{loading: true} }

func (s *storageScreen) id() screenID    { return sStorage }
func (s *storageScreen) title() string   { return "storage" }
func (s *storageScreen) helpKeys() []keys.Map { return nil }
func (s *storageScreen) inputActive() bool    { return false }

func (s *storageScreen) enter(ctx screenCtx, _ string) tea.Cmd {
	return tea.Batch(fetchStorage(ctx), fetchConfig(ctx))
}
func (s *storageScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (s *storageScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case storageLoadedMsg:
		s.loading = false
		s.err = v.Err
		s.st = v.Storage
	case configLoadedMsg:
		s.cfg = v.Cfg
	case tea.KeyMsg:
		switch {
		case key.Matches(v, ctx.keys.Verify):
			return s, verifyStorageCmd(ctx)
		case v.String() == "r":
			return s, func() tea.Msg {
				if _, err := ctx.client.ReindexStorage(ctx.ctx); err != nil {
					return bannerMsg{Kind: "error", Text: err.Error()}
				}
				return bannerMsg{Kind: "info", Text: "reindex queued"}
			}
		}
	}
	return s, nil
}

func (s *storageScreen) view(ctx screenCtx) string {
	st := ctx.styles
	if s.loading {
		return panelEmpty(ctx, "storage", st.Muted.Render("loading…"))
	}
	if s.err != nil {
		return panelEmpty(ctx, "storage", st.Danger.Render("✗ "+s.err.Error()))
	}
	row := func(label, value string) string {
		return st.Muted.Render(fmt.Sprintf("  %-18s", label)) + st.HeaderEm.Render(value)
	}
	indexState := def(s.st.IndexState, "clean")
	indexCell := indexState
	switch indexState {
	case "clean":
		indexCell = st.Success.Render("✓ clean")
	case "indexing":
		indexCell = st.Info.Render("⟳ indexing")
	default:
		indexCell = st.Warning.Render("△ " + indexState)
	}
	lines := []string{
		st.HeaderEm.Render("Local artifact health"),
		"",
		row("recordings dir", s.st.RecordingsDir),
		st.Muted.Render(fmt.Sprintf("  %-18s", "meetings")) + st.HeaderEm.Render(fmt.Sprintf("%d", s.st.MeetingCount)),
		st.Muted.Render(fmt.Sprintf("  %-18s", "index")) + indexCell,
		row("schema version", def(s.st.SchemaVersion, "config.v1")),
		"",
		st.HeaderEm.Render("Paths"),
		row("config dir", s.cfg.ConfigDir),
		row("artifact root", s.cfg.ArtifactRoot),
		"",
		st.HeaderEm.Render("Retention"),
		"  " + st.Muted.Render("delete raw audio after valid transcript:") + "  " + st.Warning.Render("off"),
	}
	body := strings.Join(lines, "\n")
	hints := strings.Join([]string{
		hint(st, "v", "verify checksums"),
		hint(st, "r", "rebuild index"),
		hint(st, "esc", "back"),
	}, "   ")
	body += "\n\n" + st.HintBar.Render(hints)
	return layout.Panel{Title: "storage", Width: ctx.width, Height: ctx.height, Focused: true, Body: body}.Render(st)
}

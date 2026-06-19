package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// configRoot builds a root model on the Config screen with providers loaded and
// the given section open.
func configRoot(t *testing.T, section int) (*rootModel, *configScreen) {
	t.Helper()
	c := newConfigScreen().(*configScreen)
	c.cfgLoad, c.provLoad, c.storageLoad = false, false, false
	c.provs = []notoapi.ProviderInfo{
		{ID: "openai", Kind: "llm", HasKey: true, KeySource: "env"},
		{ID: "deepgram", Kind: "speech", IsActiveSpeech: true},
	}
	c.section = section
	r := testRootModel()
	r.stack = []screen{c}
	r.width, r.height = 140, 30
	return r, c
}

// Clicking a left-nav section selects it and parks focus on the nav.
func TestConfigClickSectionSelects(t *testing.T) {
	r, c := configRoot(t, secActive)

	clickRoot(t, r, "API keys")

	if c.section != secAPIKeys {
		t.Errorf("section = %d after clicking API keys; want %d", c.section, secAPIKeys)
	}
	if c.rightFocus {
		t.Errorf("clicking a section should park focus on the nav, not the content")
	}
}

// Clicking a provider row in the API-keys section crosses into the right pane
// and selects that row.
func TestConfigClickKeyRowFocuses(t *testing.T) {
	r, c := configRoot(t, secAPIKeys)

	clickRoot(t, r, "openai")

	if !c.rightFocus {
		t.Errorf("clicking a key row did not focus the content pane")
	}
	if c.cursors[secAPIKeys] != 0 {
		t.Errorf("cursor = %d; want 0 (openai row)", c.cursors[secAPIKeys])
	}
}

// Clicking the "edit key" action chip focuses the right pane and replays the
// key, opening the key editor.
func TestConfigClickEditChip(t *testing.T) {
	r, c := configRoot(t, secAPIKeys)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "edit key")

	r2, cmd := clickAt(t, r, x, y)
	dispatch(t, r2, cmd)

	if !c.keyEdit {
		t.Errorf("clicking the edit-key chip did not open the key editor")
	}
}

// Hovering a section row paints it.
func TestConfigHoverSectionPaints(t *testing.T) {
	r, _ := configRoot(t, secActive)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "Storage")
	if strings.Contains(rawLineWith(t, r.View().Content, "Storage"), overlayBG) {
		t.Fatal("precondition: Storage row already painted before hover")
	}

	r.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})

	if got := rawLineWith(t, r.View().Content, "Storage"); !strings.Contains(got, overlayBG) {
		t.Errorf("hovering the Storage section did not paint it; line=%q", got)
	}
}

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func recorderRoot(t *testing.T) (*rootModel, *recorderScreen) {
	t.Helper()
	rc := newRecorderScreen().(*recorderScreen)
	r := testRootModel()
	r.stack = []screen{rc}
	r.width, r.height = 140, 30
	return r, rc
}

// Clicking the "edit title" chip replays its key and focuses the title input —
// the chip and the keystroke are one action.
func TestRecorderClickEditTitleChip(t *testing.T) {
	r, rc := recorderRoot(t)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "edit title")

	r2, cmd := clickAt(t, r, x, y)
	dispatch(t, r2, cmd)

	if !rc.titleFocus {
		t.Errorf("clicking the edit-title chip did not focus the title input")
	}
}

// Clicking the title field itself starts editing (replays the edit-title key).
func TestRecorderClickTitleFieldFocuses(t *testing.T) {
	r, rc := recorderRoot(t)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "title  ") // the field label

	r2, cmd := clickAt(t, r, x, y)
	dispatch(t, r2, cmd)

	if !rc.titleFocus {
		t.Errorf("clicking the title field did not focus the title input")
	}
}

// Hovering an action chip lights it up. (Uses the "edit title" chip — "start"
// also appears in the panel subtitle, so it isn't a unique probe.)
func TestRecorderHoverChipPaints(t *testing.T) {
	r, _ := recorderRoot(t)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "edit title")
	if strings.Contains(rawLineWith(t, r.View().Content, "edit title"), overlayBG) {
		t.Fatal("precondition: chip already painted before hover")
	}

	r.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})

	if got := rawLineWith(t, r.View().Content, "edit title"); !strings.Contains(got, overlayBG) {
		t.Errorf("hovering the edit-title chip did not light it; line=%q", got)
	}
}

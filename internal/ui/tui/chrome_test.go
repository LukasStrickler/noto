package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The top nav strip must list every numbered page (so a newcomer can see
// where to go) and light the active one — all derived from topScreens, so
// the strip can never drift from the real nav keys.
func TestNavStripListsEveryPageAndLightsActive(t *testing.T) {
	m := testRootModel()
	header := ansi.Strip(m.renderHeader())

	for i, sc := range topScreens {
		if !strings.Contains(header, sc.title) {
			t.Errorf("nav strip missing page %q", sc.title)
		}
		digit := sc.navBinding(i).Help().Key
		if idx := strings.IndexByte(digit, '/'); idx >= 0 {
			digit = digit[:idx]
		}
		if !strings.Contains(header, digit) {
			t.Errorf("nav strip missing digit %q for page %q", digit, sc.title)
		}
	}

	// The active page (stack[0]) is rendered as a filled accent pill; the
	// raw (un-stripped) header must contain that exact styled span so the
	// highlight tracks the active screen, not a hardcoded one.
	active := topScreens[0] // dashboard is stack[0] in testRootModel
	pill := m.styles.RowSelected.Render(" 1 " + active.title + " ")
	if !strings.Contains(m.renderHeader(), pill) {
		t.Errorf("active page %q is not rendered as a highlighted pill", active.title)
	}
}

// Attention flags ride on the nav pills now: a page with work shows an amber
// ⚑N next to its number, a clean page shows none, and each flag points at the
// screen that owns the work (dashboard speakers · people · config). A quiet
// run shows no flag at all.
func TestNavFlagsRouteAttentionToOwningScreen(t *testing.T) {
	m := testRootModel()

	if got := ansi.Strip(m.renderHeader()); strings.Contains(got, "⚑") {
		t.Errorf("nav flags shown with nothing to do:\n%s", got)
	}

	m.statusBar.SpeakersToID = 3   // → dashboard pill
	m.statusBar.PeopleToReview = 2 // → people pill
	m.statusBar.ConfigIssues = 1   // → config pill
	got := ansi.Strip(m.renderHeader())
	for _, want := range []string{"⚑3", "⚑2", "⚑1"} {
		if !strings.Contains(got, want) {
			t.Errorf("nav strip missing flag %q:\n%s", want, got)
		}
	}
}

// The nav strip must be positionally stable: moving the selection only recolors
// a pill, never resizes it, so the pills to its right (and their flags) don't
// jump. Pin a later pill's column across every active screen — with a flag in
// play, since the user reported flag-distance drift as the selection moved.
func TestNavStripStableAcrossSelection(t *testing.T) {
	col := func(active screenID) int {
		m := testRootModel()
		m.stack = []screen{buildScreen(active)}
		m.statusBar.ConfigIssues = 2
		return strings.Index(ansi.Strip(m.renderHeader()), "config")
	}
	dash := col(sDashboard)
	if dash < 0 {
		t.Fatalf("'config' pill not found in nav strip")
	}
	for _, id := range []screenID{sPeople, sRecorder, sConfig} {
		if got := col(id); got != dash {
			t.Errorf("nav strip jittered: 'config' at col %d with %q active vs %d with dashboard active", got, id, dash)
		}
	}
}

// On a narrow terminal the strip falls back to digit-only for inactive
// pages (active keeps its label) so it never truncates mid-word.
func TestNavStripCompactsWhenNarrow(t *testing.T) {
	m := testRootModel()
	m.width = 40
	header := ansi.Strip(m.renderHeader())

	if !strings.Contains(header, "dashboard") {
		t.Errorf("compact strip dropped the active page label:\n%s", header)
	}
	if strings.Contains(header, "people") {
		t.Errorf("compact strip should hide inactive labels, got:\n%s", header)
	}
	if !strings.Contains(header, "2") {
		t.Errorf("compact strip should still show inactive digits:\n%s", header)
	}
}

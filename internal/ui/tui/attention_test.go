package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// screenAttention is the single source of "which page needs the user, and how
// much?" — the nav strip's per-pill flags derive from it, so pin that each
// signal routes to the screen that owns the work and unmapped screens stay clean.
func TestScreenAttention(t *testing.T) {
	sb := notoapi.StatusBar{SpeakersToID: 4, PeopleToReview: 2, ConfigIssues: 1}
	cases := map[screenID]int{
		sDashboard: 4,
		sPeople:    2,
		sConfig:    1,
		sRecorder:  0,
		sAgent:     0,
	}
	for id, want := range cases {
		if got := screenAttention(sb, id); got != want {
			t.Errorf("screenAttention(%q) = %d, want %d", id, got, want)
		}
	}
}

// actionForStatus is the single source of "does this need work, and what's the
// verb?" — every flag in the UI derives from it, so pin the mapping.
func TestActionForStatus(t *testing.T) {
	cases := []struct {
		status string
		verb   string
		needs  bool
	}{
		{"auto", "", false},
		{"manual", "", false},
		{"pending", "confirm", true},
		{"new", "review", true},
		{"unmatched", "identify", true},
		{"", "identify", true},
	}
	for _, c := range cases {
		verb, needs := actionForStatus(c.status)
		if verb != c.verb || needs != c.needs {
			t.Errorf("actionForStatus(%q) = (%q, %v), want (%q, %v)", c.status, verb, needs, c.verb, c.needs)
		}
	}
}

// attnGutter must keep the attention bar visible when a flagged row is selected:
// the bar (▍) and the cursor (▸) coexist instead of the bar hiding under the
// highlight. Every state is exactly 3 visible cells so columns stay aligned.
func TestAttnGutter(t *testing.T) {
	s := theme.NewStyles()
	cases := []struct {
		name              string
		selected, attn    bool
		wantBar, wantCurs bool
	}{
		{"idle", false, false, false, false},
		{"selected only", true, false, false, true},
		{"flagged only", false, true, true, false},
		{"selected AND flagged", true, true, true, true},
	}
	for _, c := range cases {
		got := ansi.Strip(attnGutter(s, c.selected, c.attn))
		if ansi.StringWidth(got) != 3 {
			t.Errorf("%s: gutter width = %d, want 3 (got %q)", c.name, ansi.StringWidth(got), got)
		}
		if hasBar := strings.Contains(got, "▍"); hasBar != c.wantBar {
			t.Errorf("%s: bar present = %v, want %v (got %q)", c.name, hasBar, c.wantBar, got)
		}
		if hasCurs := strings.Contains(got, "▸"); hasCurs != c.wantCurs {
			t.Errorf("%s: cursor present = %v, want %v (got %q)", c.name, hasCurs, c.wantCurs, got)
		}
	}
}

// The tab bar must say WHICH tab carries work: the Speakers tab gets a "⚑N"
// badge when speakers still need identifying, and no badge once they're set.
func TestTabBarBadgesSpeakerWork(t *testing.T) {
	s := theme.NewStyles()
	d := newDetailPane()
	d.tab = tabSummary
	carol := "p3"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "manual"},
		"B": {MeetingSpeakerID: "B", MatchStatus: "pending", ProfileID: &carol},
		"C": {MeetingSpeakerID: "C", MatchStatus: "unmatched"},
	}
	if got := d.speakersToID(); got != 2 {
		t.Fatalf("speakersToID() = %d, want 2", got)
	}
	bar := ansi.Strip(d.renderTabBar(screenCtx{styles: s}, 120, 0, 0, nil))
	if !strings.Contains(bar, "speakers ⚑2") {
		t.Errorf("tab bar must badge the speakers tab with the work count; got %q", bar)
	}

	// Resolve everything → the badge disappears.
	for id, mp := range d.mappings {
		mp.MatchStatus = "manual"
		d.mappings[id] = mp
	}
	if got := d.speakersToID(); got != 0 {
		t.Fatalf("speakersToID() after resolve = %d, want 0", got)
	}
	if bar := ansi.Strip(d.renderTabBar(screenCtx{styles: s}, 120, 0, 0, nil)); strings.Contains(bar, "⚑") {
		t.Errorf("no work left, tab bar must not show a flag; got %q", bar)
	}
}

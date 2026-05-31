package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/keys"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

func testScreenCtx() screenCtx {
	return screenCtx{
		ctx:    context.Background(),
		keys:   keys.New(),
		styles: theme.NewStyles(),
		width:  100,
		height: 30,
	}
}

func TestSearchInputTabAdvancesWithinCurrentMeeting(t *testing.T) {
	m := dashboardWithSearchMatches()
	m.pane.id_ = "m1"
	m.pane.transcript = notoapi.Transcript{
		MeetingID: "m1",
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", Text: "may first"},
			{ID: "s2", Text: "may second"},
		},
	}
	m.pane.setQuery("may")

	_, _ = m.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyTab})

	if m.cursor != 0 {
		t.Fatalf("cursor moved to meeting %d; want current meeting", m.cursor)
	}
	if got := m.pane.transcriptScroll; got != 1 {
		t.Fatalf("transcriptScroll = %d; want second matching segment", got)
	}
	if got := m.pane.matchCursor; got != 1 {
		t.Fatalf("matchCursor = %d; want 1", got)
	}
}

func TestSearchInputTabMovesToNextMeetingAfterLastMatch(t *testing.T) {
	m := dashboardWithSearchMatches()
	m.pane.id_ = "m1"
	m.pane.transcript = notoapi.Transcript{
		MeetingID: "m1",
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", Text: "may first"},
			{ID: "s2", Text: "may second"},
		},
	}
	m.pane.setQuery("may")
	m.pane.setActiveMatchSegmentID("s2")

	_, _ = m.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyTab})

	if m.cursor != 1 {
		t.Fatalf("cursor = %d; want next meeting", m.cursor)
	}
	if got := m.pane.requestedMatchSegmentID; got != "s3" {
		t.Fatalf("requestedMatchSegmentID = %q; want first transcript hit in next meeting", got)
	}
}

func TestSearchInputEnterOpensPaneOnActiveTranscriptHit(t *testing.T) {
	m := dashboardWithSearchMatches()
	m.pane.id_ = "m1"
	m.pane.transcript = notoapi.Transcript{
		MeetingID: "m1",
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", Text: "may first"},
		},
	}
	m.pane.setQuery("may")

	_, _ = m.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyEnter})

	if m.inputFocus {
		t.Fatal("inputFocus = true; want search input blurred")
	}
	if !m.paneOpen {
		t.Fatal("paneOpen = false; want detail pane focused")
	}
	if m.pane.tab != tabTranscript {
		t.Fatalf("pane tab = %v; want transcript", m.pane.tab)
	}
	if got := m.pane.requestedMatchSegmentID; got != "s1" {
		t.Fatalf("requestedMatchSegmentID = %q; want active transcript hit", got)
	}
}

func TestSearchInputEscapeReturnsToList(t *testing.T) {
	m := dashboardWithSearchMatches()

	_, _ = m.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyEsc})

	if m.inputFocus {
		t.Fatal("inputFocus = true; want search input blurred")
	}
	if m.paneOpen {
		t.Fatal("paneOpen = true; Escape from search should leave focus on list")
	}
}

func TestDetailPaneRestoresRequestedMatchAfterRecompute(t *testing.T) {
	d := newDetailPane()
	d.id_ = "m1"
	d.transcript = notoapi.Transcript{
		MeetingID: "m1",
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", Text: "may first"},
			{ID: "s2", Text: "may second"},
		},
	}
	d.setQuery("may")

	if !d.setActiveMatchSegmentID("s2") {
		t.Fatal("setActiveMatchSegmentID returned false; want true")
	}
	d.recomputeMatches()

	if d.matchCursor != 1 || d.transcriptScroll != 1 {
		t.Fatalf("matchCursor/transcriptScroll = %d/%d; want 1/1", d.matchCursor, d.transcriptScroll)
	}
}

func TestAttendeeStripWrapsCompactSpeakerRows(t *testing.T) {
	d := newDetailPane()
	d.speakers = []speakerStat{
		{ID: "a", Name: "Alice", TalkSec: 60, colorIdx: 0},
		{ID: "b", Name: "Bob", TalkSec: 45, colorIdx: 1},
		{ID: "c", Name: "Carol", TalkSec: 30, colorIdx: 2},
		{ID: "d", Name: "Dan", TalkSec: 15, colorIdx: 3},
	}
	d.transcript = notoapi.Transcript{
		Segments: []notoapi.TranscriptSegment{
			{SpeakerID: "a", StartSec: 0, EndSec: 60, Text: "a"},
			{SpeakerID: "b", StartSec: 60, EndSec: 105, Text: "b"},
			{SpeakerID: "c", StartSec: 105, EndSec: 135, Text: "c"},
			{SpeakerID: "d", StartSec: 135, EndSec: 150, Text: "d"},
		},
	}

	rendered := ansi.Strip(d.renderAttendeeStrip(theme.NewStyles(), 80))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("rendered %d speaker lines; want 2:\n%s", len(lines), rendered)
	}
	if strings.ContainsAny(rendered, "●") {
		t.Fatalf("speaker strip should not render identity ball markers: %q", rendered)
	}
	if !strings.ContainsAny(rendered, "█") {
		t.Fatalf("speaker strip should keep the timeline graph: %q", rendered)
	}
	if !strings.Contains(rendered, "Alice 01:00") || !strings.Contains(rendered, "Dan 00:15") {
		t.Fatalf("speaker strip missing compact name/time entries: %q", rendered)
	}
}

func TestDashboardRowOmitsDoneBadgeForSummarizedMeeting(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	rows := m.renderListRowDefault(theme.NewStyles(), notoapi.Meeting{
		Title:     "Planning",
		Status:    notoapi.StatusSummarized,
		CreatedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
	}, 80)
	rendered := ansi.Strip(strings.Join(rows, "\n"))

	if strings.Contains(rendered, "done") || strings.Contains(rendered, "✓") {
		t.Fatalf("summarized dashboard row should not render done badge: %q", rendered)
	}
	if strings.Contains(rendered, "todo") {
		t.Fatalf("summarized dashboard row should not render todo badge: %q", rendered)
	}
}

func TestDashboardRowShowsTodoForRecordedMeeting(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	rows := m.renderListRowDefault(theme.NewStyles(), notoapi.Meeting{
		Title:     "Needs transcript",
		Status:    notoapi.StatusRecorded,
		CreatedAt: time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC),
	}, 80)
	rendered := ansi.Strip(strings.Join(rows, "\n"))

	if !strings.Contains(rendered, "todo") {
		t.Fatalf("recorded dashboard row should render todo badge: %q", rendered)
	}
	if strings.Contains(rendered, "done") || strings.Contains(rendered, "✓") {
		t.Fatalf("recorded dashboard row should not render done badge: %q", rendered)
	}
}

func TestDetailPaneTKShortcutsSwitchTabs(t *testing.T) {
	d := newDetailPane()
	d.id_ = "m1"

	if handled, _ := d.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")}); !handled || d.tab != tabTranscript {
		t.Fatalf("t: handled=%v tab=%v; want true / transcript", handled, d.tab)
	}
	if handled, _ := d.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}); !handled || d.tab != tabSpeakers {
		t.Fatalf("k: handled=%v tab=%v; want true / speakers", handled, d.tab)
	}
}

func TestDetailPaneNumberKeysNoLongerSwitchTabs(t *testing.T) {
	// 1/3/4 collide with the global screen-switch bindings, so the pane
	// must not claim any number key — otherwise tab jumps would work
	// inconsistently (only 2/5/6/7 ever reached the pane).
	d := newDetailPane()
	d.id_ = "m1"
	for _, n := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		if handled, _ := d.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(n)}); handled {
			t.Fatalf("pane handled number key %q; want it to fall through", n)
		}
	}
}

func TestDashboardListAgentHandoffPushesAgentScreen(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{{ID: "m1", Title: "Planning"}}

	_, cmd := m.handleKey(testScreenCtx(), tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("`a` produced no command; want a push to the agent screen")
	}
	push, ok := cmd().(pushScreenMsg)
	if !ok || push.ID != sAgent || push.Param != "m1" {
		t.Fatalf("got %#v; want pushScreenMsg{ID: sAgent, Param: m1}", cmd())
	}
}

func dashboardWithSearchMatches() *dashboardScreen {
	m := newDashboardScreen().(*dashboardScreen)
	m.inputFocus = true
	m.input.Focus()
	m.input.SetValue("may")
	m.query = "may"
	m.lastQuery = "may"
	m.matched = []notoapi.MeetingHits{
		{
			MeetingID:    "m1",
			MeetingTitle: "May planning",
			TopHits: []notoapi.SearchHit{
				{MeetingID: "m1", SegmentID: "s1", ResultType: "transcript"},
				{MeetingID: "m1", SegmentID: "s2", ResultType: "transcript"},
			},
		},
		{
			MeetingID:    "m2",
			MeetingTitle: "May follow-up",
			TopHits: []notoapi.SearchHit{
				{MeetingID: "m2", SegmentID: "s3", ResultType: "transcript"},
			},
		},
	}
	return m
}

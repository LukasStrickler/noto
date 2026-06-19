package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// peopleRoot builds a root model on the People screen with n profiles loaded
// and the first one selected (its meetings hydrated, so the detail's meeting
// rows + action chips render).
func peopleRoot(t *testing.T, n int) (*rootModel, *peopleScreen) {
	t.Helper()
	p := newPeopleScreen().(*peopleScreen)
	p.loading = false
	for i := 0; i < n; i++ {
		p.profiles = append(p.profiles, notoapi.SpeakerProfile{
			ID: fmt.Sprintf("p%d", i), DisplayName: fmt.Sprintf("Person%d", i), MeetingCount: 1,
		})
	}
	if n > 0 {
		p.detail = p.profiles[0]
		p.detailID = "p0"
		p.detail.Meetings = []notoapi.ProfileMeetingRef{
			{MeetingID: "m9", Title: "Kickoff", CreatedAt: time.Now()},
		}
	}
	r := testRootModel()
	r.stack = []screen{p}
	r.width, r.height = 140, 30
	return r, p
}

// Clicking a directory row selects that person and loads their detail — the
// pointer twin of Up/Down.
func TestPeopleClickRowSelects(t *testing.T) {
	r, p := peopleRoot(t, 3)

	clickRoot(t, r, "Person2")

	if p.cursor != 2 {
		t.Errorf("cursor = %d after clicking Person2; want 2", p.cursor)
	}
	if p.detailID != "p2" {
		t.Errorf("detail bound to %q; want p2", p.detailID)
	}
}

// Clicking a meeting row in the detail focuses the meetings list on it and
// jumps — the click twin of Tab-into-meetings + Enter.
func TestPeopleClickMeetingFocuses(t *testing.T) {
	r, p := peopleRoot(t, 2)

	clickRoot(t, r, "Kickoff")

	if !p.focusMeetings {
		t.Errorf("clicking a meeting row did not focus the meetings list")
	}
	if p.meetingCur != 0 {
		t.Errorf("meetingCur = %d; want 0", p.meetingCur)
	}
}

// Clicking the "meetings" action chip replays Tab and hands focus to the
// meetings list — the chip and the key are one action.
func TestPeopleClickActionChip(t *testing.T) {
	r, p := peopleRoot(t, 2)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "meetings")

	r2, cmd := clickAt(t, r, x, y)
	dispatch(t, r2, cmd)

	if !p.focusMeetings {
		t.Errorf("clicking the meetings chip did not focus the meetings list")
	}
}

// Hovering a directory row paints it.
func TestPeopleHoverRowPaints(t *testing.T) {
	r, _ := peopleRoot(t, 3)
	x, y := cellOf(t, ansi.Strip(r.View().Content), "Person1")
	if strings.Contains(rawLineWith(t, r.View().Content, "Person1"), overlayBG) {
		t.Fatal("precondition: Person1 already painted before hover")
	}

	r.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})

	if got := rawLineWith(t, r.View().Content, "Person1"); !strings.Contains(got, overlayBG) {
		t.Errorf("hovering Person1 did not paint the row; line=%q", got)
	}
}

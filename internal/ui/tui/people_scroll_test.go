package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

func manyProfiles(n int) []notoapi.SpeakerProfile {
	out := make([]notoapi.SpeakerProfile, n)
	for i := range out {
		out[i] = notoapi.SpeakerProfile{ID: fmt.Sprintf("p%d", i), DisplayName: fmt.Sprintf("Person %02d", i), MeetingCount: 1}
	}
	return out
}

func peopleScrollCtx() screenCtx {
	return screenCtx{ctx: context.Background(), keys: keys.New(), styles: theme.NewStyles(), width: 140, height: 24}
}

// The directory used to render EVERY row with no windowing, so a selection past
// the fold was unreachable and the hint chips were clipped. With the shared
// rowList it scrolls: selecting a person well past the viewport must keep that
// person's row on screen (cursor-follow), and a scrollbar must appear.
func TestPeopleDirectoryScrollsToSelection(t *testing.T) {
	p := newPeopleScreen().(*peopleScreen)
	p.loading = false
	p.profiles = manyProfiles(60)
	ctx := peopleScrollCtx()

	// Select a person far past the first window, then follow into it.
	p.cursor = 45
	p.followCursor(ctx)

	frame := ansi.Strip(p.view(ctx))
	if !strings.Contains(frame, "Person 45") {
		t.Fatalf("selected person past the fold is not visible:\n%s", frame)
	}
	if !strings.Contains(frame, "Person 00") {
		// sanity: nothing forces row 0 off — but with cursor at 45 it SHOULD be gone
	} else {
		t.Fatal("row 0 should have scrolled out of view when the cursor is at 45")
	}
	// The hint chips stay pinned and reachable (not clipped off the bottom).
	if !strings.Contains(frame, "select") {
		t.Fatal("hint chips were clipped — the list isn't leaving room for them")
	}
}

// A wheel notch over a directory row pans the list without moving the selection,
// exactly like the meeting list.
func TestPeopleWheelPansWithoutSelecting(t *testing.T) {
	p := newPeopleScreen().(*peopleScreen)
	p.loading = false
	p.profiles = manyProfiles(60)
	ctx := peopleScrollCtx()

	beforeCursor := p.cursor
	p.handleWheel(ctx, mouseWheelMsg{over: "people:row:0", up: false})
	if p.cursor != beforeCursor {
		t.Fatalf("wheel moved the selection (cursor %d→%d); it should only pan", beforeCursor, p.cursor)
	}
	if p.listScroll == 0 {
		t.Fatal("wheel down did not pan the list")
	}
	// Over a non-list region the wheel is ignored.
	p.listScroll = 0
	p.handleWheel(ctx, mouseWheelMsg{over: "dash:pane", up: false})
	if p.listScroll != 0 {
		t.Fatal("wheel over a foreign region should not pan the directory")
	}
}

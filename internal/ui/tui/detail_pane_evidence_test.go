package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// evidenceFixturePane: a meeting with three timed segments and a couple of
// decisions/actions that cite them by segment id — the data the items tabs turn
// into human timestamps + jump targets.
func evidenceFixturePane() *detailPane {
	d := newDetailPane()
	d.id_ = "m1"
	d.transcript = notoapi.Transcript{
		MeetingID: "m1",
		Speakers:  []notoapi.Speaker{{ID: "A", Label: "Speaker A"}},
		Segments: []notoapi.TranscriptSegment{
			{ID: "seg_001", SpeakerID: "A", StartSec: 8, EndSec: 22, Text: "ship search v2"},
			{ID: "seg_002", SpeakerID: "A", StartSec: 36, EndSec: 48, Text: "defer mobile"},
			{ID: "seg_003", SpeakerID: "A", StartSec: 62, EndSec: 74, Text: "migration risk"},
		},
	}
	d.speakers = computeSpeakerStats(d.transcript)
	d.summary = notoapi.Summary{
		MeetingID: "m1",
		Decisions: []notoapi.SummaryItem{
			{Text: "Ship search v2", SegmentRefs: []string{"seg_001", "seg_002"}},
			{Text: "Defer mobile beta", SegmentRefs: []string{"seg_003"}},
		},
	}
	return d
}

// The items tabs must show WHEN a decision happened (a timestamp), never the
// internal "seg_xxx" id, and advertise that Enter jumps to the transcript.
func TestRenderItemsListShowsTimestampsNotSegmentIDs(t *testing.T) {
	d := evidenceFixturePane()
	d.tab = tabDecisions
	ctx := testScreenCtx()
	got := ansi.Strip(d.renderItemsList(ctx, "Decisions", d.summary.Decisions, 100, 40, ctx.styles.Decision))

	if strings.Contains(got, "seg_") {
		t.Errorf("items list leaked a segment id:\n%s", got)
	}
	for _, want := range []string{"00:08", "00:36", "01:02", "jump to transcript"} {
		if !strings.Contains(got, want) {
			t.Errorf("items list missing %q:\n%s", want, got)
		}
	}
}

// Regression: a speaker whose Name falls back to a bare id ("A") must not let
// renderPeople match the "A" inside the resolved "Speaker A" and double it.
func TestRenderPeopleDoesNotDoubleBareIDNames(t *testing.T) {
	d := evidenceFixturePane() // segments carry no Speaker label, so Name == id "A"
	s := testScreenCtx().styles
	got := ansi.Strip(d.renderPeople(s, "owner: @S1"))
	if got != "owner: Speaker A" {
		t.Errorf("renderPeople = %q, want %q", got, "owner: Speaker A")
	}
}

// The transcript leads each line with the [MM:SS] timestamp + speaker only —
// the internal "seg_xxx" id is meaningless to a reader and leaked a stray
// underscore, so it must not render.
func TestTranscriptHidesSegmentIDs(t *testing.T) {
	d := evidenceFixturePane()
	d.tab = tabTranscript
	ctx := testScreenCtx()
	got := ansi.Strip(d.renderTranscript(ctx, 100, 40))
	if strings.Contains(got, "seg_") {
		t.Errorf("transcript leaked an internal segment id:\n%s", got)
	}
	if !strings.Contains(got, "00:08") {
		t.Errorf("transcript missing [MM:SS] timestamp lead:\n%s", got)
	}
}

// Enter on a selected item lands on the exact cited segment in the transcript:
// the tab switches, the scroll + focus point at that segment so it highlights.
func TestEnterJumpsFromItemToCitedSegment(t *testing.T) {
	d := evidenceFixturePane()
	d.tab = tabDecisions
	ctx := testScreenCtx()

	// Move to the second decision (cites seg_003 @ 62s = index 2).
	if handled, _ := d.handleKey(ctx, keyMsg("down")); !handled || d.itemCur != 1 {
		t.Fatalf("down: handled=%v itemCur=%d, want true/1", handled, d.itemCur)
	}
	if handled, _ := d.handleKey(ctx, keyMsg("enter")); !handled {
		t.Fatal("enter on item was not handled")
	}
	if d.tab != tabTranscript {
		t.Errorf("tab = %v, want transcript", d.tab)
	}
	if d.focusSeg != 2 || d.transcriptScroll != 2 {
		t.Errorf("focusSeg=%d transcriptScroll=%d, want 2/2", d.focusSeg, d.transcriptScroll)
	}
}

package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// hasScrollThumb reports whether a RAW (un-stripped) frame contains the
// scrollbar thumb. The thumb is a space painted with the ScrollThumb BACKGROUND
// (a background fill is gapless in every terminal, unlike a foreground █ which
// shows inter-line gaps in macOS Terminal.app), so it is INVISIBLE to ansi.Strip
// — match its opening SGR in the raw frame instead. Background(Subtle) is unique
// to the thumb, so the escape is an unambiguous marker.
func hasScrollThumb(s theme.Styles, rawFrame string) bool {
	open, _, found := strings.Cut(s.ScrollThumb.Render(" "), " ")
	return found && open != "" && strings.Contains(rawFrame, open)
}

func manyMeetings(n int) []notoapi.Meeting {
	out := make([]notoapi.Meeting, n)
	base := time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC)
	for i := range out {
		out[i] = notoapi.Meeting{
			ID:        fmt.Sprintf("m%02d", i),
			Title:     fmt.Sprintf("Mtg-%02d", i),
			Status:    notoapi.StatusSummarized,
			CreatedAt: base,
		}
	}
	return out
}

// Arrow-key navigation must SCROLL the view to keep the selected row on screen
// (followCursor), instead of rendering from the top and clipping the selection
// out of view (the reported bug). A scrollbar thumb appears once the rows
// overflow the panel.
func TestDashboardArrowNavFollowsCursor(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = manyMeetings(40)
	ctx := testScreenCtx()

	// Walk the cursor to the very end with the Down key: its row must be visible
	// and the first row scrolled off.
	for i := 0; i < 39; i++ {
		m.handleKey(ctx, keyMsg("down"))
	}
	raw := m.view(ctx)
	out := ansi.Strip(raw)
	if !strings.Contains(out, "Mtg-39") {
		t.Error("selected row Mtg-39 scrolled out of view; the view did not follow the cursor")
	}
	if strings.Contains(out, "Mtg-00") {
		t.Error("first row should have scrolled off when the cursor reached the end")
	}
	if !hasScrollThumb(ctx.styles, raw) {
		t.Error("an overflowing list must draw a scrollbar thumb")
	}

	// Walk back to the top: the first row must be visible again.
	for i := 0; i < 39; i++ {
		m.handleKey(ctx, keyMsg("up"))
	}
	out = ansi.Strip(m.view(ctx))
	if !strings.Contains(out, "Mtg-00") {
		t.Error("cursor back at top: the first row must be visible")
	}
}

// A short list neither windows nor draws a scrollbar — the gutter is reserved
// only when content overflows.
func TestDashboardShortListHasNoScrollbar(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = manyMeetings(2)
	ctx := testScreenCtx()
	if hasScrollThumb(ctx.styles, m.view(ctx)) {
		t.Error("a list that fits must not draw a scrollbar")
	}
}

// The wheel scrolls the list VIEW only — it pans listScroll and must NOT move
// the selection (scroll ≠ select; clicking is what changes focus). Over the pane
// it scrolls the pane instead.
func TestDashboardWheelScrollsViewNotCursor(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = manyMeetings(40)
	ctx := testScreenCtx()

	m.handleWheel(ctx, mouseWheelMsg{over: "dash:list", up: false})
	if m.cursor != 0 {
		t.Errorf("wheel must not move the selection; cursor = %d, want 0", m.cursor)
	}
	if m.listScroll == 0 {
		t.Error("wheel down over the list should have scrolled the view (listScroll still 0)")
	}
	down := m.listScroll

	m.handleWheel(ctx, mouseWheelMsg{over: "dash:row:5", up: false})
	if m.listScroll <= down {
		t.Errorf("wheel down over a row should keep scrolling; listScroll %d not > %d", m.listScroll, down)
	}
	if m.cursor != 0 {
		t.Errorf("wheel over a row must not select it; cursor = %d, want 0", m.cursor)
	}

	twoDown := m.listScroll
	m.handleWheel(ctx, mouseWheelMsg{over: "dash:list", up: true})
	if m.listScroll >= twoDown {
		t.Errorf("wheel up should scroll back; listScroll %d not < %d", m.listScroll, twoDown)
	}
	if m.cursor != 0 {
		t.Errorf("wheel up must not move the selection; cursor = %d, want 0", m.cursor)
	}
}

func TestDashboardWheelOverPaneScrollsPaneNotCursor(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = manyMeetings(40)
	segs := make([]notoapi.TranscriptSegment, 30)
	for i := range segs {
		segs[i] = notoapi.TranscriptSegment{ID: fmt.Sprintf("s%d", i), Text: "line"}
	}
	m.pane.transcript = notoapi.Transcript{Segments: segs}
	m.pane.tab = tabTranscript
	ctx := testScreenCtx()

	startCursor := m.cursor
	if _, _ = m.handleWheel(ctx, mouseWheelMsg{over: "dash:pane", up: false}); m.pane.transcriptScroll != 1 {
		t.Errorf("wheel down over the pane: transcriptScroll = %d; want 1", m.pane.transcriptScroll)
	}
	if m.cursor != startCursor {
		t.Errorf("wheel over the pane must not move the list cursor (was %d, now %d)", startCursor, m.cursor)
	}
}

// The sidebar list row's counter strip spells out the counts while they fit, but
// drops to the compact icon+count form ("◆2 ▸3 …") on a narrow column instead of
// spilling past the right edge — the reported "broken in the sidebar" bug. The
// detail pane keeps the full words; only the narrow sidebar compacts.
func TestSidebarCounterStripCompactsWhenNarrow(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	s := testScreenCtx().styles
	mt := notoapi.Meeting{
		Title: "Plan", DecisionCount: 2, ActionCount: 3, RiskCount: 1, QuestionCount: 2,
	}

	wide := ansi.Strip(m.renderListRowDefault(s, mt, 80, countsFull)[1])
	if !strings.Contains(wide, "decisions") {
		t.Errorf("a wide row should keep the spelled-out counts; got %q", wide)
	}

	narrow := ansi.Strip(m.renderListRowDefault(s, mt, 24, countsIconNum)[1])
	if strings.Contains(narrow, "decision") {
		t.Errorf("a narrow row must compact the counts, not spell them; got %q", narrow)
	}
	if !strings.Contains(narrow, "◆2") || !strings.Contains(narrow, "?2") {
		t.Errorf("a narrow row should use the icon+count form (◆2 …); got %q", narrow)
	}
}

// The compact decision is list-wide: if one busy/flagged meeting forces compact,
// EVERY row compacts, so the break point is identical across rows (a flagged row
// doesn't compact a step earlier than a plain one). Guards "consistent across
// all" — no ragged mix of spelled and iconified rows at the same width.
func TestSidebarCounterCompactionIsListWide(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{
		// A busy + flagged meeting whose full counts overflow a tight column.
		{ID: "m0", Title: "Busy", DecisionCount: 2, ActionCount: 3, RiskCount: 1, QuestionCount: 2,
			Identity: &notoapi.SpeakerIdentitySummary{Unset: 2}},
		// A quiet meeting whose lone count would fit full on its own.
		{ID: "m1", Title: "Quiet", DecisionCount: 1},
	}
	s := testScreenCtx().styles
	// A column wide enough for "◆ 1 decision" alone but not the busy row's full
	// strip — the quiet row must STILL compact, matching the busy row.
	width := 30
	if m.listCountsTier(s, width) == countsFull {
		t.Fatal("precondition: the busy row should force list-wide compaction at this width")
	}
	tier := m.listCountsTier(s, width)
	quiet := ansi.Strip(m.renderListRowDefault(s, m.all[1], width, tier)[1])
	if strings.Contains(quiet, "decision") {
		t.Errorf("quiet row should compact in lock-step with the busy row, not spell out; got %q", quiet)
	}
}

// With a scrollbar present the row content must be clipped to leave the bar its
// column — otherwise gutter + content + bar runs one cell past the panel and the
// row WRAPS onto a new line (the "breaks too late" overflow). Guard that every
// frame line is exactly the terminal width across sizes, with a long overflowing
// list and busy counter strips.
func TestListNoWrapWithScrollbar(t *testing.T) {
	build := func() *dashboardScreen {
		m := newDashboardScreen().(*dashboardScreen)
		m.loading = false
		for i := 0; i < 30; i++ {
			m.all = append(m.all, notoapi.Meeting{
				ID:            fmt.Sprintf("m%d", i),
				Title:         fmt.Sprintf("Meeting %d with a fairly long title", i),
				Status:        notoapi.StatusSummarized,
				CreatedAt:     time.Date(2026, 5, 20, 9, 0, 0, 0, time.UTC),
				DecisionCount: 2, ActionCount: 3, RiskCount: 1, QuestionCount: 2,
				Identity: &notoapi.SpeakerIdentitySummary{Resolved: 3},
			})
		}
		m.cursor = 15
		return m
	}
	for _, sz := range []struct{ w, h int }{{120, 30}, {100, 26}, {80, 24}, {66, 20}, {54, 18}} {
		m := build()
		ctx := screenCtxAt(sz.w, sz.h-3)
		for _, ln := range strings.Split(m.view(ctx), "\n") {
			if got := ansi.StringWidth(ln); got != sz.w {
				t.Errorf("size %dx%d: a frame line is %d cells wide, want %d (row wrapped/overflowed): %q",
					sz.w, sz.h, got, sz.w, ansi.Strip(ln))
				break
			}
		}
	}
}

// Singular and plural counter labels must render to the SAME width so the strip's
// columns line up at a constant distance regardless of the counts ("1 question "
// aligns with "2 questions"). countWord right-pads the singular form.
func TestCounterStripConstantWidth(t *testing.T) {
	s := testScreenCtx().styles
	singular := ansi.StringWidth(ansi.Strip(renderCounterStrip(s, 1, 1, 1, 1)))
	plural := ansi.StringWidth(ansi.Strip(renderCounterStrip(s, 2, 2, 2, 2)))
	if singular != plural {
		t.Errorf("counter strip width differs by count: singular=%d plural=%d; should be equal", singular, plural)
	}
}

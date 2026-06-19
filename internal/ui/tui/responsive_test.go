package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// shortenTo drops trailing runes with NO ellipsis (it's a compact short form),
// and leaves already-fitting strings untouched.
func TestShortenTo(t *testing.T) {
	if got := shortenTo("Dashboard", 4); got != "Dash" {
		t.Errorf("shortenTo(Dashboard,4) = %q, want Dash", got)
	}
	if got := shortenTo("ab", 5); got != "ab" {
		t.Errorf("shortenTo(ab,5) = %q, want ab", got)
	}
	if got := shortenTo("x", 0); got != "" {
		t.Errorf("shortenTo(x,0) = %q, want empty", got)
	}
}

// wrapBody guarantees no rendered line exceeds the width — even an unbreakable
// word longer than the column is hard-broken rather than spilling past the edge.
func TestWrapBodyNeverOverflows(t *testing.T) {
	content := "antidisestablishmentarianism and a few short words plus supercalifragilisticexpialidocious"
	for _, w := range []int{6, 10, 20, 40} {
		for _, ln := range wrapBody(content, w) {
			if got := ansi.StringWidth(ln); got > w {
				t.Errorf("width %d: wrapped line is %d cells wide: %q", w, got, ln)
			}
		}
	}
}

// The nav strip never spills past its budget across the whole range of usable
// terminal widths: as space shrinks it steps full → mid (shortened) → digit, and
// the rendered strip stays within the available width.
func TestNavStripNeverOverflows(t *testing.T) {
	m := testRootModel()
	for w := 40; w <= 200; w++ {
		m.width = w
		if got := ansi.StringWidth(ansi.Strip(m.renderHeader())); got > w {
			t.Errorf("width %d: nav header is %d cells wide (overflow)", w, got)
		}
	}
}

// At an intermediate width the strip uses the MID tier: inactive labels are
// shortened (a prefix of the full word), neither fully spelled nor reduced to a
// bare digit — the "long / mid / one letter" middle step.
func TestNavStripMidTierShortensLabels(t *testing.T) {
	m := testRootModel()
	// Wide enough that full overflows but a shortened form still fits.
	m.width = 52
	header := ansi.Strip(m.renderHeader())
	if strings.Contains(header, "people") {
		t.Fatalf("expected the mid tier to shorten 'people'; got full label:\n%s", header)
	}
	if !strings.Contains(header, "peo") {
		t.Fatalf("expected a shortened 'people' (prefix) at the mid tier:\n%s", header)
	}
}

// The sidebar counter strip has a THIRD, narrowest tier: when even "◆2 ▸3" can't
// fit, the icons alone carry the meaning (◆ ▸ ⚠ ?) with the counts dropped.
func TestCounterStripIconOnlyTier(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	s := testScreenCtx().styles
	mt := notoapi.Meeting{DecisionCount: 2, ActionCount: 3, RiskCount: 1, QuestionCount: 2}
	row := ansi.Strip(m.renderListRowDefault(s, mt, 8, countsIcon)[1])
	if strings.ContainsAny(row, "0123456789") {
		t.Errorf("icon-only tier must drop the counts; got %q", row)
	}
	for _, glyph := range []string{"◆", "▸", "⚠", "?"} {
		if !strings.Contains(row, glyph) {
			t.Errorf("icon-only tier missing category glyph %q; got %q", glyph, row)
		}
	}
}

// listCountsTier picks the narrowest (icon-only) tier list-wide when a busy row's
// icon+count form still overflows a very tight column.
func TestListCountsTierFallsToIcons(t *testing.T) {
	m := newDashboardScreen().(*dashboardScreen)
	m.loading = false
	m.all = []notoapi.Meeting{{
		ID: "m0", Title: "Busy", DecisionCount: 2, ActionCount: 3, RiskCount: 1, QuestionCount: 2,
	}}
	s := testScreenCtx().styles
	if got := m.listCountsTier(s, 4); got != countsIcon {
		t.Errorf("listCountsTier at a tiny width = %d, want countsIcon (%d)", got, countsIcon)
	}
}

// The summary tab free-scrolls: a body taller than the viewport draws the shared
// scrollbar, exposes a positive bodyMax, and scrolling changes the visible window
// — all without any line overflowing the width.
func TestDetailSummaryScrollsAndWraps(t *testing.T) {
	d := &detailPane{id_: "m1"}
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("Summary line with enough words to wrap on a narrow pane column.\n")
	}
	d.summary = notoapi.Summary{MeetingID: "m1", Markdown: sb.String()}
	ctx := testScreenCtx()
	const width, height = 36, 6

	first := d.renderSummary(ctx, width, height)
	if d.bodyMax <= 0 {
		t.Fatalf("a long summary should expose a positive bodyMax; got %d", d.bodyMax)
	}
	if !hasScrollThumb(ctx.styles, first) {
		t.Errorf("a scrolling summary should draw the scrollbar thumb")
	}
	for _, ln := range strings.Split(first, "\n") {
		if got := ansi.StringWidth(ln); got > width {
			t.Errorf("summary line overflowed: %d > %d: %q", got, width, ansi.Strip(ln))
		}
	}

	d.bodyScroll = d.bodyMax
	last := d.renderSummary(ctx, width, height)
	if first == last {
		t.Errorf("scrolling to bodyMax should change the visible window")
	}
}

// Every detail-tab body flows through the shared paneBody, so the unification
// holds across ALL of them at once: when content is taller/wider than the
// viewport, each tab WRAPS (no rendered line exceeds the width — nothing spills
// past the right edge) and SCROLLS (a scrollbar thumb appears — content is
// reachable, never silently hidden past the fold). This is the regression guard
// for "summary, actions … transcript, speakers all render inside the pane".
func TestAllTabBodiesWrapAndScroll(t *testing.T) {
	d := &detailPane{id_: "m1"}

	segs := make([]notoapi.TranscriptSegment, 40)
	for i := range segs {
		segs[i] = notoapi.TranscriptSegment{
			ID: fmt.Sprintf("s%d", i), SpeakerID: fmt.Sprintf("S%d", i%4),
			StartSec: float64(i * 10), EndSec: float64(i*10 + 9),
			Text: "A fairly long transcript line that should wrap across the narrow detail pane column several times over.",
		}
	}
	d.transcript = notoapi.Transcript{Segments: segs}
	d.speakers = computeSpeakerStats(d.transcript)

	items := make([]notoapi.SummaryItem, 20)
	actions := make([]notoapi.ActionItem, 20)
	for i := range items {
		items[i] = notoapi.SummaryItem{Text: fmt.Sprintf("Item %d: a long decision/action/risk/question that wraps on a narrow pane column.", i)}
		actions[i] = notoapi.ActionItem{Text: items[i].Text}
	}
	var md strings.Builder
	for i := 0; i < 40; i++ {
		md.WriteString("Summary paragraph with enough words to wrap on the narrow pane column repeatedly.\n")
	}
	d.summary = notoapi.Summary{
		MeetingID: "m1", Markdown: md.String(),
		Decisions: items, Risks: items, OpenQuestions: items, ActionItems: actions,
	}

	ctx := testScreenCtx()
	const width, height = 40, 8
	for _, tc := range []struct {
		name string
		tab  detailTab
	}{
		{"summary", tabSummary}, {"actions", tabActions}, {"decisions", tabDecisions},
		{"risks", tabRisks}, {"questions", tabQuestions}, {"transcript", tabTranscript},
		{"speakers", tabSpeakers},
	} {
		d.tab = tc.tab
		raw := d.renderTabBody(ctx, width, height)
		for _, ln := range strings.Split(raw, "\n") {
			if w := ansi.StringWidth(ln); w > width {
				t.Errorf("%s tab: line overflows width %d (%d cells): %q", tc.name, width, w, ansi.Strip(ln))
			}
		}
		if !hasScrollThumb(ctx.styles, raw) {
			t.Errorf("%s tab: overflowing content must draw a scrollbar (else it's hidden past the fold)", tc.name)
		}
	}
}

// wrapEntries greedily packs entries into rows no wider than width, keeps each
// entry intact, and wraps to as many rows as the roster needs — the measured
// people-wrap that replaces the old fixed 3-per-row packing.
func TestWrapEntries(t *testing.T) {
	entries := []string{"Alice 01:00", "Bob 00:45", "Carol 00:30", "Dan 00:15"}

	// Wide: everything fits one row.
	if rows := wrapEntries(entries, "   ", 80); len(rows) != 1 {
		t.Errorf("width 80: got %d rows, want 1: %v", len(rows), rows)
	}
	// Narrow: wraps to several rows, none over width, every entry preserved.
	const narrow = 24
	rows := wrapEntries(entries, "   ", narrow)
	if len(rows) < 2 {
		t.Errorf("width %d: got %d rows, want >1: %v", narrow, len(rows), rows)
	}
	joined := strings.Join(rows, " ")
	for _, e := range entries {
		if !strings.Contains(joined, e) {
			t.Errorf("entry %q dropped during wrap: %v", e, rows)
		}
	}
	for _, r := range rows {
		if w := ansi.StringWidth(r); w > narrow {
			t.Errorf("row overflows width %d (%d cells): %q", narrow, w, r)
		}
	}
	// Empty input → no rows (caller emits its own placeholder).
	if rows := wrapEntries(nil, "   ", 40); rows != nil {
		t.Errorf("empty entries should yield nil, got %v", rows)
	}
}

// renderHeader leads with ONE full-width timeline line, then the people roster,
// and no line spills past the pane width at any size.
func TestDetailHeaderTimelineFullWidthNoOverflow(t *testing.T) {
	d := evidenceFixturePane() // 3 segments, one speaker "A"
	s := testScreenCtx().styles
	for _, w := range []int{30, 50, 88} {
		header := d.renderHeader(s, w)
		lines := strings.Split(header, "\n")
		// First line is the timeline: full of the bar glyph, spanning ~the width.
		if !strings.Contains(lines[0], "█") {
			t.Errorf("width %d: first header line is not the timeline: %q", w, ansi.Strip(lines[0]))
		}
		for _, ln := range lines {
			if got := ansi.StringWidth(ln); got > w {
				t.Errorf("width %d: header line overflows (%d cells): %q", w, got, ansi.Strip(ln))
			}
		}
	}
}

// headerTitle / headerMeta drive the panel chrome: "Details: <name>" on the left,
// and a flag on the right ONLY for outstanding work (never a "done" badge).
func TestDetailHeaderTitleAndMeta(t *testing.T) {
	s := testScreenCtx().styles

	empty := &detailPane{}
	if got := empty.headerTitle(); got != "Details" {
		t.Errorf("unbound headerTitle = %q, want %q", got, "Details")
	}
	if got := empty.headerMeta(s); got != "" {
		t.Errorf("unbound headerMeta = %q, want empty", got)
	}

	d := &detailPane{id_: "m1"}
	d.meeting = notoapi.Meeting{Title: "Acme Q3 Planning", Status: notoapi.StatusRecorded}
	if got := d.headerTitle(); got != "Details: Acme Q3 Planning" {
		t.Errorf("headerTitle = %q", got)
	}
	if got := ansi.Strip(d.headerMeta(s)); !strings.Contains(got, "untranscribed") {
		t.Errorf("recorded meeting should flag 'untranscribed'; got %q", got)
	}

	d.meeting.Title = ""
	if got := d.headerTitle(); got != "Details: Untitled meeting" {
		t.Errorf("blank-title headerTitle = %q", got)
	}

	// Total time: derived from the last segment when the meeting record has none.
	d.transcript = notoapi.Transcript{Segments: []notoapi.TranscriptSegment{{EndSec: 174}}}
	if got := ansi.Strip(d.headerMeta(s)); !strings.Contains(got, "02:54") {
		t.Errorf("headerMeta should show total time 02:54; got %q", got)
	}

	// A finished meeting carries NO status flag (flag only outstanding work).
	d.meeting.Status = notoapi.StatusSummarized
	if got := ansi.Strip(d.headerMeta(s)); strings.ContainsAny(got, "●✗") || strings.Contains(got, "untranscribed") {
		t.Errorf("summarized meeting should carry no status flag; got %q", got)
	}
}

// The detail pane's minimum content width is DERIVED from its full tab bar, so
// the whole strip — every label spelled out, plus the worst-case attention badge
// — always fits at the minimum width without abbreviation or clipping. This locks
// the derivation: rename/add a tab and minContentW must grow with it.
func TestMinContentWidthFitsFullTabBar(t *testing.T) {
	ctx := testScreenCtx()
	innerW := minContentW - panelChromeCells

	// Worst case: the Speakers tab carries an attention badge (" ⚑N").
	d := &detailPane{id_: "m1", tab: tabSummary}
	pending := "pending"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: pending},
	}
	if d.tabAttention(tabSpeakers) == 0 {
		t.Fatal("precondition: speakers tab should carry an attention badge")
	}

	bar := ansi.Strip(d.renderTabBar(ctx, innerW, 0, 0, nil))
	if w := ansi.StringWidth(bar); w > innerW {
		t.Fatalf("full tab bar (%d cells, with badge) overflows minContentW inner (%d): %q", w, innerW, bar)
	}
	// Every tab spelled out in full at the minimum width — no abbreviation.
	for _, full := range []string{"summary", "actions", "decisions", "risks", "questions", "transcript", "speakers"} {
		if !strings.Contains(bar, full) {
			t.Errorf("tab %q not rendered in full at minContentW: %q", full, bar)
		}
	}
}

package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/scroll"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// view renders the full pane body: fixed header (3 lines + rule), tab
// strip (1 line + rule), and the active tab body. The host wraps the
// returned string in its own layout.Panel.
// view renders the pane. originX/originY are the absolute screen coordinates of
// the pane body's first cell (the host computes them from its panel via
// BodyOffset) so the tab bar can register clickable regions; onTab is the
// host's per-tab click action (nil disables tab clicks, e.g. while a sub-mode
// owns input).
func (d *detailPane) view(ctx screenCtx, width, height, originX, originY int, onTab func(detailTab) clickAction) string {
	s := ctx.styles
	if d.id_ == "" {
		return s.Muted.Render("select a meeting to preview")
	}
	if d.err != nil {
		return s.BadgeDanger.Render(d.err.Error())
	}
	innerW := width
	if innerW < 20 {
		innerW = 20
	}

	header := d.renderHeader(s, innerW)
	// Layout is Join([header, rule, tabBar, "", body]); the tab bar sits one
	// line below the header lines (past the rule).
	tabBarY := originY + renderedLineCount(header) + 1
	tabBar := d.renderTabBar(ctx, innerW, originX, tabBarY, onTab)
	headerLines := renderedLineCount(header) + renderedLineCount(tabBar) + 2 // rule under header + blank
	bodyH := height - headerLines
	if bodyH < 3 {
		bodyH = 3
	}
	body := d.renderTabBody(ctx, innerW, bodyH)
	rule := s.Muted.Render(strings.Repeat("─", innerW))
	return strings.Join([]string{header, rule, tabBar, "", body}, "\n")
}

// headerTitle is the detail panel's left title — "Details: <name>" (or a bare
// "Details" when nothing is bound). The meeting title used to live in the pane
// body; it now rides in the panel chrome so the body is pure timeline + people.
// The host fits the name to leave room for headerMeta on the right.
func (d *detailPane) headerTitle() string {
	if d.id_ == "" {
		return "Details"
	}
	name := d.meeting.Title
	if name == "" {
		name = "Untitled meeting"
	}
	return "Details: " + name
}

// headerMeta is the detail panel's right segment: muted "date · time" plus a
// status flag that shows ONLY outstanding work (untranscribed / recording /
// failed) — a finished meeting carries no badge, mirroring the list. It's
// pre-styled and placed via Panel.Right so the flag keeps its colour (the Muted
// Subtitle wrap would otherwise clobber it).
func (d *detailPane) headerMeta(s theme.Styles) string {
	if d.id_ == "" {
		return ""
	}
	parts := []string{}
	if !d.meeting.CreatedAt.IsZero() {
		parts = append(parts, s.Muted.Render(d.meeting.CreatedAt.Format("Jan 02 · 15:04")))
	}
	if total := d.totalDurationSec(); total > 0 {
		parts = append(parts, s.Muted.Render(formatDuration(total)))
	}
	if flag := outstandingStatusBadge(s, d.meeting.Status); flag != "" {
		parts = append(parts, flag)
	}
	return strings.Join(parts, s.Muted.Render(" · "))
}

// totalDurationSec is the meeting's total length in seconds — the recorded
// duration when set, else derived from the last transcript segment's end (seeded
// meetings carry their duration on the transcript, not the meeting record). It's
// the "total time" the header advertises beside date · time.
func (d *detailPane) totalDurationSec() int {
	if d.meeting.DurationSeconds > 0 {
		return d.meeting.DurationSeconds
	}
	if n := len(d.transcript.Segments); n > 0 {
		return int(d.transcript.Segments[n-1].EndSec)
	}
	return 0
}

// renderHeader is the pane body's top strip: one FULL-WIDTH timeline row, then
// the people, each line wrapped to width so nothing spills past the pane edge.
// The meeting title/date/status now live in the panel chrome (headerTitle /
// headerMeta), so this is no longer a meta ribbon — just identity + roster.
func (d *detailPane) renderHeader(s theme.Styles, width int) string {
	timeline := clipLine(d.renderHeaderTimeline(s, width), width)
	people := strings.Split(d.renderAttendeeStrip(s, width), "\n")
	for i, line := range people {
		people[i] = clipLine(line, width)
	}
	return timeline + "\n\n" + strings.Join(people, "\n")
}

// renderHeaderTimeline draws ONE timeline across the whole pane width (all
// speakers, dominant-speaker-per-cell colouring) — replacing the old per-row
// timelines whose lengths jiggled with each legend. A stable single line renders
// even before segments load so the header height doesn't jump.
func (d *detailPane) renderHeaderTimeline(s theme.Styles, width int) string {
	if tl := d.renderInlineTimelineForSpeakers(s, width, d.speakers); tl != "" {
		return tl
	}
	if d.loadingTranscript {
		return s.Muted.Render("loading timeline…")
	}
	return s.Muted.Render(strings.Repeat("·", max(1, width)))
}

// renderAttendeeStrip lays out the people as "Name talktime" entries, greedily
// wrapped to width across as many rows as the roster needs (long names / many
// people push the rest down rather than spilling off the edge). No ● swatch —
// the full-width timeline above carries speaker colour/identity.
func (d *detailPane) renderAttendeeStrip(s theme.Styles, width int) string {
	sep := "   " // a 3-space gap groups "Name HH:MM" pairs without a dot rail
	if len(d.speakers) == 0 {
		// Fall back to the static attendee list while diarization is
		// still loading or when this meeting has no diarization at all.
		if len(d.transcript.Speakers) == 0 {
			if len(d.meeting.Attendees) > 0 {
				return strings.Join(wrapEntries(d.meeting.Attendees, sep, width), "\n")
			}
			return s.Muted.Render("Attendees: —")
		}
		names := make([]string, 0, len(d.transcript.Speakers))
		for i, sp := range d.transcript.Speakers {
			// Resolve even on this stats-not-ready path so a confirmed person shows
			// by name (and a guess as ~name?) the moment the transcript loads, the
			// same as everywhere else — not a raw label until talk stats arrive.
			name, tier := d.resolveSpeaker(sp.ID)
			names = append(names, styleSpeaker(s, name, i%6, tier))
		}
		return strings.Join(wrapEntries(names, sep, width), "\n")
	}
	return d.renderSpeakerOverviewRows(s, width)
}

// renderSpeakerOverviewRows is the people roster once talk-time stats exist:
// "Name talktime" per speaker, greedy-wrapped to width.
func (d *detailPane) renderSpeakerOverviewRows(s theme.Styles, width int) string {
	entries := make([]string, 0, len(d.speakers))
	for _, sp := range d.speakers {
		name, tier := d.resolveSpeaker(sp.ID)
		entries = append(entries, styleSpeaker(s, name, sp.colorIdx, tier)+" "+s.Muted.Render(formatDuration(int(sp.TalkSec))))
	}
	return strings.Join(wrapEntries(entries, "   ", width), "\n")
}

// wrapEntries greedily packs pre-styled entries into rows no wider than width,
// joining them with sep. Each entry stays intact on its row (an entry wider than
// width gets its own row, clipped only as a last resort). Measured with
// lipgloss.Width and pure, so the people strip never spills past the pane edge
// and grows to as many rows as the roster needs.
func wrapEntries(entries []string, sep string, width int) []string {
	if len(entries) == 0 {
		return nil
	}
	if width < 1 {
		width = 1
	}
	sepW := lipgloss.Width(sep)
	var rows []string
	var cur strings.Builder
	curW := 0
	for _, e := range entries {
		ew := lipgloss.Width(e)
		switch {
		case curW == 0:
			cur.WriteString(e)
			curW = ew
		case curW+sepW+ew <= width:
			cur.WriteString(sep)
			cur.WriteString(e)
			curW += sepW + ew
		default:
			rows = append(rows, cur.String())
			cur.Reset()
			cur.WriteString(e)
			curW = ew
		}
	}
	rows = append(rows, cur.String())
	for i, r := range rows {
		rows[i] = clipLine(r, width)
	}
	return rows
}

func (d *detailPane) renderInlineTimelineForSpeakers(s theme.Styles, width int, speakers []speakerStat) string {
	if width < 10 || len(d.transcript.Segments) == 0 || len(speakers) == 0 {
		return ""
	}
	end := d.transcript.Segments[len(d.transcript.Segments)-1].EndSec
	if end <= 0 {
		return ""
	}
	bucketSec := end / float64(width)
	if bucketSec <= 0 {
		return ""
	}
	buckets := make([][]float64, width)
	for i := range buckets {
		buckets[i] = make([]float64, len(speakers))
	}
	idxBySpeaker := map[string]int{}
	for i, sp := range speakers {
		idxBySpeaker[sp.ID] = i
	}
	for _, seg := range d.transcript.Segments {
		idx, ok := idxBySpeaker[seg.SpeakerID]
		if !ok {
			continue
		}
		start := int(seg.StartSec / bucketSec)
		stop := int(seg.EndSec / bucketSec)
		if stop >= width {
			stop = width - 1
		}
		if start < 0 {
			start = 0
		}
		for b := start; b <= stop && b < width; b++ {
			buckets[b][idx] += seg.EndSec - seg.StartSec
		}
	}
	var b strings.Builder
	for _, bk := range buckets {
		bestIdx, best := -1, 0.0
		for i, v := range bk {
			if v > best {
				best = v
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			b.WriteString(s.Muted.Render("·"))
			continue
		}
		c := speakerColorForIndex(s.T, speakers[bestIdx].colorIdx)
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("█"))
	}
	return b.String()
}

// replaceWordCaseInsensitive replaces case-insensitive occurrences of
// `needle` at word boundaries in `haystack`. Uses byte iteration so
// it's safe on ASCII names; longer Unicode names still work but the
// "word boundary" check is approximate (non-letter/digit byte).
func replaceWordCaseInsensitive(haystack, needle string, transform func(string) string) string {
	if needle == "" {
		return haystack
	}
	lowH := strings.ToLower(haystack)
	lowN := strings.ToLower(needle)
	var b strings.Builder
	i := 0
	for i < len(haystack) {
		j := strings.Index(lowH[i:], lowN)
		if j < 0 {
			b.WriteString(haystack[i:])
			break
		}
		start := i + j
		end := start + len(needle)
		if !isWordBoundary(lowH, start, end) {
			b.WriteString(haystack[i : start+1])
			i = start + 1
			continue
		}
		b.WriteString(haystack[i:start])
		b.WriteString(transform(haystack[start:end]))
		i = end
	}
	return b.String()
}

func isWordBoundary(s string, start, end int) bool {
	before := start == 0 || !isWordByte(s[start-1])
	after := end == len(s) || !isWordByte(s[end])
	return before && after
}

func isWordByte(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	}
	return false
}

// renderCounterStrip is the icon-prefixed counter line used in both
// the detail header and dashboard list rows. Zero-count categories
// drop out so the row stays compact.
// counterCat is one category in the work-counter strip: its glyph, its
// singular/plural words, and the accent style that colours it. The four
// categories — decisions green, actions blue, risks amber, questions indigo,
// matching the detail tabs — are defined ONCE here so the three strip tiers
// (full, compact, icons) can never disagree about a glyph, colour, or word. A
// new category or a glyph change is a single edit to this table.
type counterCat struct {
	glyph, one, many string
	style            func(theme.Styles) lipgloss.Style
}

var counterCats = [4]counterCat{
	{"◆", "decision", "decisions", func(s theme.Styles) lipgloss.Style { return s.Decision }},
	{"▸", "action", "actions", func(s theme.Styles) lipgloss.Style { return s.Action }},
	{"⚠", "risk", "risks", func(s theme.Styles) lipgloss.Style { return s.Risk }},
	{"?", "question", "questions", func(s theme.Styles) lipgloss.Style { return s.Question }},
}

// renderCounters is the one engine behind all three counter-strip tiers: it
// walks the shared category table, formats each non-zero count with `format`,
// colours it with the category's style, and joins the parts with `sep`. The
// tiers differ ONLY in those two parameters — see the three wrappers below.
func renderCounters(s theme.Styles, counts [4]int, sep string, format func(c counterCat, n int) string) string {
	parts := make([]string, 0, len(counterCats))
	for i, c := range counterCats {
		if counts[i] > 0 {
			parts = append(parts, c.style(s).Render(format(c, counts[i])))
		}
	}
	return strings.Join(parts, sep)
}

func renderCounterStrip(s theme.Styles, decisions, actions, risks, questions int) string {
	return renderCounters(s, [4]int{decisions, actions, risks, questions}, "   ",
		func(c counterCat, n int) string {
			return fmt.Sprintf("%s %d %s", c.glyph, n, countWord(n, c.one, c.many))
		})
}

// countWord is plural() that keeps a CONSTANT width: the singular is right-padded
// with a trailing space so "1 question " lines up with "2 questions" and the
// strip's columns stay at the same distance whatever the counts are. (The
// singular is always one shorter — it's the plural minus its "s" — so this is a
// single trailing space, but pad to the longer of the two to stay general.)
func countWord(n int, singular, plural string) string {
	word := plural
	if n == 1 {
		word = singular
	}
	if pad := len([]rune(plural)) - len([]rune(word)); pad > 0 {
		word += strings.Repeat(" ", pad)
	}
	return word
}

// renderCounterStripCompact is the narrow-pane form of the counter strip: the
// category ICON carries the meaning (◆ decision · ▸ action · ⚠ risk · ? question,
// same colours as the full strip and the detail tabs), so the word drops and only
// "◆2 ▸3 ⚠1 ?2" remains. The sidebar list is a 1/3-width column where the spelled
// counts ("◆ 2 decisions   ▸ 3 actions   …") blow past the right edge; this keeps
// the same glanceable information in a fraction of the width.
func renderCounterStripCompact(s theme.Styles, decisions, actions, risks, questions int) string {
	return renderCounters(s, [4]int{decisions, actions, risks, questions}, " ",
		func(c counterCat, n int) string { return fmt.Sprintf("%s%d", c.glyph, n) })
}

// renderCounterStripIcons is the narrowest counter form: the category ICONS
// alone (◆ ▸ ⚠ ?), colours intact, with the counts dropped entirely. It's the
// third sidebar tier below renderCounterStripCompact — when even "◆2 ▸3 ⚠1 ?2"
// can't fit the column, the glyphs still say WHICH kinds of work a meeting holds,
// just not how many. A category with a zero count drops out so the row stays a
// faithful (if count-less) inventory.
func renderCounterStripIcons(s theme.Styles, decisions, actions, risks, questions int) string {
	return renderCounters(s, [4]int{decisions, actions, risks, questions}, " ",
		func(c counterCat, n int) string { return c.glyph })
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// tabBarSep is the single-source separator drawn between detail tabs. It's also
// what detailTabBarFullCells measures, so the pane's minimum-width floor and the
// rendered strip can never disagree about the gap.
const tabBarSep = " │ "

// tabBadgeReserve is the cell budget the tab-bar floor leaves for the worst-case
// attention flag (" ⚑N") the Speakers tab can carry, so a flagged bar still fits
// the minimum content width without the badge being clipped.
const tabBadgeReserve = 3

// detailTabBarFullCells is the cell width of the FULL tab bar — every label
// spelled out (tabs are NEVER abbreviated) plus the worst-case attention badge.
// It's the widest fixed-width row in the pane, so it sets minContentW: the detail
// pane is never sized narrower, and the terminal must be wide enough to grant it
// (else the root shows the too-small guard). Derived from detailTabLabels so a
// tab rename/addition keeps the floor honest.
func detailTabBarFullCells() int {
	w := 0
	for i, label := range detailTabLabels {
		if i > 0 {
			w += lipgloss.Width(tabBarSep)
		}
		w += lipgloss.Width(label)
	}
	return w + tabBadgeReserve
}

// renderTabBar prints "summary | actions | decisions | …", each tab a clickable
// button laid out by a hit.Row (so no column math here): the active tab reads
// bold, a hovered tab lights up, and a clicked tab focuses the pane + switches to
// it. Labels are ALWAYS spelled out in full — the pane is guaranteed wide enough
// for the whole strip (minContentW is derived from it; a terminal too small to
// grant that shows the too-small guard instead), so there's no abbreviation and
// clipLine is only a defensive backstop. When onTab is nil (a sub-mode owns
// input, or a measure/test pass) the row registers nothing and shows no hover.
func (d *detailPane) renderTabBar(ctx screenCtx, width, originX, originY int, onTab func(detailTab) clickAction) string {
	s := ctx.styles
	sep := s.Muted.Render(tabBarSep)
	var hits *hit.Map[region]
	var ptr pointer
	if onTab != nil {
		hits = ctx.hits
		ptr = ctx.pointer()
	}
	row := hit.NewRow(hits, originX, originY)
	for i, label := range detailTabLabels {
		if i > 0 {
			row.Add(sep)
		}
		i, label := i, label
		tab := detailTab(i)
		// Badge the tab that has work waiting, so you know where to go before
		// opening it (e.g. "speakers ⚑2"). Same token as the nav pills; it rides
		// after the recoloured label so press/hover never resize the segment.
		badge := ""
		if n := d.tabAttention(tab); n > 0 {
			badge = " " + attnCount(s, n)
		}
		var onClick clickAction
		if onTab != nil {
			onClick = onTab(tab)
		}
		button{
			id:      fmt.Sprintf("dash:tab:%d", i),
			active:  tab == d.tab,
			onClick: onClick,
			render:  func(st uiState) string { return tabSeg(s, label, st) + badge },
		}.place(row, ptr)
	}
	return clipLine(row.String(), width)
}

// tabSeg renders one detail-tab label in its pointer-resolved state, every
// state the same width so the strip never reflows. A tab is a LABEL, not a
// surface: its active state is a recoloured glyph (bold body text), so it uses
// the label feedback family rather than the accent FILL a row/pill uses —
// dropping a solid blue bar onto a tab would shout. Hover brightens the text
// over a quiet raise; pressed shifts the text to the accent over that same
// raise, so re-clicking the active tab reads as a colour change, not a fill.
func tabSeg(s theme.Styles, label string, st uiState) string {
	switch st {
	case uiPressed:
		return s.LabelPressed.Render(label)
	case uiActive:
		return s.HeaderEm.Render(label)
	case uiHover:
		return s.LabelHover.Render(label)
	default:
		return s.Muted.Render(label)
	}
}

// renderTabBody dispatches to the active tab's renderer.
func (d *detailPane) renderTabBody(ctx screenCtx, width, height int) string {
	switch d.tab {
	case tabSummary:
		return d.renderSummary(ctx, width, height)
	case tabActions:
		return d.renderItemsList(ctx, "Action items", actionItemsToSummary(d.summary.ActionItems), width, height, ctx.styles.Action)
	case tabDecisions:
		return d.renderItemsList(ctx, "Decisions", d.summary.Decisions, width, height, ctx.styles.Decision)
	case tabRisks:
		return d.renderItemsList(ctx, "Risks", d.summary.Risks, width, height, ctx.styles.Risk)
	case tabQuestions:
		return d.renderItemsList(ctx, "Open questions", d.summary.OpenQuestions, width, height, ctx.styles.Question)
	case tabTranscript:
		return d.renderTranscript(ctx, width, height)
	case tabSpeakers:
		return d.renderSpeakers(ctx, width, height)
	}
	return ""
}

// wrapBody word-wraps already-styled (ANSI) content to width cells, breaking
// over-long words so a rendered line NEVER exceeds width — the content can't
// spill past the pane's right edge (the "overflows out the sides" bug). Each
// existing line wraps independently so paragraph and list-row breaks survive.
func wrapBody(content string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	for _, ln := range strings.Split(content, "\n") {
		out = append(out, strings.Split(ansi.Wrap(ln, width, ""), "\n")...)
	}
	return out
}

// paneBody is the ONE renderer every detail-tab body goes through, so the
// summary, the item lists, the transcript and the speakers tab all WRAP (never
// clip past the right edge) and SCROLL (never hide content past the bottom) the
// same way — the single helper this is unified around. A screen never windows or
// draws a scrollbar itself; it describes its body and the focus, exactly like the
// list and transcript already lean on the scroll package.
//
// renderAt draws the body to the content width it is HANDED and returns it as a
// newline-joined block whose every line already fits that width (text tabs wrap
// via wrapText; the speakers tab sizes its talk-time bars to it). paneBody calls
// it once at the full width to learn the line count, and AGAIN at width-1 when a
// scrollbar is needed — so a width-tuned row is rebuilt one cell narrower instead
// of having its last cell re-wrapped onto a stray line (the reason a bar row
// can't just be wrapped like prose).
//
// Scroll model mirrors the scroll package's two shapes:
//   - free-scroll: focus == nil; offset is the caller's stored line offset,
//     clamped to [0, maxOff] (the summary).
//   - cursor-follow: focus(contentW) returns the (top, h) line span to keep
//     visible, derived at the SAME width paneBody renders, so the selected
//     item/segment/speaker stays on screen (items, transcript, speakers).
//
// Returns the windowed block (scrollbar joined when it overflows) and the max
// valid free-scroll offset, so a free-scroll caller can bound its state without
// re-measuring.
func paneBody(s theme.Styles, width, height, offset int, focus func(contentW int) (top, h int), renderAt func(contentW int) string) (string, int) {
	lines := strings.Split(renderAt(width), "\n")
	contentW := width
	if scroll.Needed(len(lines), height) {
		contentW = width - 1 // reserve the scrollbar column, then rebuild to it
		lines = strings.Split(renderAt(contentW), "\n")
	}
	total := len(lines)
	maxOff := total - height
	if maxOff < 0 {
		maxOff = 0
	}
	off := scroll.Clamp(offset, total, height)
	if focus != nil {
		top, h := focus(contentW)
		off = scroll.Follow(total, height, top, h, 0)
	}
	end := off + height
	if end > total {
		end = total
	}
	rows := make([]string, height)
	for i := range rows {
		if off+i < end {
			rows[i] = lines[off+i]
		}
	}
	if contentW == width {
		return strings.Join(rows, "\n"), maxOff
	}
	return attachScrollbar(rows, contentW, total, off, s), maxOff
}

// wrapText is the renderAt callback for the TEXT tabs (summary, items,
// transcript): wrap the already-styled content to the width paneBody hands in, so
// a long line breaks instead of spilling past the pane edge. The speakers tab
// supplies its own callback instead, because its bar rows are sized to the width
// rather than wrapped.
func wrapText(content string) func(int) string {
	return func(w int) string { return strings.Join(wrapBody(content, w), "\n") }
}

// wrappedLineOf maps a SOURCE line index (before wrapping) to the line index it
// occupies once the body is wrapped to contentW — the focus-span helper every
// cursor-following tab shares so a selected item/segment lands where paneBody
// actually renders it. (For the speakers tab, whose rows are clipped to width
// rather than wrapped, every source line is one rendered line and this is exact.)
func wrappedLineOf(content string, srcLine, contentW int) int {
	if srcLine <= 0 || contentW < 1 {
		return 0
	}
	src := strings.Split(content, "\n")
	top := 0
	for i := 0; i < srcLine && i < len(src); i++ {
		top += len(strings.Split(ansi.Wrap(src[i], contentW, ""), "\n"))
	}
	return top
}

func (d *detailPane) renderSummary(ctx screenCtx, width, height int) string {
	s := ctx.styles
	if d.loadingSummary && d.summary.MeetingID == "" {
		return s.Muted.Render("loading summary…")
	}
	tokens := queryTokens(d.query)
	var content string
	switch {
	case d.summary.Markdown != "":
		content = highlightString(s, d.renderPeople(s, d.summary.Markdown), tokens, -1, -1)
	case d.summary.ShortSummary != "":
		content = highlightString(s, d.renderPeople(s, d.summary.ShortSummary), tokens, -1, -1)
	default:
		content = s.Muted.Render("No summary yet. Run `noto summarize` or kick a pipeline job.")
	}
	// Free-scroll: wrap to the body, window by d.bodyScroll, draw the bar. The
	// only state written here is bodyMax (the geometry the next Up/Down bounds
	// against); the offset itself is read-only here and clamped purely for
	// display, so a resize self-corrects without a stale write driving the view.
	block, maxOff := paneBody(s, width, height, d.bodyScroll, nil, wrapText(content))
	d.bodyMax = maxOff
	return block
}

func (d *detailPane) renderItemsList(ctx screenCtx, title string, items []notoapi.SummaryItem, width, height int, titleStyle lipgloss.Style) string {
	s := ctx.styles
	if len(items) == 0 {
		return s.Muted.Render("No " + strings.ToLower(title) + " yet.")
	}
	tokens := queryTokens(d.query)
	// Build the body as one string with explicit newlines; bodyViewport wraps it
	// to the width (so long items wrap instead of clipping) and windows it,
	// cursor-following itemCur so the selected item is always on screen. cursorTop
	// is tracked in WRAPPED-line space, computed below after wrapping per row.
	var b strings.Builder
	b.WriteString(titleStyle.Bold(true).Render(title))
	b.WriteString("\n\n")
	// Record each item's first source line so the focus span can be mapped to
	// wrapped lines (an item may wrap to several). itemTop[i] is the line index
	// (pre-wrap) where item i begins.
	itemTop := make([]int, len(items))
	srcLine := 2 // title + blank already emitted
	hasJump := false
	for i, it := range items {
		itemTop[i] = srcLine
		body := highlightString(s, d.renderPeople(s, it.Text), tokens, -1, -1)
		// ▸ marks the item Enter jumps from. Keep the marker a fixed 2 cells
		// (matching the inactive lead) so the numbered list stays aligned.
		marker := "  "
		if i == d.itemCur {
			marker = titleStyle.Bold(true).Render("▸") + " "
		}
		row := fmt.Sprintf("%s%d. %s", marker, i+1, body)
		if stamps := d.evidenceStamps(it.SegmentRefs); stamps != "" {
			hasJump = true
			row += "  " + s.Citation.Render(stamps)
		}
		b.WriteString(row)
		b.WriteByte('\n')
		srcLine++
	}
	if hasJump {
		// Derive the key glyphs from the bindings (single source of truth) the
		// same way the transcript footer advertises its n/N jumps.
		hint := fmt.Sprintf("%s/%s select · %s jump to transcript",
			ctx.keys.Up.Help().Key, ctx.keys.Down.Help().Key, ctx.keys.Enter.Help().Key)
		b.WriteString("\n")
		b.WriteString(s.Muted.Render(hint))
	}
	content := b.String()
	// Map the selected item's pre-wrap source line to its position in the wrapped
	// body so cursor-follow keeps it visible even when earlier items wrapped.
	focus := func(contentW int) (int, int) {
		if d.itemCur < 0 || d.itemCur >= len(itemTop) {
			return 0, 1
		}
		return wrappedLineOf(content, itemTop[d.itemCur], contentW), 1
	}
	block, _ := paneBody(s, width, height, 0, focus, wrapText(content))
	return block
}

// evidenceStamps renders an item's cited segments as human timestamps
// ("[00:08 · 00:36]"). A segment id is an internal artifact; a reader wants to
// know WHEN in the meeting — and the timestamp doubles as the jump target.
// Returns "" when none of the refs resolve to a loaded segment.
func (d *detailPane) evidenceStamps(refs []string) string {
	if len(refs) == 0 || len(d.transcript.Segments) == 0 {
		return ""
	}
	start := make(map[string]float64, len(d.transcript.Segments))
	for _, seg := range d.transcript.Segments {
		start[seg.ID] = seg.StartSec
	}
	stamps := make([]string, 0, len(refs))
	for _, ref := range refs {
		if t, ok := start[ref]; ok {
			stamps = append(stamps, formatSec(t))
		}
	}
	if len(stamps) == 0 {
		return ""
	}
	return "[" + strings.Join(stamps, " · ") + "]"
}

func actionItemsToSummary(actions []notoapi.ActionItem) []notoapi.SummaryItem {
	out := make([]notoapi.SummaryItem, 0, len(actions))
	for _, a := range actions {
		text := a.Text
		if a.Owner != "" {
			text += "  (owner: " + a.Owner + ")"
		}
		out = append(out, notoapi.SummaryItem{Text: text, SegmentRefs: a.SegmentRefs})
	}
	return out
}

// renderTranscript shows speaker-colored segments with FTS highlights through
// the shared paneBody: every segment is rendered as a header + body line, wrapped
// to the pane width (long turns break instead of clipping past the edge) and
// windowed with the shared scrollbar. Cursor-follow keeps the active segment
// visible — the n/N search match, the Enter-jump target, or the up/down scroll
// position (transcriptScroll), so "where did this happen?" always stays on
// screen. A pinned footer advertises the live search hit count.
func (d *detailPane) renderTranscript(ctx screenCtx, width, height int) string {
	s := ctx.styles
	segs := d.transcript.Segments
	if len(segs) == 0 {
		if d.loadingTranscript {
			return s.Muted.Render("loading transcript…")
		}
		return s.Muted.Render("no transcript yet — run a pipeline job")
	}
	tokens := queryTokens(d.query)
	// A live search match wins; otherwise highlight the segment an item's
	// Enter-jump landed on, so "where did this decision happen?" is obvious.
	activeSeg := -1
	if len(d.matchedSegs) > 0 && d.matchCursor < len(d.matchedSegs) {
		activeSeg = d.matchedSegs[d.matchCursor]
	} else if d.focusSeg >= 0 && d.focusSeg < len(segs) {
		activeSeg = d.focusSeg
	}
	// Color each speaker by the SAME per-speaker index used in the header, the
	// speakers tab and inline name colorization (talk-time order, full 6-hue
	// palette) so a person keeps one identity color everywhere. Precompute the
	// whole map (falling back to appearance order for ids without stats) so the
	// renderAt callback below is pure — paneBody may call it more than once.
	colorByID := make(map[string]int, len(d.speakers))
	for _, sp := range d.speakers {
		colorByID[sp.ID] = sp.colorIdx
	}
	next := 0
	for _, seg := range segs {
		if _, ok := colorByID[seg.SpeakerID]; !ok {
			colorByID[seg.SpeakerID] = next % 6
			next++
		}
	}

	// Precompute the two width-independent, per-frame-constant pieces ONCE, so
	// build (called up to 3× per frame, over every segment) doesn't recompute
	// them per segment:
	//   - repls: the people-rewrite table (resolves + styles every speaker; the
	//     dominant cost when rebuilt per segment), applied per body line.
	//   - spNameByID: the styled "[stamp] Name" speaker lead per distinct speaker,
	//     so the per-segment header is a map lookup, not a resolve+style+Render.
	repls := d.peopleReplacer(s)
	spNameByID := make(map[string]string, len(colorByID))
	for id := range colorByID {
		name, tier := d.resolveSpeaker(id)
		spNameByID[id] = styleSpeaker(s, name, colorByID[id], tier)
	}

	// build lays the transcript out at content width w and returns the rendered
	// lines plus each segment's first-line index (for cursor-follow). Like the
	// summary, the transcript is reading content, so it is FLUSH at column 0 and
	// uses the FULL width — no marker gutter (the old 4-cell header pad + 6-cell
	// body indent that ate the width is gone). The [MM:SS] stamp + speaker lead
	// each segment; the spoken text wraps full width beneath. The active segment
	// (an n/N match or an Enter-jump target) is marked WIDTH-NEUTRALLY by
	// highlighting its timestamp, so nothing shifts as the focus moves. A blank
	// line separates segments for legibility.
	//
	// Memoized by width: paneBody calls renderAt(contentW) and then focus(contentW)
	// at the SAME width, so the focus pass (which only needs tops) reuses the
	// rendered result instead of laying the whole transcript out a second time.
	cacheW := -1
	var cacheLines string
	var cacheTops []int
	build := func(w int) (string, []int) {
		if w == cacheW {
			return cacheLines, cacheTops
		}
		var lines []string
		tops := make([]int, len(segs))
		for i, seg := range segs {
			tops[i] = len(lines)
			// Lead with the human timestamp + speaker only; the internal segment
			// id ("seg_001") is meaningless to a reader and leaks an underscore.
			stampStyle := s.Muted
			if i == activeSeg {
				stampStyle = s.HighlightActive
			}
			header := stampStyle.Render(fmt.Sprintf("[%s]", formatSec(seg.StartSec))) + " " + spNameByID[seg.SpeakerID]
			lines = append(lines, clipLine(header, w))
			body := highlightString(s, applyPeople(seg.Text, repls), tokens, i, activeSeg)
			lines = append(lines, wrapBody(body, w)...)
			if i < len(segs)-1 {
				lines = append(lines, "")
			}
		}
		cacheW, cacheLines, cacheTops = w, strings.Join(lines, "\n"), tops
		return cacheLines, cacheTops
	}

	// A pinned footer (it must not scroll away) advertises the search hit count;
	// it costs a blank + a line, so the scrollable body shrinks to match.
	footer := ""
	bodyH := height
	if len(d.matchedSegs) > 0 {
		footer = s.Muted.Render(fmt.Sprintf("hit %d/%d (%s/%s to jump)",
			d.matchCursor+1, len(d.matchedSegs),
			ctx.keys.NextMatch.Help().Key, ctx.keys.PrevMatch.Help().Key))
		if bodyH = height - 2; bodyH < 1 {
			bodyH = 1
		}
	}

	// Cursor-follow the active segment (or the up/down scroll position), keeping
	// its whole header+body block visible.
	focusSeg := activeSeg
	if focusSeg < 0 {
		focusSeg = d.transcriptScroll
	}
	focus := func(w int) (int, int) {
		_, tops := build(w)
		if focusSeg < 0 || focusSeg >= len(tops) {
			return 0, 1
		}
		top := tops[focusSeg]
		h := 2
		if focusSeg+1 < len(tops) {
			h = tops[focusSeg+1] - top
		}
		return top, h
	}
	block, _ := paneBody(s, width, bodyH, 0, focus, func(w int) string {
		c, _ := build(w)
		return c
	})
	if footer != "" {
		block += "\n\n" + footer
	}
	return block
}

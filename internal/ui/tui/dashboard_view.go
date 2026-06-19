package tui

import (
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/scroll"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// --- mouse focus helpers ------------------------------------------
//
// These mutate focus the same way the keyboard paths do, so a click and a
// keypress land in identical states. They're the onClick bodies for the
// dashboard's clickable regions (registered in viewWide/viewNarrow +
// renderList + the detail tab bar).

// focusList moves focus to the meetings list (clicking the left pane).
func (m *dashboardScreen) focusList() {
	m.paneOpen = false
	m.inputFocus = false
	m.input.Blur()
}

// focusPane moves focus into the detail pane (clicking the right pane), but
// only when a meeting is bound — an empty pane has nothing to drive.
func (m *dashboardScreen) focusPane() {
	if m.currentMeetingID() == "" {
		return
	}
	m.paneOpen = true
	m.inputFocus = false
	m.input.Blur()
}

// focusFilter focuses the search input (clicking the filter row), mirroring `/`.
func (m *dashboardScreen) focusFilter(ctx screenCtx) tea.Cmd {
	m.paneOpen = false
	m.inputFocus = true
	m.input.Focus()
	return m.refreshFocusedSearch(ctx)
}

// selectRow selects meeting i and loads it into the pane, keeping list focus —
// a click is the pointer twin of arrow-key navigation.
func (m *dashboardScreen) selectRow(ctx screenCtx, i int) tea.Cmd {
	if i < 0 || i >= m.visibleCount() {
		return nil
	}
	m.cursor = i
	m.focusList()
	return m.loadSelectedCmd(ctx)
}

// openTab focuses the pane AND switches it to tab t — so clicking "actions"
// from the list both moves focus and navigates, not just one (the prioritised
// behaviour: a click on a target acts on the target).
func (m *dashboardScreen) openTab(t detailTab) {
	m.focusPane()
	if m.currentMeetingID() == "" {
		return
	}
	m.pane.tab = t
	m.pane.itemCur = 0
}

// --- view ---------------------------------------------------------

func (m *dashboardScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if m.loading {
		return panelEmpty(ctx, "dashboard", s.Muted.Render("loading meetings…"))
	}
	if m.err != nil {
		return panelEmpty(ctx, "dashboard", s.Danger.Render("✗ "+m.err.Error()))
	}

	var content string
	if layout.BreakpointFor(ctx.width) == layout.Narrow {
		content = m.viewNarrow(ctx)
	} else {
		content = m.viewWide(ctx)
	}
	// The identify/reassign dialog is owned by the pane but composited here so
	// it dims the whole dashboard (same pattern as the People confirm overlay).
	if m.pane.assignDialogOpen() {
		content = overlayCenter(content, m.pane.assignDialogView(ctx), ctx.width, ctx.height, s.T.Faint)
	}
	return content
}

// bottomStripHeight is dynamic: hidden when idle, ~6 rows when there's
// activity, ~8 rows when there are multiple concurrent jobs. The
// border alone eats 2 rows so the inner body lands at 4 / 6 rows.
func (m *dashboardScreen) bottomStripHeight() int {
	if !m.rec.Active && len(m.jobs) == 0 {
		return 0
	}
	if len(m.jobs) >= 3 {
		return 8
	}
	return 6
}

func (m *dashboardScreen) viewWide(ctx screenCtx) string {
	s := ctx.styles
	// Left meetings column is the shared, draggable sidebar width (clamped so
	// neither pane drops below its floor); details takes the rest. joinSidebar
	// (below) reserves the same 1-cell gap the solver accounts for and draws the
	// divider in it.
	leftW, rightW := layout.SidebarSplit(ctx.width, sidebarPref(ctx), minSidebarW, minContentW)

	// Left column splits vertically into the meetings list (flex) and the
	// bottom strip (fixed; 0 collapses it entirely).
	stripH := m.bottomStripHeight()
	listH := layout.Split(ctx.height, 0, layout.FlexMin(1, 6), layout.Fixed(stripH))[0]

	listP := layout.Panel{Title: "dashboard", Width: leftW, Height: listH}
	// The detail panel's chrome carries the meeting identity now: "Details: <name>"
	// on the left, muted date · time · status flag on the right (pre-styled so the
	// flag keeps its colour). Title is fit to leave room for the meta.
	paneMeta := m.pane.headerMeta(s)
	paneP := layout.Panel{
		Title: detailPaneTitle(m.pane, rightW-4, lipgloss.Width(paneMeta)),
		Right: paneMeta,
		Width: rightW, Height: ctx.height,
	}

	// Register clickable regions (skipped while a pane sub-mode owns input, so a
	// stray click can't act behind the rename editor / identify dialog). Coarse
	// pane-focus regions go in FIRST; the finer row/tab regions registered by
	// renderList / the tab bar are added afterwards and win (last-added wins).
	if !m.pane.inputActive() {
		rightX := leftW + 1 // HStack 1-cell gap
		ctx.hits.Add(hit.Rect{X: 0, Y: ctx.bodyTop, W: leftW, H: listH},
			region{id: "dash:list", onClick: func() tea.Cmd { m.focusList(); return nil }})
		ctx.hits.Add(hit.Rect{X: rightX, Y: ctx.bodyTop, W: rightW, H: ctx.height},
			region{id: "dash:pane", onClick: func() tea.Cmd { m.focusPane(); return nil }})
	}

	listOX, listOY := listP.BodyOffset()
	listPanel := func() string {
		p := listP
		p.Subtitle = m.listSubtitle()
		p.Focused = !m.paneOpen
		p.Body = m.renderList(ctx, leftW, listH-4, listOX, ctx.bodyTop+listOY)
		return p.Render(s)
	}()

	leftCol := listPanel
	if stripH > 0 {
		leftCol = layout.VStack(listPanel, m.renderBottomStrip(ctx, leftW, stripH))
	}

	paneOX, paneOY := paneP.BodyOffset()
	var onTab func(detailTab) clickAction
	if !m.pane.inputActive() {
		onTab = func(t detailTab) clickAction { return func() tea.Cmd { m.openTab(t); return nil } }
	}
	rightPanel := func() string {
		p := paneP
		p.Focused = m.paneOpen
		p.Body = m.pane.view(ctx, rightW-4, ctx.height-2, (leftW+1)+paneOX, ctx.bodyTop+paneOY, onTab)
		return p.Render(s)
	}()

	return joinSidebar(ctx, leftCol, rightPanel, leftW, ctx.height)
}

func (m *dashboardScreen) viewNarrow(ctx screenCtx) string {
	s := ctx.styles
	stripH := m.bottomStripHeight()
	listH := layout.Split(ctx.height, 0, layout.FlexMin(1, 6), layout.Fixed(stripH))[0]
	listP := layout.Panel{Title: "dashboard", Width: ctx.width, Height: listH}
	ox, oy := listP.BodyOffset()
	listP.Subtitle = m.listSubtitle()
	listP.Focused = true
	listP.Body = m.renderList(ctx, ctx.width, listH-4, ox, ctx.bodyTop+oy)
	listPanel := listP.Render(s)
	if stripH == 0 {
		return listPanel
	}
	return layout.VStack(listPanel, m.renderBottomStrip(ctx, ctx.width, stripH))
}

// renderBottomStrip lays out jobs + active recording as a 50/50
// horizontal split. Hidden when both are idle.
func (m *dashboardScreen) renderBottomStrip(ctx screenCtx, width, height int) string {
	s := ctx.styles
	cols := layout.Split(width, 1, layout.Flex(1), layout.Flex(1))
	jobsW, recW := cols[0], cols[1]
	jobsPanel := layout.Panel{
		Title:    "jobs",
		Subtitle: m.jobsSubtitle(),
		Width:    jobsW,
		Height:   height,
		Body:     m.renderJobsBody(ctx, jobsW-4),
	}.Render(s)
	recPanel := layout.Panel{
		Title:    "recording",
		Subtitle: m.recordingSubtitle(),
		Width:    recW,
		Height:   height,
		Body:     m.renderRecordingBody(ctx, recW-4),
	}.Render(s)
	return layout.HStack(jobsPanel, recPanel)
}

func (m *dashboardScreen) jobsSubtitle() string {
	if len(m.jobs) == 0 {
		return ""
	}
	running := 0
	for _, j := range m.jobs {
		if j.Status == notoapi.JobRunning {
			running++
		}
	}
	if running > 0 {
		return fmt.Sprintf("%d running", running)
	}
	return fmt.Sprintf("%d recent", len(m.jobs))
}

func (m *dashboardScreen) recordingSubtitle() string {
	if !m.rec.Active {
		return ""
	}
	return formatDuration(m.rec.ElapsedSec)
}

func (m *dashboardScreen) renderJobsBody(ctx screenCtx, width int) string {
	s := ctx.styles
	if len(m.jobs) == 0 {
		return s.Muted.Render("idle")
	}
	out := []string{}
	maxRows := 4
	if width > 60 {
		maxRows = 6
	}
	for i, j := range m.jobs {
		if i >= maxRows {
			break
		}
		bar := jobBar(j.Progress, 10)
		var status string
		switch j.Status {
		case notoapi.JobRunning:
			status = s.Info.Render("running")
		case notoapi.JobSucceeded:
			status = s.Success.Render("done")
		case notoapi.JobFailed:
			status = s.Danger.Render("failed")
		case notoapi.JobInterrupted:
			status = s.Warning.Render("interrupted")
		default:
			status = s.Muted.Render(string(j.Status))
		}
		line := fmt.Sprintf("%s %s %s", jobKindStyled(s, j.Kind), bar, status)
		out = append(out, clipLine(line, width))
	}
	return strings.Join(out, "\n")
}

func (m *dashboardScreen) renderRecordingBody(ctx screenCtx, width int) string {
	s := ctx.styles
	if !m.rec.Active {
		return strings.Join([]string{
			s.Muted.Render("○ idle"),
			s.Muted.Render("press ") + s.ChipKey.Render(ctx.keys.Record.Help().Key) + s.Muted.Render(" to record"),
		}, "\n")
	}
	lines := []string{
		s.Recording.Render(fmt.Sprintf("● REC  %s", formatDuration(m.rec.ElapsedSec))),
		s.HeaderEm.Render(def(m.rec.Title, "Untitled meeting")),
		renderMeter(s, "mic ", m.rec.MicDB),
		renderMeter(s, "sys ", m.rec.ParticipantDB),
	}
	for i, l := range lines {
		lines[i] = clipLine(l, width)
	}
	return strings.Join(lines, "\n")
}

// detailPaneTitle is the detail panel's left title fit to leave room for the
// right-aligned meta: "Details: <name>" truncated so title + 1-cell gap + meta
// stay within the panel's inner width.
func detailPaneTitle(d *detailPane, innerW, metaW int) string {
	full := d.headerTitle()
	avail := innerW - metaW - 1 // 1-cell minimum gap before the right meta
	if avail < 1 {
		avail = 1
	}
	if lipgloss.Width(full) <= avail {
		return full
	}
	return fit(full, avail)
}

// --- list rendering -----------------------------------------------

func (m *dashboardScreen) renderList(ctx screenCtx, width, bodyH, originX, originY int) string {
	s := ctx.styles
	// Panel padding+border consumes 4 cols; clip everything we draw to
	// what's actually visible inside. Otherwise long titles, snippets,
	// or the hint bar spray past the right border.
	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}

	// Mouse: the filter row is body line 0, meeting rows start at body line 2
	// (a blank line sits between). originY is the absolute y of body line 0, so
	// we register click regions as we lay rows out. Skipped while a pane
	// sub-mode owns input (rename editor / identify dialog).
	clickable := !m.pane.inputActive()
	if clickable {
		ctx.hits.Add(hit.Rect{X: originX, Y: originY, W: innerW, H: 1},
			region{id: "dash:filter", onClick: func() tea.Cmd { return m.focusFilter(ctx) }})
	}
	const rowsTop = 2 // header line + blank line before the first row

	// Filter and hints share the same left-edge as list rows (3-cell
	// marker gutter) so titles, the search chip, and the hint bar all
	// start at the same column. Without this the title text appears
	// shifted relative to the search input.
	header := clipLine("   "+m.renderFilterRow(ctx), innerW)
	// Hover feedback on the filter row (the input never "selects", so it only
	// ever hovers). Skipped while the search box is focused (clickable=false).
	if clickable {
		header = rowFeedback(header, ctx.pointer().state("dash:filter", false), s, innerW, 0)
	}
	hintBar := s.HintBar.Render(clipLine("   "+m.renderListHints(ctx, innerW-3), innerW))

	if m.visibleCount() == 0 {
		empty := s.Muted.Render("no meetings yet — record one with r, or run `make seed`")
		if m.query != "" {
			empty = s.Muted.Render(fmt.Sprintf("no matches for %q", m.query))
		}
		return header + "\n\n" + clipLine(empty, innerW) + "\n\n" + hintBar
	}

	// The rows are the one scrollable region; the filter and hint bar are fixed
	// chrome above/below it. rowBudget is how many row-lines the panel can show
	// (body height minus filter + blank + blank + hintBar).
	rowBudget := bodyH - 4
	if rowBudget < 1 {
		rowBudget = 1
	}

	tokens := queryTokens(m.query)

	// The meeting rows are the one scrollable region; rowList owns the windowing,
	// the reserved scrollbar column and the per-row click regions — and renders
	// ONLY the rows actually on screen. This screen keeps its own row LOOK: an
	// amber attnGutter down rows needing triage, the cursor ▸, the counter strip,
	// and the selection fill (rowFeedback). Each composed line is gutter + content
	// clipped to rowInnerW, so it is at most contentW wide — leaving the bar its
	// column. The counter-strip compaction tier is decided ONCE for the whole list
	// (so every row breaks at the same width), the moment rowList hands us the
	// final content width.
	var rowInnerW int
	var countsTierSel countsTier
	list := rowList{
		ctx: ctx, width: innerW, height: rowBudget,
		originX: originX, originY: originY + rowsTop,
		count: m.visibleCount(), cursor: m.cursor, offset: m.listScroll, clickable: clickable,
		lineCount: m.rowLineCount,
		rowID:     func(i int) string { return fmt.Sprintf("dash:row:%d", i) },
		onClick:   func(i int) tea.Cmd { return m.selectRow(ctx, i) },
		prepare: func(contentW int) {
			rowInnerW = contentW - 3 // the 3-cell " ▸ " / "   " gutter
			if rowInnerW < 14 {
				rowInnerW = 14
			}
			countsTierSel = m.listCountsTier(s, rowInnerW)
		},
		render: func(i, contentW int, st uiState) []string {
			var lines []string
			attention := false
			if m.query == "" {
				lines = m.renderListRowDefault(s, m.all[i], rowInnerW, countsTierSel)
				attention = identityNeedsAttention(m.all[i].Identity)
			} else {
				lines = m.renderListRowSearch(s, m.matched[i], rowInnerW, tokens)
			}
			first := attnGutter(s, i == m.cursor, attention)
			rest := attnGutter(s, false, attention)
			protect := 0
			if attention {
				protect = 1 // keep the amber ▍ beside the selection fill
			}
			composed := make([]string, len(lines))
			for j, line := range lines {
				indent := first
				if j > 0 {
					indent = rest
				}
				composed[j] = rowFeedback(indent+clipLine(line, rowInnerW), st, s, contentW, protect)
			}
			return composed
		},
	}
	return header + "\n\n" + list.view() + "\n\n" + hintBar
}

// padCells right-pads a (possibly ANSI-styled) line with spaces to w display
// cells; it never truncates. Used to anchor a trailing scrollbar to a fixed
// column when a row renders shorter than the gutter width.
func padCells(line string, w int) string {
	if gap := w - ansi.StringWidth(line); gap > 0 {
		return line + strings.Repeat(" ", gap)
	}
	return line
}

// attachScrollbar is the ONE composition step every scrollable region shares
// after windowing: pad each already-windowed row to `width` cells, then join the
// shared scroll.Bar gutter on the right — anchored to a fixed column even when a
// row renders short. The meeting list and every detail-pane body go through it,
// so a scrolled region looks identical everywhere (the same way scroll.Bar owns
// the glyph). The caller has already reserved the column (rows rendered at
// inner-1) and decided — via scroll.Needed — that the bar is warranted; the bar's
// height is len(rows), so the gutter always matches the block it rides beside.
func attachScrollbar(rows []string, width, total, offset int, s theme.Styles) string {
	padded := make([]string, len(rows))
	for i, ln := range rows {
		padded[i] = padCells(ln, width)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		strings.Join(padded, "\n"), scroll.Bar(len(rows), total, offset, s))
}

// renderListHints picks a hint set that fits the available width.
// Below ~50 cols (left panel on a narrow terminal) the long list of
// hints overflows; we collapse to the essential three.
func (m *dashboardScreen) renderListHints(ctx screenCtx, width int) string {
	s, k := ctx.styles, ctx.keys
	full := []string{
		chip(s, k.Search),
		chipPair(s, k.Up, k.Down, "navigate"),
		chipAs(s, k.Enter, "open"),
		chip(s, k.NextMatch),
		chip(s, k.ClearSearch),
	}
	joined := strings.Join(full, "  ")
	if lipgloss.Width(joined) <= width {
		return joined
	}
	short := []string{
		chip(s, k.Search),
		chipPair(s, k.Up, k.Down, "nav"),
		chipAs(s, k.Enter, "open"),
	}
	return strings.Join(short, "  ")
}

func (m *dashboardScreen) renderFilterRow(ctx screenCtx) string {
	s, k := ctx.styles, ctx.keys
	keyChip := s.ChipKey.Render(k.Search.Help().Key)
	if m.inputFocus {
		return keyChip + " " + m.input.View()
	}
	if m.query == "" {
		return keyChip + " " + s.Muted.Render(m.input.Placeholder)
	}
	editHint := fmt.Sprintf("(press %s to edit · %s to clear)", k.Search.Help().Key, k.ClearSearch.Help().Key)
	return keyChip + " " + s.HeaderEm.Render(m.query) + "   " + s.Muted.Render(editHint)
}

// renderListRowDefault lays out a "browse" row over 2 visible lines:
//
//	row 1: title · date · status_badge
//	row 2: ◆ N decisions   ▸ N actions   ⚠ N risks   ? N questions  (omits zero counts)
//
// The meta cells (date + status) are MEASURED and the title gets whatever's left,
// so the row fits `width` exactly without a hand-tuned shoulder constant. compact
// is decided ONCE for the whole list (see listCountsTier) and passed in, so
// every row breaks to the same form at the SAME width — a flagged row (its ⚑N is
// wider) never compacts a step earlier than its unflagged neighbour.
func (m *dashboardScreen) renderListRowDefault(s theme.Styles, mt notoapi.Meeting, width int, tier countsTier) []string {
	when := s.Muted.Render(mt.CreatedAt.Format("Jan 02 15:04"))
	badge := dashboardStatusBadge(s, mt.Status)
	// Trailing meta, with its leading 2-space gaps, is fixed-width; the title
	// flexes into the remainder.
	meta := "  " + when
	if badge != "" {
		meta += "  " + badge
	}
	titleBudget := width - lipgloss.Width(meta)
	if titleBudget < 6 {
		titleBudget = 6 // a stub title beats none on a very narrow column
	}
	row1 := s.HeaderEm.Render(fit(mt.Title, titleBudget)) + meta
	// row 2 is the counts + the attention flag (⚑N) for speakers still needing
	// identity work; a resolved meeting adds nothing.
	var counts string
	switch tier {
	case countsIcon:
		counts = renderCounterStripIcons(s, mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount)
	case countsIconNum:
		counts = renderCounterStripCompact(s, mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount)
	default:
		counts = renderCounterStrip(s, mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount)
	}
	row2 := joinRow2(counts, identityCluster(s, mt.Identity))
	if row2 == "" {
		row2 = s.Muted.Render("·")
	}
	return []string{row1, row2}
}

// countsTier is the rendering fidelity of a list row's counter strip, decided
// ONCE for the whole list (listCountsTier). The three-step responsive family,
// mirroring the nav pills' full → mid → min: spelled words, icon+count, then the
// icon alone.
type countsTier int

const (
	countsFull    countsTier = iota // ◆ 2 decisions   ▸ 3 actions   …
	countsIconNum                   // ◆2 ▸3 ⚠1 ?2
	countsIcon                      // ◆ ▸ ⚠ ?
)

// listCountsTier decides — for the WHOLE browse list at once — which counter
// form the row-2 strips use. It's all-or-nothing so the list reads cleanly: it
// returns the widest tier whose EVERY row (counts + identity flag) fits the
// column, so the break point is identical across rows (flagged or not) instead of
// ragged. Returns countsFull in search mode (those rows carry no counter strip).
func (m *dashboardScreen) listCountsTier(s theme.Styles, width int) countsTier {
	if m.query != "" {
		return countsFull
	}
	// A row's strip width is a pure function of its counts + the identity
	// attention total (the only inputs to the two strips and identityCluster), and
	// most rows share one signature — so measure each DISTINCT signature once
	// rather than re-rendering a strip per meeting. That keeps this O(distinct
	// rows), so a long library costs about the same here as the windowed row
	// render does. Early-exit the moment the worst tier is forced.
	type sig struct{ d, a, r, q, attn int }
	seen := make(map[sig]struct{})
	fullFits, iconNumFits := true, true
	for i := 0; i < m.visibleCount(); i++ {
		mt := m.all[i]
		attn := 0
		if mt.Identity != nil {
			attn = mt.Identity.Likely + mt.Identity.New + mt.Identity.Unset
		}
		k := sig{mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount, attn}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		id := identityCluster(s, mt.Identity)
		full := joinRow2(renderCounterStrip(s, mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount), id)
		if lipgloss.Width(full) > width {
			fullFits = false
		}
		iconNum := joinRow2(renderCounterStripCompact(s, mt.DecisionCount, mt.ActionCount, mt.RiskCount, mt.QuestionCount), id)
		if lipgloss.Width(iconNum) > width {
			iconNumFits = false
		}
		if !fullFits && !iconNumFits {
			break // worst tier already forced; nothing left to learn
		}
	}
	switch {
	case fullFits:
		return countsFull
	case iconNumFits:
		return countsIconNum
	default:
		return countsIcon
	}
}

// joinRow2 joins the counter strip and identity rollup with a 3-space gap,
// skipping either when empty so a meeting with only one of them has no dangling
// separator.
func joinRow2(counts, identity string) string {
	switch {
	case counts != "" && identity != "":
		return counts + "   " + identity
	case counts != "":
		return counts
	default:
		return identity
	}
}

// identityCluster renders the per-meeting speaker-identity rollup as the SAME
// single amber ⚑N the rest of the attention system uses (nav pill, detail tab
// bar, Speakers rollup): one count of speakers still needing identity work
// (likely + new + not-set). Splitting it into separate ◐/✦/○ glyphs put two
// near-identical amber chips on a row and broke the "one flag, traceable from
// the nav strip to the item" language — the breakdown belongs in the detail
// pane (identityRollupLine), not the glanceable list. A fully-resolved meeting
// (or one with no mappings) renders NOTHING: the list flags only outstanding
// work, never a "done" badge.
func identityCluster(s theme.Styles, id *notoapi.SpeakerIdentitySummary) string {
	if id == nil {
		return ""
	}
	if n := id.Likely + id.New + id.Unset; n > 0 {
		return attnCount(s, n)
	}
	// A fully-resolved meeting shows NOTHING here — a "done" badge on every
	// finished row is just noise. The list flags only what still needs the user
	// (the ⚑N); silence means resolved.
	return ""
}

// identityNeedsAttention reports whether a meeting has any speaker still needing
// identity work (likely / new / not-set). Drives the list row's attention bar.
func identityNeedsAttention(id *notoapi.SpeakerIdentitySummary) bool {
	return id != nil && (id.Likely > 0 || id.New > 0 || id.Unset > 0)
}

// outstandingStatusBadge flags only a meeting's UNFINISHED state for the detail
// header — recording (in progress), recorded-but-not-yet-transcribed, or failed.
// A transcribed/summarized meeting is "done" and carries no badge (the header
// shouldn't shout a green check on every finished meeting). Mirrors the list's
// "flag only outstanding work" rule, with header-appropriate wording.
func outstandingStatusBadge(s theme.Styles, status notoapi.MeetingStatus) string {
	switch status {
	case notoapi.StatusRecording:
		return badgeDanger(s, "● rec")
	case notoapi.StatusRecorded:
		return badgeWarn(s, "untranscribed")
	case notoapi.StatusFailed:
		return badgeDanger(s, "✗ failed")
	default:
		return ""
	}
}

func dashboardStatusBadge(s theme.Styles, status notoapi.MeetingStatus) string {
	switch status {
	case notoapi.StatusRecorded:
		return badgeWarn(s, "todo")
	case notoapi.StatusRecording:
		return badgeDanger(s, "● rec")
	case notoapi.StatusFailed:
		return badgeDanger(s, "✗ failed")
	default:
		return ""
	}
}

// renderListRowSearch returns 1 or 2 visible lines: the title row and,
// when present, a snippet preview. The caller is responsible for
// clipping/indenting each line.
func (m *dashboardScreen) renderListRowSearch(s theme.Styles, mh notoapi.MeetingHits, width int, tokens []string) []string {
	titleBudget := width - 22
	if titleBudget < 10 {
		titleBudget = 10
	}
	title := fit(mh.MeetingTitle, titleBudget)
	titleStyled := highlightString(s, title, tokens, -1, -1)
	dot := s.Muted.Render("○ ")
	switch {
	case mh.TitleMatch:
		dot = s.HeaderEm.Render("● ")
	case mh.SummaryMatch:
		dot = s.Info.Render("✦ ")
	}
	when := ""
	if !mh.CreatedAt.IsZero() {
		when = mh.CreatedAt.Format("Jan 02")
	}
	counts := []string{}
	if mh.TranscriptCount > 0 {
		counts = append(counts, fmt.Sprintf("tr %d", mh.TranscriptCount))
	}
	if mh.SummaryCount > 0 {
		counts = append(counts, fmt.Sprintf("su %d", mh.SummaryCount))
	}
	first := dot + titleStyled + "  " + s.Muted.Render(when)
	if len(counts) > 0 {
		first += "  " + s.Info.Render(strings.Join(counts, " · "))
	}
	out := []string{first}
	if mh.Snippet != "" {
		out = append(out, s.Muted.Render(snippetText(mh.Snippet, width-2)))
	}
	return out
}

func (m *dashboardScreen) listSubtitle() string {
	if m.query != "" {
		return fmt.Sprintf("%d hits", len(m.matched))
	}
	return fmt.Sprintf("%d total", len(m.all))
}

// --- highlight + token helpers ------------------------------------

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func queryTokens(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	parts := nonWord.Split(q, -1)
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func textContainsAny(text string, tokens []string) bool {
	lower := strings.ToLower(text)
	for _, t := range tokens {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// highlightString wraps every token occurrence (case-insensitive) in
// the Highlight style. If segIdx == activeIdx the rendering uses the
// HighlightActive style so the user can see which segment n/N is on.
func highlightString(s theme.Styles, text string, tokens []string, segIdx, activeIdx int) string {
	if text == "" || len(tokens) == 0 {
		return text
	}
	pattern := buildTokenPattern(tokens)
	if pattern == nil {
		return text
	}
	style := s.Highlight
	if segIdx >= 0 && segIdx == activeIdx {
		style = s.HighlightActive
	}
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		return style.Render(match)
	})
}

var patternCache = map[string]*regexp.Regexp{}

func buildTokenPattern(tokens []string) *regexp.Regexp {
	key := strings.Join(tokens, "|")
	if re, ok := patternCache[key]; ok {
		return re
	}
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = regexp.QuoteMeta(t)
	}
	re, err := regexp.Compile("(?i)(" + strings.Join(quoted, "|") + ")")
	if err != nil {
		return nil
	}
	patternCache[key] = re
	return re
}

// --- small helpers ------------------------------------------------

// snippetText strips FTS5 '**' delimiters (we apply our own highlight)
// and clamps length.
func snippetText(snip string, maxLen int) string {
	snip = strings.ReplaceAll(snip, "**", "")
	if len(snip) > maxLen {
		snip = snip[:maxLen-1] + "…"
	}
	return snip
}

// clipLine truncates a single rendered line to `width` visible cells
// while preserving ANSI styles. Lipgloss's MaxWidth handles the ANSI
// math; we keep the input single-line so it doesn't insert padding.
func clipLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func deleteMeetingCmd(ctx screenCtx, id string) tea.Cmd {
	return func() tea.Msg {
		err := ctx.client.DeleteMeeting(ctx.ctx, id)
		if err != nil {
			return bannerMsg{Kind: "error", Text: err.Error()}
		}
		return bannerMsg{Kind: "info", Text: "meeting deleted"}
	}
}

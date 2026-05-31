package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/tui/theme"
)

// view renders the full pane body: fixed header (3 lines + rule), tab
// strip (1 line + rule), and the active tab body. The host wraps the
// returned string in its own layout.Panel.
func (d *detailPane) view(ctx screenCtx, width, height int) string {
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
	tabBar := d.renderTabBar(s, innerW)
	headerLines := renderedLineCount(header) + renderedLineCount(tabBar) + 2 // rule under header + blank
	bodyH := height - headerLines
	if bodyH < 3 {
		bodyH = 3
	}
	body := d.renderTabBody(ctx, innerW, bodyH)
	rule := s.Muted.Render(strings.Repeat("─", innerW))
	return strings.Join([]string{header, rule, tabBar, "", body}, "\n")
}

// renderHeader is a compact meta strip:
//
//	row 1: title · date · duration · status
//	row 2+: compact attendee rows, three speakers per row.
//
// Counters and source are intentionally omitted: counters appear in
// the list row, the source kind is implicit, and the user wanted the
// header to stay one ribbon tall.
func (d *detailPane) renderHeader(s theme.Styles, width int) string {
	title := d.meeting.Title
	if title == "" {
		title = "Untitled meeting"
	}
	when := ""
	if !d.meeting.CreatedAt.IsZero() {
		when = d.meeting.CreatedAt.Format("Jan 02 15:04")
	}
	dur := ""
	if d.meeting.DurationSeconds > 0 {
		dur = formatDuration(d.meeting.DurationSeconds)
	}
	line1Parts := []string{s.HeaderEm.Render(fit(title, width-30))}
	if when != "" {
		line1Parts = append(line1Parts, s.Muted.Render(when))
	}
	if dur != "" {
		line1Parts = append(line1Parts, s.Muted.Render(dur))
	}
	line1Parts = append(line1Parts, statusBadge(s, d.meeting.Status))
	line1 := strings.Join(line1Parts, "  ")
	attendees := strings.Split(d.renderAttendeeStrip(s, width), "\n")
	for i, line := range attendees {
		attendees[i] = clipLine(line, width)
	}
	return clipLine(line1, width) + "\n" + strings.Join(attendees, "\n")
}

// renderAttendeeStrip lays out the always-visible speaker overview:
// compact name + talk time entries plus a time distribution graph.
// Rows wrap after three speakers so busy meetings grow vertically
// instead of blowing out the right edge.
func (d *detailPane) renderAttendeeStrip(s theme.Styles, width int) string {
	if len(d.speakers) == 0 {
		// Fall back to the static attendee list while diarization is
		// still loading or when this meeting has no diarization at all.
		if len(d.transcript.Speakers) == 0 {
			if len(d.meeting.Attendees) > 0 {
				return s.Muted.Render("Attendees: ") + strings.Join(d.meeting.Attendees, ", ")
			}
			return s.Muted.Render("Attendees: —")
		}
		names := make([]string, 0, len(d.transcript.Speakers))
		for i, sp := range d.transcript.Speakers {
			label := sp.DisplayName
			if label == "" {
				label = sp.ID
			}
			color := speakerColorForIndex(s.T, i%6)
			names = append(names, lipgloss.NewStyle().Foreground(color).Bold(true).Render(label))
		}
		return speakerRows(s, width, names)
	}
	return d.renderSpeakerOverviewRows(s, width)
}

func speakerRows(s theme.Styles, width int, entries []string) string {
	if len(entries) == 0 {
		return s.Muted.Render("Attendees: —")
	}
	const perRow = 3
	sep := s.Muted.Render(" · ")
	rows := make([]string, 0, (len(entries)+perRow-1)/perRow)
	for i := 0; i < len(entries); i += perRow {
		end := i + perRow
		if end > len(entries) {
			end = len(entries)
		}
		rows = append(rows, clipLine(strings.Join(entries[i:end], sep), width))
	}
	return strings.Join(rows, "\n")
}

func (d *detailPane) renderSpeakerOverviewRows(s theme.Styles, width int) string {
	const perRow = 3
	sep := s.Muted.Render(" · ")
	rows := make([]string, 0, (len(d.speakers)+perRow-1)/perRow)
	for i := 0; i < len(d.speakers); i += perRow {
		end := i + perRow
		if end > len(d.speakers) {
			end = len(d.speakers)
		}
		group := d.speakers[i:end]
		entries := make([]string, 0, len(group))
		for _, sp := range group {
			color := speakerColorForIndex(s.T, sp.colorIdx)
			name := lipgloss.NewStyle().Foreground(color).Bold(true).Render(sp.Name)
			entries = append(entries, name+" "+s.Muted.Render(formatDuration(int(sp.TalkSec))))
		}
		legend := strings.Join(entries, sep)
		timelineW := width - lipgloss.Width(legend) - 2
		if timelineW >= 12 {
			timeline := d.renderInlineTimelineForSpeakers(s, timelineW, group)
			if timeline != "" {
				rows = append(rows, clipLine(legend+"  "+timeline, width))
				continue
			}
		}
		rows = append(rows, clipLine(legend, width))
	}
	return strings.Join(rows, "\n")
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

// colorizeSpeakerNames wraps every occurrence of a known speaker
// display name in that speaker's color. Called before FTS highlighting
// so a hit on a speaker name composes correctly (the FTS yellow wins
// inside the match span).
//
// Match policy: case-insensitive, word-boundaried, longest-first so
// "Paulina" matches before "Paul". Speakers without a display name
// (still labelled by id) are skipped so we don't accidentally color
// "spk_0" strings inside transcript ids.
func (d *detailPane) colorizeSpeakerNames(s theme.Styles, text string) string {
	if text == "" || len(d.speakers) == 0 {
		return text
	}
	type spName struct {
		name  string
		color color.Color
	}
	names := make([]spName, 0, len(d.speakers))
	for _, sp := range d.speakers {
		if sp.Name == "" || strings.HasPrefix(sp.Name, "spk_") || strings.HasPrefix(sp.Name, "speaker_") {
			continue
		}
		names = append(names, spName{
			name:  sp.Name,
			color: speakerColorForIndex(s.T, sp.colorIdx),
		})
	}
	// Longest first so "Paulina" wins over "Paul".
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if len(names[j].name) > len(names[i].name) {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	for _, sp := range names {
		text = replaceWordCaseInsensitive(text, sp.name, func(match string) string {
			return lipgloss.NewStyle().Foreground(sp.color).Bold(true).Render(match)
		})
	}
	return text
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
func renderCounterStrip(s theme.Styles, decisions, actions, risks, questions int) string {
	parts := []string{}
	if decisions > 0 {
		parts = append(parts, s.PanelTitle.Render(fmt.Sprintf("◆ %d %s", decisions, plural(decisions, "decision", "decisions"))))
	}
	if actions > 0 {
		parts = append(parts, s.Info.Render(fmt.Sprintf("▸ %d %s", actions, plural(actions, "action", "actions"))))
	}
	if risks > 0 {
		parts = append(parts, s.Warning.Render(fmt.Sprintf("⚠ %d %s", risks, plural(risks, "risk", "risks"))))
	}
	if questions > 0 {
		parts = append(parts, s.Muted.Render(fmt.Sprintf("? %d %s", questions, plural(questions, "question", "questions"))))
	}
	return strings.Join(parts, "   ")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renderTabBar prints "summary | actions | decisions | …" with the
// active tab highlighted.
func (d *detailPane) renderTabBar(s theme.Styles, width int) string {
	parts := make([]string, 0, tabCount)
	for i, label := range detailTabLabels {
		if detailTab(i) == d.tab {
			parts = append(parts, s.HeaderEm.Render(label))
		} else {
			parts = append(parts, s.Muted.Render(label))
		}
	}
	sep := s.Muted.Render(" │ ")
	return clipLine(strings.Join(parts, sep), width)
}

// renderTabBody dispatches to the active tab's renderer.
func (d *detailPane) renderTabBody(ctx screenCtx, width, height int) string {
	switch d.tab {
	case tabSummary:
		return d.renderSummary(ctx, width)
	case tabActions:
		return d.renderItemsList(ctx, "Action items", actionItemsToSummary(d.summary.ActionItems), width)
	case tabDecisions:
		return d.renderItemsList(ctx, "Decisions", d.summary.Decisions, width)
	case tabRisks:
		return d.renderItemsList(ctx, "Risks", d.summary.Risks, width)
	case tabQuestions:
		return d.renderItemsList(ctx, "Open questions", d.summary.OpenQuestions, width)
	case tabTranscript:
		return d.renderTranscript(ctx, width, height)
	case tabSpeakers:
		return d.renderSpeakers(ctx, width, height)
	}
	return ""
}

func (d *detailPane) renderSummary(ctx screenCtx, width int) string {
	s := ctx.styles
	if d.loadingSummary && d.summary.MeetingID == "" {
		return s.Muted.Render("loading summary…")
	}
	tokens := queryTokens(d.query)
	var lines []string
	switch {
	case d.summary.Markdown != "":
		lines = append(lines, highlightString(s, d.colorizeSpeakerNames(s, d.summary.Markdown), tokens, -1, -1))
	case d.summary.ShortSummary != "":
		lines = append(lines, highlightString(s, d.colorizeSpeakerNames(s, d.summary.ShortSummary), tokens, -1, -1))
	default:
		lines = append(lines, s.Muted.Render("No summary yet. Run `noto summarize` or kick a pipeline job."))
	}
	for i, line := range lines {
		lines[i] = clipLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func (d *detailPane) renderItemsList(ctx screenCtx, title string, items []notoapi.SummaryItem, width int) string {
	s := ctx.styles
	if len(items) == 0 {
		return s.Muted.Render("No " + strings.ToLower(title) + " yet.")
	}
	tokens := queryTokens(d.query)
	rows := []string{s.PanelTitle.Render(title), ""}
	for i, it := range items {
		body := highlightString(s, d.colorizeSpeakerNames(s, it.Text), tokens, -1, -1)
		row := fmt.Sprintf("  %d. %s", i+1, body)
		if len(it.SegmentRefs) > 0 {
			row += "  " + s.Info.Render("["+strings.Join(it.SegmentRefs, ",")+"]")
		}
		rows = append(rows, clipLine(row, width))
	}
	return strings.Join(rows, "\n")
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

// renderTranscript shows speaker-colored segments with FTS highlights
// and a scrollable viewport. Cursor-driven n/N jumps the active
// segment; up/down scrolls one line at a time.
func (d *detailPane) renderTranscript(ctx screenCtx, width, height int) string {
	s := ctx.styles
	segs := d.transcript.Segments
	if len(segs) == 0 {
		if d.loadingTranscript {
			return s.Muted.Render("loading transcript…")
		}
		return s.Muted.Render("no transcript yet — run a pipeline job")
	}
	// 2 lines per segment (header + body) + 1 footer summary line.
	perSeg := 2
	innerH := height - 1
	if innerH < perSeg {
		innerH = perSeg
	}
	segLimit := innerH / perSeg
	if segLimit < 1 {
		segLimit = 1
	}
	start := d.transcriptScroll
	if start > len(segs)-segLimit {
		start = len(segs) - segLimit
	}
	if start < 0 {
		start = 0
	}
	end := start + segLimit
	if end > len(segs) {
		end = len(segs)
	}
	tokens := queryTokens(d.query)
	activeSeg := -1
	if len(d.matchedSegs) > 0 && d.matchCursor < len(d.matchedSegs) {
		activeSeg = d.matchedSegs[d.matchCursor]
	}
	speakerStyle := map[string]int{}
	next := 0
	var rows []string
	for i := start; i < end; i++ {
		seg := segs[i]
		idx, ok := speakerStyle[seg.SpeakerID]
		if !ok {
			idx = next % 3
			speakerStyle[seg.SpeakerID] = idx
			next++
		}
		spStyle := s.SpeakerA
		switch idx {
		case 1:
			spStyle = s.SpeakerB
		case 2:
			spStyle = s.SpeakerC
		}
		header := fmt.Sprintf("%s  %s  %s",
			s.Muted.Render(fmt.Sprintf("[%s]", formatSec(seg.StartSec))),
			spStyle.Render(seg.Speaker),
			s.Citation.Render(seg.ID))
		marker := "    "
		if i == activeSeg {
			marker = s.HighlightActive.Render(" ▸ ") + " "
		}
		header = marker + header
		body := "      " + highlightString(s, d.colorizeSpeakerNames(s, seg.Text), tokens, i, activeSeg)
		rows = append(rows, clipLine(header, width), clipLine(body, width))
	}
	footer := ""
	if start > 0 || end < len(segs) {
		footer = "\n" + s.Muted.Render(fmt.Sprintf("showing %d–%d of %d", start+1, end, len(segs)))
	}
	if len(d.matchedSegs) > 0 {
		footer += s.Muted.Render(fmt.Sprintf("  · hit %d/%d (%s/%s to jump)",
			d.matchCursor+1, len(d.matchedSegs),
			ctx.keys.NextMatch.Help().Key, ctx.keys.PrevMatch.Help().Key))
	}
	return strings.Join(rows, "\n") + footer
}

// renderSpeakers is now a rename editor: each row pairs the
// diarized speaker_id with the current display name and a talk-time
// bar. Up/Down navigates rows; Enter or `e` opens the inline text
// input; the host writes the new name back to transcript.json via
// UpdateSpeakerName.
//
// Salient phrases for the selected speaker stay rendered underneath as
// an aid for "which voice is this?" identification while the user is
// renaming. The colored timeline strip is omitted here — it's already
// in the header.
func (d *detailPane) renderSpeakers(ctx screenCtx, width, height int) string {
	s := ctx.styles
	if len(d.speakers) == 0 {
		if d.loadingTranscript {
			return s.Muted.Render("loading speakers…")
		}
		empty := []string{
			s.Muted.Render("No diarization in this transcript yet."),
			s.Muted.Render("Speaker labels appear after AssemblyAI runs with `speaker_labels=true`."),
		}
		return strings.Join(empty, "\n")
	}
	_ = height
	hintLine := strings.Join([]string{
		chipPair(s, ctx.keys.Up, ctx.keys.Down, "select"),
		chipAs(s, ctx.keys.Edit, "rename"),
		chipAs(s, ctx.keys.Enter, "save"),
		chipAs(s, ctx.keys.Back, "cancel"),
	}, s.Muted.Render("  "))
	list := d.renderSpeakerList(s, width)
	bottom := []string{}
	if d.editorBanner != "" {
		style := s.Muted
		if strings.HasPrefix(d.editorBanner, "save failed") {
			style = s.Danger
		}
		bottom = append(bottom, style.Render(d.editorBanner))
	}
	bottom = append(bottom, "", d.renderSpeakerDetail(s, width))
	return strings.Join(append([]string{clipLine(hintLine, width), "", list}, bottom...), "\n")
}

func (d *detailPane) renderSpeakerList(s theme.Styles, width int) string {
	total := totalTalk(d.speakers)
	if total <= 0 {
		total = 1
	}
	rows := []string{}
	for i, sp := range d.speakers {
		share := sp.TalkSec / total
		barW := max(8, width-44)
		bar := shareBar(s, share, barW, sp.colorIdx)
		color := speakerColorForIndex(s.T, sp.colorIdx)
		coloredName := lipgloss.NewStyle().Foreground(color).Bold(true).Render(fit(sp.Name, max(10, width-barW-24)))
		idHint := s.Muted.Render(fmt.Sprintf("(%s)", sp.ID))
		stat := s.Muted.Render(fmt.Sprintf("%5s  %4.0f%%", formatDuration(int(sp.TalkSec)), share*100))
		line := fmt.Sprintf("%s %s  %s  %s", coloredName, idHint, bar, stat)
		if i == d.speakerCur {
			line = s.RowSelected.Render(" ▸ ") + line
		} else {
			line = "   " + line
		}
		rows = append(rows, clipLine(line, width))
		// Inline editor under the cursor row when active.
		if i == d.speakerCur && d.editorOpen {
			editLine := s.ChipKey.Render(" name › ") + d.editorInput.View()
			rows = append(rows, clipLine("   "+editLine, width))
		}
	}
	return strings.Join(rows, "\n")
}

func (d *detailPane) renderSpeakerDetail(s theme.Styles, width int) string {
	if d.speakerCur >= len(d.speakers) {
		return s.Muted.Render("Select a speaker (↑/↓).")
	}
	sp := d.speakers[d.speakerCur]
	speakerColor := speakerColorForIndex(s.T, sp.colorIdx)
	chip := lipgloss.NewStyle().Foreground(speakerColor).Bold(true).Render("●  " + sp.Name)
	meta := s.Muted.Render(fmt.Sprintf("%s · %d turns · ~%d words", formatDuration(int(sp.TalkSec)), sp.TurnCount, sp.WordCount))
	bullets := []string{}
	for _, ph := range sp.TopPhrases {
		if lipgloss.Width(ph) > width-4 {
			ph = ph[:max(0, width-5)] + "…"
		}
		bullets = append(bullets, "  • "+ph)
	}
	if len(bullets) == 0 {
		bullets = append(bullets, s.Muted.Render("  (no salient phrases yet)"))
	}
	return strings.Join([]string{
		clipLine(chip, width),
		clipLine(meta, width),
		s.PanelTitle.Render("salient phrases"),
		strings.Join(bullets, "\n"),
	}, "\n")
}

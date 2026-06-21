package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/ui/tui/scroll"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// detail_pane_speakers_view.go renders the Speakers tab: the labelled
// identity rollup, the per-speaker list with status badges, and the
// centered "Identify speaker" dialog. The matching behaviour (person
// jump, rename editor, dialog state + keys) lives in detail_pane_identity.go.

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
			s.Muted.Render("Speaker labels appear once diarization has run on the recording."),
		}
		return strings.Join(empty, "\n")
	}
	// The whole tab (hints + rollup + speaker rows + the inline detail) flows
	// through the shared paneBody so a long roster SCROLLS instead of being hidden
	// past the fold, with the selected speaker kept visible. Its rows carry
	// talk-time bars sized to the width, so renderAt rebuilds them at whatever
	// content width paneBody hands in (reserving the scrollbar column) rather than
	// being prose-wrapped.
	rollup := d.identityRollupLine(ctx)
	headLines := 2 // hint + blank
	if rollup != "" {
		headLines = 3
	}
	renderAt := func(w int) string {
		hintLine := strings.Join([]string{
			chipPair(s, ctx.keys.Up, ctx.keys.Down, "select"),
			chipAs(s, ctx.keys.Enter, "open person"),
			chipAs(s, ctx.keys.Assign, "identify"),
			chipAs(s, ctx.keys.Edit, "rename"),
		}, s.Muted.Render("  "))
		head := []string{clipLine(hintLine, w)}
		if rollup != "" {
			head = append(head, clipLine(rollup, w))
		}
		head = append(head, "")
		list := d.renderSpeakerList(ctx, w)
		bottom := []string{}
		if d.editorBanner != "" {
			style := s.Muted
			if strings.HasPrefix(d.editorBanner, "save failed") {
				style = s.Danger
			}
			bottom = append(bottom, style.Render(d.editorBanner))
		}
		bottom = append(bottom, "", d.renderSpeakerDetail(s, w))
		return strings.Join(append(append(head, list), bottom...), "\n")
	}
	// Each head line and each speaker row is clipped to one rendered line, so the
	// selected speaker's row begins at a fixed offset — no wrap mapping needed.
	// Span 2 lines so its inline editor/hint sub-row stays on screen with it.
	focus := func(int) (int, int) { return headLines + d.speakerCur, 2 }
	block, _ := paneBody(s, width, height, 0, focus, renderAt)
	return block
}

// speakersToID counts this meeting's speakers that still need identity work.
// Drives the Speakers tab badge and the rollup line's action count.
func (d *detailPane) speakersToID() int {
	n := 0
	for _, mp := range d.mappings {
		if _, needs := actionForStatus(mp.MatchStatus); needs {
			n++
		}
	}
	return n
}

// tabAttention returns how many flagged items live in a tab, so the tab bar can
// badge the one(s) that need action. Only the Speakers tab carries work today,
// but the tab bar reads this generically so other tabs can flag later.
func (d *detailPane) tabAttention(t detailTab) int {
	if t == tabSpeakers {
		return d.speakersToID()
	}
	return 0
}

// identityRollupLine leads with the actionable count ("⚑ N to identify") so the
// Speakers tab opens stating the work, then the resolved tally. Empty when there
// are no mappings for this meeting; a clean "✓ all set" when nothing's pending.
func (d *detailPane) identityRollupLine(ctx screenCtx) string {
	s := ctx.styles
	if len(d.mappings) == 0 {
		return ""
	}
	resolved, toID := 0, 0
	for _, mp := range d.mappings {
		if _, needs := actionForStatus(mp.MatchStatus); needs {
			toID++
		} else {
			resolved++
		}
	}
	// Lead with the work only — successes aren't worth a chip per the "show
	// issues, not successes" rule. The one success state worth stating is the
	// fully-done one (nothing left to identify).
	if toID == 0 {
		return s.Success.Render(fmt.Sprintf("✓ all %d set", resolved))
	}
	return s.BadgeWarn.Render(fmt.Sprintf("⚑ %d to identify", toID))
}

func (d *detailPane) renderSpeakerList(ctx screenCtx, width int) string {
	s := ctx.styles
	total := totalTalk(d.speakers)
	if total <= 0 {
		total = 1
	}
	rows := []string{}
	for i, sp := range d.speakers {
		share := sp.TalkSec / total
		barW := max(8, width-52)
		nameW := max(10, width-barW-23)
		bar := shareBar(s, share, barW, sp.colorIdx)

		// Resolve once: ● swatch + the name at its confidence tier (bold person,
		// faint-italic ~guess?, or the plain token). fit() the PLAIN decorated
		// string so the column width is stable, then style.
		name, tier := d.resolveSpeaker(sp.ID)
		coloredName := tierStyle(s, sp.colorIdx, tier).Render(fit(decorateName(name, tier), nameW))

		// Only the orange attention flag — no "✓ set" success badge, so resolved
		// rows stay quiet. A fixed 2-cell column keeps bars + stats aligned across
		// every row regardless of state.
		flag := "  "
		attention := false
		if mp, ok := d.mappings[sp.ID]; ok {
			if _, needs := actionForStatus(mp.MatchStatus); needs {
				attention = true
				flag = s.BadgeWarn.Render("⚑") + " "
			}
		}
		stat := s.Muted.Render(fmt.Sprintf("%5s  %4.0f%%", formatDuration(int(sp.TalkSec)), share*100))
		line := speakerSwatch(s, sp.colorIdx) + " " + coloredName + " " + flag + " " + bar + "  " + stat
		line = attnGutter(s, i == d.speakerCur, attention) + line
		rows = append(rows, clipLine(line, width))
		// Under the cursor row: the inline rename editor, or a one-line "who is
		// this?" nudge for an unresolved speaker. (The identify/reassign dialog
		// is a centered overlay, composited by the dashboard — not inline here.)
		switch {
		case i == d.speakerCur && d.editorOpen:
			editLine := s.ChipKey.Render(" name › ") + d.editorInput.View()
			rows = append(rows, clipLine("   "+editLine, width))
		case i == d.speakerCur:
			if hint := d.unresolvedHint(ctx); hint != "" {
				rows = append(rows, clipLine(hint, width))
			}
		}
	}
	return strings.Join(rows, "\n")
}

// unresolvedHint nudges the user to identify an unresolved speaker, naming the
// top suggestion if there is one.
func (d *detailPane) unresolvedHint(ctx screenCtx) string {
	s := ctx.styles
	cands := d.currentCandidates()
	if len(cands) == 0 {
		mp, ok := d.mappings[d.speakers[d.speakerCur].ID]
		if !ok || mp.ProfileID != nil {
			return ""
		}
		return "     " + s.Muted.Render("unknown — ") + chipAs(s, ctx.keys.Edit, "name them")
	}
	top := cands[0]
	return "     " + s.Muted.Render(fmt.Sprintf("likely %s (%.2f) — ", top.DisplayName, top.Score)) +
		chipAs(s, ctx.keys.Assign, "pick")
}

// assignDialogView renders the centered "Identify speaker" overlay: a search
// box over the people directory with the ranked candidates pinned on top, a
// create-new action, and the currently-linked person flagged. Like the other
// overlays, every span is backed with the surface color so the rounded box
// reads as one panel; the dashboard composites it over the dimmed screen.
func (d *detailPane) assignDialogView(ctx screenCtx) string {
	s := ctx.styles
	o := newOverlaySurface(s)
	bg, titleS, mutedS, rowS, keyS := o.BG, o.Title, o.Muted, o.Body, o.Key

	var sp speakerStat
	if d.speakerCur < len(d.speakers) {
		sp = d.speakers[d.speakerCur]
	}
	boxW := min(ctx.width-6, 60)
	innerW := max(20, boxW-6)

	lines := []string{titleS.Render(fit(`Identify "`+def(sp.Name, sp.ID)+`"`, innerW))}
	if mp, ok := d.mappings[sp.ID]; ok && mp.ProfileName != "" {
		lines = append(lines, mutedS.Render("currently ")+rowS.Render(fit(mp.ProfileName, innerW-10)))
	}
	// Search field — a backed query string with a block caret (a real text
	// input would punch a black hole in the surface).
	lines = append(lines, "", keyS.Render(" search ")+rowS.Render(fit(d.assignQuery, innerW-9))+s.RowSelected.Render(" "))

	rows := d.assignRows()
	lines = append(lines, "")
	if d.dirLoading && len(d.directory) == 0 {
		lines = append(lines, mutedS.Render("  loading people…"))
	}
	// Window the list around the cursor so long directories stay in the box —
	// the same cursor-follow primitive the meeting list and palette use, so the
	// dialog scrolls exactly like every other list in the app.
	const visible = 8
	start := scroll.Follow(len(rows), visible, d.assignCur, 1, 0)
	end := min(len(rows), start+visible)
	for i := start; i < end; i++ {
		r := rows[i]
		label := d.assignRowLabel(r)
		if i == d.assignCur {
			lines = append(lines, s.RowSelected.Render(" ▸ "+fit(label, innerW-3)))
		} else {
			lines = append(lines, bg.Render("   ")+rowS.Render(fit(label, innerW-3)))
		}
	}
	if start+visible < len(rows) {
		lines = append(lines, mutedS.Render(fmt.Sprintf("  +%d more — keep typing to filter", len(rows)-(start+visible))))
	}

	lines = append(lines, "",
		mutedS.Render("↑/↓ select · ")+keyS.Render(ctx.keys.Enter.Help().Key)+mutedS.Render(" choose · type to filter · ")+
			keyS.Render(ctx.keys.Back.Help().Key)+mutedS.Render(" cancel"))

	return s.OverlayBox.Width(boxW).Render(strings.Join(lines, "\n"))
}

// assignRowLabel formats one dialog row: the create action, or a person with
// optional score/reason (candidates) and a "current" flag.
func (d *detailPane) assignRowLabel(r assignRow) string {
	if r.create {
		if q := strings.TrimSpace(d.assignQuery); q != "" {
			return "＋ create new person “" + q + "”"
		}
		return "＋ create new person"
	}
	label := def(r.name, "(unnamed)")
	if r.candidate != nil {
		label += fmt.Sprintf("  %.2f", r.candidate.Score)
		if r.candidate.Reason != "" {
			label += " · " + r.candidate.Reason
		}
	}
	if r.current {
		label += "  ✓ current"
	}
	return label
}

func (d *detailPane) renderSpeakerDetail(s theme.Styles, width int) string {
	if d.speakerCur >= len(d.speakers) {
		return s.Muted.Render("Select a speaker (↑/↓).")
	}
	sp := d.speakers[d.speakerCur]
	name, tier := d.resolveSpeaker(sp.ID)
	chip := speakerSwatch(s, sp.colorIdx) + "  " + styleSpeaker(s, name, sp.colorIdx, tier)
	meta := s.Muted.Render(fmt.Sprintf("%s · %d %s · ~%d words", formatDuration(int(sp.TalkSec)), sp.TurnCount, plural(sp.TurnCount, "turn", "turns"), sp.WordCount))
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

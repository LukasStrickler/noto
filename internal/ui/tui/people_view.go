package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/hit"
	"github.com/lukasstrickler/noto/internal/ui/tui/keys"
	"github.com/lukasstrickler/noto/internal/ui/tui/layout"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// people_view.go is the rendering half of the People screen: the directory
// list, the person detail/edit panes, the destructive-action confirm
// overlay, and the small status-badge/wrap helpers. The model, key
// handling, and edit-form logic live in screen_people.go.

func (p *peopleScreen) view(ctx screenCtx) string {
	s := ctx.styles
	if p.loading {
		return panelEmpty(ctx, "people", s.Muted.Render("loading…"))
	}
	if p.err != nil {
		return panelEmpty(ctx, "people", s.Warning.Render(p.err.Error()))
	}

	var base string
	bp := layout.BreakpointFor(ctx.width)
	if bp == layout.Narrow {
		listP := layout.Panel{
			Title: "people", Subtitle: fmt.Sprintf("%d people", len(p.profiles)),
			Width: ctx.width, Height: ctx.height, Focused: true,
		}
		ox, oy := listP.BodyOffset()
		listBody := p.renderList(ctx, ctx.width-6, ox, ctx.bodyTop+oy)
		// The detail block follows the list and a 2-line gap inside the same
		// panel body, so its click regions start that many rows lower.
		detailY := ctx.bodyTop + oy + lipgloss.Height(listBody) + 2
		detailBody := p.renderDetail(ctx, ctx.width-6, ox, detailY)
		listP.Body = listBody + "\n\n" + detailBody
		base = listP.Render(s)
	} else {
		leftW, rightW := layout.SidebarSplit(ctx.width, sidebarPref(ctx), minSidebarW, minContentW)
		// The directory holds focus unless we're editing or browsing the
		// person's meetings — then the detail pane is the active surface.
		detailFocused := p.editing || p.focusMeetings
		leftP := layout.Panel{
			Title: "people", Subtitle: fmt.Sprintf("%d", len(p.profiles)),
			Width: leftW, Height: ctx.height, Focused: !detailFocused,
		}
		lox, loy := leftP.BodyOffset()
		leftP.Body = p.renderList(ctx, leftW-4, lox, ctx.bodyTop+loy)
		left := leftP.Render(s)
		rightTitle := "details"
		if p.editing {
			rightTitle = "edit person"
		}
		rightP := layout.Panel{
			Title: rightTitle, Subtitle: p.detailSubtitle(),
			Width: rightW, Height: ctx.height, Focused: detailFocused,
		}
		rox, roy := rightP.BodyOffset()
		rightP.Body = p.renderDetail(ctx, rightW-4, (leftW+1)+rox, ctx.bodyTop+roy)
		right := rightP.Render(s)
		base = joinSidebar(ctx, left, right, leftW, ctx.height)
	}

	if p.confirm != confirmNone {
		return overlayCenter(base, p.confirmBox(ctx), ctx.width, ctx.height, s.T.Faint)
	}
	return base
}

func (p *peopleScreen) detailSubtitle() string {
	switch {
	case p.merging:
		return "pick survivor · ⏎ merge · esc cancel"
	case p.focusMeetings:
		return "↑/↓ meeting · ⏎ open · tab back"
	}
	return ""
}

// renderList draws the directory. originX/originY are the absolute screen
// coordinates of the body's first cell so each person row can register a click
// region (select that person; pick the survivor while merging) and light up on
// hover. Click wiring is skipped while an edit form or confirm overlay owns
// input, so a stray click can't act behind them.
func (p *peopleScreen) renderList(ctx screenCtx, width, originX, originY int) string {
	s, k := ctx.styles, ctx.keys
	clickable := !p.inputActive()
	if len(p.profiles) == 0 {
		return strings.Join([]string{
			s.HeaderEm.Render("No people yet"),
			"",
			s.Muted.Render("People appear here once a meeting is"),
			s.Muted.Render("transcribed and its speakers are matched."),
		}, "\n")
	}
	// Header doubles as the attention notification: when any auto-created
	// person hasn't been reviewed yet, a "⚑ N to review" badge sits next to
	// the title so there's a visible reason to be on this page.
	headerLine := s.HeaderEm.Render("Directory")
	if p.merging {
		headerLine = s.HeaderEm.Render("Merge — pick who survives")
	} else if n := peopleToReview(p.profiles); n > 0 {
		headerLine += "   " + s.BadgeWarn.Render(fmt.Sprintf("⚑ %d to review", n))
	}
	// Two aligned columns: name (flex) + a right-aligned meta cell, so the
	// "N mtg · date" column lines up no matter the name length. nameW is set from
	// the content width rowList settles on (one cell narrower when the scrollbar
	// claims its column).
	const metaW = 15
	attnBar := s.Warning.Render("▍") + "  " // 3 cells, matches the "   " gutter
	var nameW int
	// The directory is the one scrollable region; rowList windows it (rendering
	// only the visible rows), reserves the scrollbar column and registers a click
	// region per row. The bespoke per-row look — the merge source/survivor states,
	// the amber review bar, the cursor ▸ and selection fill — stays here.
	list := rowList{
		ctx: ctx, width: width, height: p.listRowBudget(ctx),
		originX: originX, originY: originY + 2, // header line + blank
		count: len(p.profiles), cursor: p.cursor, offset: p.listScroll, clickable: clickable,
		lineCount: func(int) int { return 1 },
		rowID:     func(i int) string { return fmt.Sprintf("people:row:%d", i) },
		onClick:   func(i int) tea.Cmd { return p.selectAt(ctx, i) },
		prepare:   func(contentW int) { nameW = max(8, contentW-metaW-4) },
		render: func(i, contentW int, st uiState) []string {
			prof := p.profiles[i]
			name := fit(def(prof.DisplayName, "(unnamed)"), nameW)
			meta := fmt.Sprintf("%*s", metaW, peopleMeta(prof))
			body := name + " " + meta
			review := !p.merging && profileNeedsReview(prof)
			var row string
			switch {
			case p.merging && i == p.cursor: // the source being folded in
				row = s.Warning.Render(" ⤷ "+name+" ") + s.Muted.Render("source")
			case p.merging && i == p.mergeIdx: // the survivor under the cursor
				row = s.RowSelected.Render(" ▸ " + body)
			case !p.merging && i == p.cursor:
				if review {
					// Keep the amber bar beside the blue cursor so the flag stays
					// visible when you move onto the row (it used to disappear).
					row = s.Warning.Render("▍") + s.RowSelected.Render("▸ "+body)
				} else {
					row = s.RowSelected.Render(" ▸ " + body)
				}
			case review:
				// Auto-created, not yet reviewed: amber bar + amber name pull the
				// eye to the people that still need naming/confirming.
				row = attnBar + s.Warning.Render(name) + " " + s.Muted.Render(meta)
			default:
				row = "   " + s.Row.Render(name) + " " + s.Muted.Render(meta)
			}
			protect := 0
			if review {
				protect = 1 // keep the amber ▍ beside the selection fill
			}
			return []string{rowFeedback(clipLine(row, contentW), st, s, contentW, protect)}
		},
	}

	rows := []string{headerLine, "", list.view(), ""}
	if p.merging {
		rows = append(rows,
			"  "+chipPair(s, k.Up, k.Down, "survivor"),
			"  "+chipAs(s, k.Enter, "merge")+"   "+chipAs(s, k.Back, "cancel"),
		)
	} else {
		rows = append(rows,
			"  "+chipPair(s, k.Up, k.Down, "select"),
			"  "+chipPair(s, k.Edit, k.Delete, "edit/delete")+"   "+chip(s, k.Merge),
		)
	}
	return strings.Join(rows, "\n")
}

// listRowBudget is how many directory rows fit in the scroll viewport — the SAME
// arithmetic the view and Update (followCursor / wheel) must agree on. Wide: the
// left panel's body (Height − 4 panel chrome) minus the header (2 lines) and the
// hint chips (3 lines). Narrow stacks the list above the detail in one panel, so
// the list isn't independently scrolled there — it renders every row (the panel
// clips, as before), which a budget ≥ len makes a no-op window.
func (p *peopleScreen) listRowBudget(ctx screenCtx) int {
	if layout.BreakpointFor(ctx.width) == layout.Narrow {
		return max(1, len(p.profiles))
	}
	return max(1, ctx.height-9)
}

// profileNeedsReview reports whether a person was auto-created/linked but not
// yet confirmed (a "new"/pending mapping) — the People-page attention signal.
func profileNeedsReview(p notoapi.SpeakerProfile) bool { return p.UnconfirmedMeetings > 0 }

// peopleToReview counts how many people still need review, for the directory's
// "⚑ N to review" notification.
func peopleToReview(profiles []notoapi.SpeakerProfile) int {
	n := 0
	for _, p := range profiles {
		if profileNeedsReview(p) {
			n++
		}
	}
	return n
}

// peopleMeta is the right-hand column for a directory row: meeting count and,
// when known, when the person was last seen.
func peopleMeta(prof notoapi.SpeakerProfile) string {
	meta := fmt.Sprintf("%d mtg", prof.MeetingCount)
	if prof.LastSeenAt != nil {
		meta += " · " + prof.LastSeenAt.Format("Jan 02")
	}
	return meta
}

// renderDetail draws the selected person. originX/originY locate the body's
// first cell so the meeting rows (click to jump to that meeting) and the action
// chips (click replays the key) register clickable regions; hover lights them.
func (p *peopleScreen) renderDetail(ctx screenCtx, width, originX, originY int) string {
	s, k := ctx.styles, ctx.keys
	clickable := !p.inputActive()
	ptr := pointer{}
	if clickable {
		ptr = ctx.pointer()
	}
	if p.detailID == "" {
		return s.Muted.Render("select a person")
	}
	if p.editing {
		return p.renderEditForm(s, k, width)
	}

	d := p.detail
	rows := []string{s.HeaderEm.Render(def(d.DisplayName, "(unnamed)"))}
	if sub := personSubtitle(d); sub != "" {
		rows = append(rows, s.Muted.Render(fit(sub, width)))
	}
	if profileNeedsReview(d) {
		// Auto-created and unconfirmed — push the user to act on it right here.
		rows = append(rows, s.BadgeWarn.Render(fit("⚑ auto-created — review & confirm ("+k.Edit.Help().Key+" to edit)", width)))
	}

	if d.Notes != "" {
		rows = append(rows, "", s.HeaderEm.Render("Notes"))
		for _, ln := range wrapLines(d.Notes, width-2) {
			rows = append(rows, "  "+s.Row.Render(ln))
		}
	}

	rows = append(rows, "", s.HeaderEm.Render("Affiliations"))
	if len(d.Affiliations) == 0 {
		rows = append(rows, "  "+s.Muted.Render("none yet — press e to add one"))
	}
	for _, a := range d.Affiliations {
		head := def(a.Context, "—")
		if a.Organization != "" {
			head += " · " + a.Organization
		}
		rows = append(rows, "  "+s.Row.Render(fit(head, max(8, width-2))))
		if a.Email != "" {
			rows = append(rows, "    "+s.Muted.Render(fit(a.Email, max(8, width-4))))
		}
	}

	rows = append(rows, "", s.HeaderEm.Render(fmt.Sprintf("Meetings (%d)", d.MeetingCount)))
	switch {
	case p.detailLoad:
		rows = append(rows, "  "+s.Muted.Render("loading…"))
	case len(d.Meetings) == 0:
		rows = append(rows, "  "+s.Muted.Render("none yet"))
	default:
		for i, mtg := range d.Meetings {
			when := mtg.CreatedAt.Format("Jan 02")
			badge := speakerStatusBadge(s, mtg.MatchStatus)
			title := fit(def(mtg.Title, mtg.MeetingID), max(8, width-22))
			line := fmt.Sprintf("%s  %s  %s", s.Muted.Render(when), s.Row.Render(title), badge)
			marker := "   "
			if p.focusMeetings && i == p.meetingCur {
				marker = s.RowSelected.Render(" ▸ ")
			}
			full := clipLine(marker+line, width)
			if clickable {
				i := i
				id := fmt.Sprintf("people:mtg:%d", i)
				full = rowFeedback(full, ptr.state(id, p.focusMeetings && i == p.meetingCur), s, width, 0)
				ctx.hits.Add(hit.Rect{X: originX, Y: originY + len(rows), W: width, H: 1},
					region{id: id, onClick: func() tea.Cmd { return p.openMeetingAt(i) }})
			}
			rows = append(rows, full)
		}
	}

	rows = append(rows, "")
	if p.focusMeetings {
		rows = append(rows, "  "+chipPair(s, k.Up, k.Down, "meeting")+
			"   "+chipAs(s, k.Enter, "open")+"   "+chipAs(s, k.Tab, "back"))
	} else {
		// Single-key action chips, each clickable (click replays its key). The
		// row builder tracks columns from originX so the regions line up with the
		// rendered chips with no offset math here.
		var hits *hit.Map[region]
		if clickable {
			hits = ctx.hits
		}
		row := hit.NewRow(hits, originX, originY+len(rows))
		row.Add("  ")
		chipButton(row, ptr, s, "people:act:edit", k.Edit)
		row.Add("   ")
		chipButton(row, ptr, s, "people:act:delete", k.Delete)
		row.Add("   ")
		chipButton(row, ptr, s, "people:act:merge", k.Merge)
		if len(d.Meetings) > 0 {
			row.Add("   ")
			chipButtonAs(row, ptr, s, "people:act:meetings", k.Tab, "meetings")
		}
		rows = append(rows, row.String())
	}
	return strings.Join(rows, "\n")
}

// confirmBox renders the centered confirmation overlay for a destructive
// action (delete / merge). Like the help overlay, every span is backed with
// the overlay surface so the rounded box reads as one continuous panel rather
// than punching black gaps between styled runs.
func (p *peopleScreen) confirmBox(ctx screenCtx) string {
	s := ctx.styles
	o := newOverlaySurface(s)
	bg, titleS, bodyS, mutedS, keyS := o.BG, o.Title, o.Body, o.Muted, o.Key

	var title, body string
	switch p.confirm {
	case confirmDelete:
		name := def(p.detail.DisplayName, "this person")
		title = "Delete " + name + "?"
		body = "Removes the person and unlinks them from every meeting they appear in. Their voice match is forgotten. This can't be undone."
	case confirmMerge:
		var sourceName, targetName string
		if p.cursor >= 0 && p.cursor < len(p.profiles) {
			sourceName = def(p.profiles[p.cursor].DisplayName, "(unnamed)")
		}
		if p.mergeIdx >= 0 && p.mergeIdx < len(p.profiles) {
			targetName = def(p.profiles[p.mergeIdx].DisplayName, "(unnamed)")
		}
		title = "Merge into " + targetName + "?"
		body = fmt.Sprintf("Folds %s into %s: their meetings and voiceprints move over and %s is deleted. This can't be undone.",
			sourceName, targetName, sourceName)
	}

	// OverlayBox draws a 1-cell border and 2-cell horizontal padding each side,
	// so the usable text column is boxW-6. Wrap to exactly that so lipgloss
	// doesn't re-wrap a too-long line and orphan a word.
	boxW := min(ctx.width-6, 54)
	wrapW := max(8, boxW-6)
	lines := []string{titleS.Render(fit(title, wrapW)), ""}
	for _, ln := range wrapLines(body, wrapW) {
		lines = append(lines, bodyS.Render(ln))
	}
	lines = append(lines, "",
		bg.Render("  ")+keyS.Render(ctx.keys.Enter.Help().Key)+bg.Render(" ")+mutedS.Render("confirm")+
			bg.Render("    ")+keyS.Render(ctx.keys.Back.Help().Key)+bg.Render(" ")+mutedS.Render("cancel"))

	return s.OverlayBox.Width(boxW).Render(strings.Join(lines, "\n"))
}

// personSubtitle is the one-line "she/her · Work@noto" header line: pronouns
// plus the primary affiliation, whichever are present.
func personSubtitle(d notoapi.SpeakerProfile) string {
	parts := []string{}
	if d.Pronouns != "" {
		parts = append(parts, d.Pronouns)
	}
	if len(d.Affiliations) > 0 {
		a := d.Affiliations[0]
		switch {
		case a.Organization != "" && a.Context != "":
			parts = append(parts, a.Context+" @ "+a.Organization)
		case a.Organization != "":
			parts = append(parts, a.Organization)
		case a.Context != "":
			parts = append(parts, a.Context)
		}
	}
	return strings.Join(parts, "  ·  ")
}

func (p *peopleScreen) renderEditForm(s theme.Styles, _ keys.Map, width int) string {
	rows := []string{s.HeaderEm.Render("Editing " + def(p.detail.DisplayName, "person")), ""}
	const labelW = 22
	valW := max(8, width-labelW-4)
	for i, f := range p.form {
		focused := i == p.fieldIdx
		marker := "   "
		if focused {
			marker = s.RowSelected.Render(" ▸ ")
		}
		if f.kind == ffAddAff {
			label := f.label
			if focused {
				label = s.HeaderEm.Render(label)
			} else {
				label = s.Muted.Render(label)
			}
			rows = append(rows, marker+label)
			continue
		}
		label := s.Muted.Render(fmt.Sprintf("%-*s", labelW, f.label))
		var val string
		if focused {
			val = p.input.View()
		} else {
			val = s.Row.Render(fit(def(p.formVals[i], "—"), valW))
		}
		rows = append(rows, marker+label+"  "+val)
	}
	rows = append(rows, "",
		s.Muted.Render("↑/↓ field · type to edit · ⏎ save · esc cancel"),
		s.Muted.Render("clear an affiliation's cells to drop it"),
	)
	return strings.Join(rows, "\n")
}

// wrapLines greedily word-wraps text to width, returning one entry per line.
func wrapLines(text string, width int) []string {
	if width < 1 {
		width = 1
	}
	var out []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case len(line)+1+len(word) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// speakerStatusBadge renders a meeting-speaker match status as a colored chip,
// shared by the People detail and the Speakers assignment tab.
func speakerStatusBadge(s theme.Styles, status string) string {
	switch status {
	case "auto":
		return s.BadgeOK.Render("✓ auto")
	case "manual":
		return s.BadgeOK.Render("✓ set")
	case "pending":
		return s.BadgeWarn.Render("pending")
	case "new":
		return s.BadgeInfo.Render("new")
	case "", "unmatched":
		return s.Muted.Render("unmatched")
	default:
		return s.Muted.Render(status)
	}
}

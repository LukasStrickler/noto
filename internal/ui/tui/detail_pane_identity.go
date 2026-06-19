package tui

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// detail_pane_identity.go holds the Speakers-tab cross-meeting identity
// affordances: jumping to a person page, the inline rename editor, and the
// centered "Identify speaker" dialog (search + reassign + create). The pane
// model, lifecycle, and transcript-search live in detail_pane.go.

// --- Speakers-tab identity helpers ---

// openPerson is the "jump to this speaker's person page" action (Enter):
//   - linked speaker  → open that person on the People screen (view mode);
//   - unresolved (has a voiceprint) → create a person seeded from it, then open
//     it on the People screen in edit mode so the user can name it;
//   - no identity layer at all → fall back to the inline cosmetic rename.
//
// The returned cmd emits either a navigation (switchScreenMsg, handled by the
// root router) or a speakerPersonOpenMsg the pane turns into a navigation once
// the new profile id is known.
func (d *detailPane) openPerson(ctx screenCtx) tea.Cmd {
	sp := d.speakers[d.speakerCur]
	mp, hasMapping := d.mappings[sp.ID]
	switch {
	case hasMapping && mp.ProfileID != nil && *mp.ProfileID != "":
		return pushOrReplaceTo(sPeople, "person:"+*mp.ProfileID)
	case hasMapping:
		d.editorPending = true
		d.editorBanner = "creating person…"
		return createPersonForSpeakerOpenCmd(ctx, d.id_, sp.ID, d.speakerDisplayName(sp))
	default:
		d.openEditor()
		return nil
	}
}

// openEditor opens the name editor for the selected speaker, pre-filling the
// current name and remembering the linked profile (if any) so commit knows
// whether to rename a person or create one.
func (d *detailPane) openEditor() {
	sp := d.speakers[d.speakerCur]
	d.editingID = sp.ID
	d.editorProfileID = ""
	if mp, ok := d.mappings[sp.ID]; ok && mp.ProfileID != nil {
		d.editorProfileID = *mp.ProfileID
	}
	d.editorInput.SetValue(d.speakerDisplayName(sp))
	d.editorInput.CursorEnd()
	d.editorInput.Focus()
	d.editorOpen = true
	d.editorBanner = ""
}

func (d *detailPane) closeEditor() {
	d.editorOpen = false
	d.editorInput.Blur()
	d.editorInput.SetValue("")
	d.editingID = ""
	d.editorProfileID = ""
}

// commitEditor persists the typed name. With a linked profile it renames that
// person (propagating everywhere); with a mapping but no profile it creates a
// new person from the voiceprint; with no mapping at all it falls back to the
// cosmetic transcript relabel.
func (d *detailPane) commitEditor(ctx screenCtx) tea.Cmd {
	name := strings.TrimSpace(d.editorInput.Value())
	sid := d.editingID
	pid := d.editorProfileID
	_, hasMapping := d.mappings[sid]
	d.closeEditor()
	if sid == "" || name == "" {
		return nil
	}
	d.editorPending = true
	d.editorBanner = "saving…"
	switch {
	case pid != "":
		return renameLinkedProfileCmd(ctx, d.id_, sid, pid, name)
	case hasMapping:
		return createPersonForSpeakerCmd(ctx, d.id_, sid, name)
	default:
		return saveSpeakerNameCmd(ctx, d.id_, sid, name)
	}
}

// assignRow is one selectable line in the identify dialog: an existing person
// (profileID set), optionally a ranked candidate (carries score/reason and a
// digit shortcut) or the currently-linked one, or the pinned create-new action.
type assignRow struct {
	profileID string
	name      string
	candidate *notoapi.SpeakerCandidate
	current   bool
	create    bool
}

// openAssignDialog opens the identify/reassign dialog for the selected speaker
// and (re)loads the people directory. Gated to speakers that have a mapping —
// assigning needs a row to patch and a voiceprint to seed a new person from.
func (d *detailPane) openAssignDialog(ctx screenCtx) tea.Cmd {
	if d.speakerCur >= len(d.speakers) {
		return nil
	}
	if _, ok := d.mappings[d.speakers[d.speakerCur].ID]; !ok {
		return nil
	}
	d.assignOpen = true
	d.assignQuery = ""
	d.assignCur = 0
	d.dirLoading = true
	return fetchProfiles(ctx) // refresh so a just-created person shows up
}

func (d *detailPane) closeAssignDialog() {
	d.assignOpen = false
	d.assignQuery = ""
	d.assignCur = 0
}

func (d *detailPane) assignDialogOpen() bool { return d.assignOpen }

// assignRows builds the dialog's ordered, query-filtered list: ranked
// candidates first (so the engine's guesses stay one keystroke away), then the
// rest of the directory, then the create-new action. The currently-linked
// person is flagged so a reassign reads clearly.
func (d *detailPane) assignRows() []assignRow {
	sp := d.speakers[d.speakerCur]
	mp := d.mappings[sp.ID]
	curID := ""
	if mp.ProfileID != nil {
		curID = *mp.ProfileID
	}
	q := strings.ToLower(strings.TrimSpace(d.assignQuery))
	match := func(name string) bool { return q == "" || strings.Contains(strings.ToLower(name), q) }

	var rows []assignRow
	seen := map[string]bool{}
	for i := range mp.Candidates {
		c := mp.Candidates[i]
		if !match(c.DisplayName) {
			continue
		}
		rows = append(rows, assignRow{profileID: c.ProfileID, name: c.DisplayName, candidate: &mp.Candidates[i], current: c.ProfileID == curID})
		seen[c.ProfileID] = true
	}
	for i := range d.directory {
		p := d.directory[i]
		if seen[p.ID] || !match(p.DisplayName) {
			continue
		}
		rows = append(rows, assignRow{profileID: p.ID, name: p.DisplayName, current: p.ID == curID})
		seen[p.ID] = true
	}
	rows = append(rows, assignRow{create: true})
	return rows
}

// handleAssignKey drives the dialog: arrows move the selection, Enter acts on
// it (assign / reassign / create), 1-3 quick-pick a ranked candidate while the
// search is empty, esc closes, and any other character filters the directory.
func (d *detailPane) handleAssignKey(ctx screenCtx, k tea.KeyPressMsg) (bool, tea.Cmd) {
	if isEscapeKey(k) {
		d.closeAssignDialog()
		return true, nil
	}
	rows := d.assignRows()
	switch {
	case key.Matches(k, ctx.keys.Up):
		if d.assignCur > 0 {
			d.assignCur--
		}
		return true, nil
	case key.Matches(k, ctx.keys.Down):
		if d.assignCur < len(rows)-1 {
			d.assignCur++
		}
		return true, nil
	case key.Matches(k, ctx.keys.Enter):
		return true, d.commitAssignDialog(ctx, rows)
	}
	// Quick-pick a ranked candidate by number while not filtering.
	if d.assignQuery == "" && len(k.Text) == 1 && k.Text >= "1" && k.Text <= "3" {
		idx := int(k.Text[0] - '1')
		if idx < len(rows) && rows[idx].candidate != nil {
			d.assignCur = idx
			return true, d.commitAssignDialog(ctx, rows)
		}
	}
	// Otherwise treat the key as search input.
	switch {
	case k.Code == tea.KeyBackspace:
		if r := []rune(d.assignQuery); len(r) > 0 {
			d.assignQuery = string(r[:len(r)-1])
			d.assignCur = 0
		}
	case k.Text != "":
		d.assignQuery += k.Text
		d.assignCur = 0
	}
	return true, nil
}

// commitAssignDialog acts on the selected row: create a new person seeded from
// the voiceprint (named with the typed query, or the speaker label if blank),
// or (re)assign to the chosen existing person. Both reuse the existing commands
// and overwrite an existing mapping, so this is also the correction path.
func (d *detailPane) commitAssignDialog(ctx screenCtx, rows []assignRow) tea.Cmd {
	if d.assignCur < 0 || d.assignCur >= len(rows) || d.speakerCur >= len(d.speakers) {
		return nil
	}
	row := rows[d.assignCur]
	sid := d.speakers[d.speakerCur].ID
	query := strings.TrimSpace(d.assignQuery)
	d.closeAssignDialog()

	if row.create {
		name := query
		if name == "" {
			name = d.speakerDisplayName(d.speakers[d.speakerCur])
		}
		d.editorPending = true
		d.editorBanner = "creating " + name + "…"
		return createPersonForSpeakerCmd(ctx, d.id_, sid, name)
	}
	if row.profileID == "" {
		return nil
	}
	d.editorPending = true
	d.editorBanner = "assigning…"
	return assignSpeakerCmd(ctx, d.id_, sid, row.profileID, row.name)
}

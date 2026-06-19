package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/scroll"
)

// peopleScreen (nav slot 2) is the person directory: the human-facing surface
// for the cross-meeting speaker-identity engine. Left pane lists every known
// person (name · #meetings · last seen); the right pane shows the selected
// person's details — pronouns, notes, the multi-context affiliations, and the
// meetings they appear in — and is also the in-place editor.
//
// One profile is one person across all meetings, so renaming or editing here
// propagates everywhere their voice was matched ("name once → everywhere").
//
// Modes are mutually exclusive: view (navigate the list), edit (a field form
// over the selected person), merge (pick a survivor to fold the selection
// into), and a confirm overlay for the destructive actions (merge / delete).
// Edit and the confirm overlay both capture keys, so inputActive() is true in
// those two states.
//
// Within view mode, Tab moves focus into the detail's meetings list, where
// Enter jumps to the meeting on the dashboard for quick reference.
type peopleScreen struct {
	profiles []notoapi.SpeakerProfile
	loading  bool
	err      error

	cursor int // index into profiles (selected person)

	// listScroll is the directory's free-scroll line offset (the wheel pans it;
	// the arrows follow the cursor into it) — the same scroll model the meeting
	// list uses, so both directories behave identically.
	listScroll int

	// detail is the fully-hydrated selected profile (carries Meetings, which
	// the list endpoint omits). detailID guards against a stale async load
	// landing after the selection moved on.
	detail     notoapi.SpeakerProfile
	detailID   string
	detailLoad bool

	// focusMeetings: the detail's meetings list has focus (Tab toggles); when
	// true, Up/Down move meetingCur and Enter jumps to that meeting.
	focusMeetings bool
	meetingCur    int

	// edit mode form
	editing  bool
	form     []formField
	formVals []string
	fieldIdx int
	input    textinput.Model

	// merge mode: pick the survivor to fold the selected person into.
	merging  bool
	mergeIdx int

	// confirm overlay for destructive actions.
	confirm confirmKind

	// pendingSelect: a profile to preselect once the list loads, set when the
	// screen is entered via a "person:<id>" / "person-edit:<id>" jump from a
	// meeting's Speakers tab. pendingEdit also opens that person's edit form
	// (the just-created, needs-a-name case).
	pendingSelect string
	pendingEdit   bool
}

// confirmKind is the destructive action a confirm overlay is gating, or none.
type confirmKind int

const (
	confirmNone confirmKind = iota
	confirmDelete
	confirmMerge
)

// formFieldKind tags a row in the edit form so the handler knows whether the
// row edits a scalar profile field, one cell of an affiliation, or triggers an
// action (add a blank affiliation).
type formFieldKind int

const (
	ffName formFieldKind = iota
	ffPronouns
	ffNotes
	ffAffContext
	ffAffOrg
	ffAffEmail
	ffAddAff
)

type formField struct {
	kind   formFieldKind
	label  string
	affIdx int // affiliation index for ffAff* kinds
}

func newPeopleScreen() screen {
	ti := textinput.New()
	ti.CharLimit = 256
	ti.Prompt = "" // rendered inline under a field label, so no "> " prompt
	return &peopleScreen{
		loading: true,
		input:   ti,
	}
}

func (p *peopleScreen) id() screenID  { return sPeople }
func (p *peopleScreen) title() string { return "people" }

// inputActive is true while the edit form or a confirm overlay is up, so the
// root router stops firing global letter keys (q, ?, screen aliases) and they
// reach this screen instead — the overlay can't be lost to a stray keypress.
func (p *peopleScreen) inputActive() bool { return p.editing || p.confirm != confirmNone }

func (p *peopleScreen) enter(ctx screenCtx, param string) tea.Cmd {
	// param convention:
	//   ""                   — no preselect
	//   "person:<id>"        — preselect that person (view mode)
	//   "person-edit:<id>"   — preselect + open their edit form (name it now)
	p.loading = true
	p.pendingSelect = ""
	p.pendingEdit = false
	switch {
	case strings.HasPrefix(param, "person-edit:"):
		p.pendingSelect = strings.TrimPrefix(param, "person-edit:")
		p.pendingEdit = true
	case strings.HasPrefix(param, "person:"):
		p.pendingSelect = strings.TrimPrefix(param, "person:")
	}
	return fetchProfiles(ctx)
}

func (p *peopleScreen) leave(_ screenCtx) tea.Cmd { return nil }

func (p *peopleScreen) update(ctx screenCtx, msg tea.Msg) (screen, tea.Cmd) {
	switch v := msg.(type) {
	case profilesLoadedMsg:
		p.loading = false
		p.err = v.Err
		p.profiles = v.Profiles
		if p.pendingSelect != "" {
			if i := p.indexOfProfile(p.pendingSelect); i >= 0 {
				p.cursor = i
			}
			wantEdit := p.pendingEdit
			p.pendingSelect = ""
			p.pendingEdit = false
			p.followCursor(ctx)
			cmd := p.syncDetail(ctx)
			if wantEdit && p.cursor < len(p.profiles) {
				p.startEdit() // land in the edit form so the name is ready to type
			}
			return p, cmd
		}
		if p.cursor >= len(p.profiles) {
			p.cursor = max(0, len(p.profiles)-1)
		}
		p.followCursor(ctx)
		return p, p.syncDetail(ctx)
	case profileLoadedMsg:
		if v.Err != nil {
			p.err = v.Err
			return p, nil
		}
		// Ignore a load that arrived after the selection moved.
		if v.Profile.ID == p.detailID {
			p.detail = v.Profile
			p.detailLoad = false
		}
		return p, nil
	case profileSavedMsg:
		if v.Err != nil {
			return p, func() tea.Msg { return bannerMsg{Kind: "error", Text: v.Err.Error()} }
		}
		verb := map[string]string{"saved": "saved", "deleted": "deleted", "merged": "merged"}[v.Action]
		return p, tea.Batch(p.enter(ctx, ""), func() tea.Msg {
			return bannerMsg{Kind: "info", Text: v.Name + " " + verb}
		})
	case tea.KeyPressMsg:
		return p.handleKey(ctx, v)
	case mouseWheelMsg:
		return p.handleWheel(ctx, v)
	}
	return p, nil
}

// handleWheel pans the directory list when the pointer is over it — moving
// listScroll only, never the selection (a click is what selects), exactly like
// the meeting list. Over anything else the wheel is ignored.
func (p *peopleScreen) handleWheel(ctx screenCtx, w mouseWheelMsg) (screen, tea.Cmd) {
	if !strings.HasPrefix(w.over, "people:row:") {
		return p, nil
	}
	const wheelStep = 3
	if w.up {
		p.listScroll -= wheelStep
	} else {
		p.listScroll += wheelStep
	}
	p.listScroll = scroll.Clamp(p.listScroll, len(p.profiles), p.listRowBudget(ctx))
	return p, nil
}

// followCursor nudges listScroll the minimum needed to keep the active row
// (the merge survivor while merging, else the selected person) visible — called
// after the ARROW keys / a click move it, never on a wheel pan.
func (p *peopleScreen) followCursor(ctx screenCtx) {
	navRow := p.cursor
	if p.merging {
		navRow = p.mergeIdx
	}
	p.listScroll = scroll.Follow(len(p.profiles), p.listRowBudget(ctx), navRow, 1, p.listScroll)
}

// syncDetail (re)points the detail pane at the selected profile and, when that
// profile changed, kicks off a full fetch to pull its meetings.
func (p *peopleScreen) syncDetail(ctx screenCtx) tea.Cmd {
	if p.cursor < 0 || p.cursor >= len(p.profiles) {
		p.detailID = ""
		p.detail = notoapi.SpeakerProfile{}
		return nil
	}
	sel := p.profiles[p.cursor]
	if sel.ID == p.detailID {
		return nil
	}
	p.detailID = sel.ID
	p.detail = sel // show the list-level data immediately; meetings fill in async
	p.detailLoad = true
	p.focusMeetings = false // a different person's meetings; drop stale focus
	p.meetingCur = 0
	return fetchProfile(ctx, sel.ID)
}

func (p *peopleScreen) handleKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	switch {
	case p.editing:
		return p.handleEditKey(ctx, v)
	case p.confirm != confirmNone:
		// The confirm overlay sits on top of whatever mode opened it (list
		// for delete, merge for merge), so it wins the dispatch.
		return p.handleConfirmKey(ctx, v)
	case p.merging:
		return p.handleMergeKey(ctx, v)
	case p.focusMeetings:
		return p.handleMeetingsKey(ctx, v)
	default:
		return p.handleListKey(ctx, v)
	}
}

func (p *peopleScreen) handleListKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	k := ctx.keys
	switch {
	case key.Matches(v, k.Up):
		if p.cursor > 0 {
			p.cursor--
		}
		p.followCursor(ctx)
		return p, p.syncDetail(ctx)
	case key.Matches(v, k.Down):
		if p.cursor < len(p.profiles)-1 {
			p.cursor++
		}
		p.followCursor(ctx)
		return p, p.syncDetail(ctx)
	}
	if len(p.profiles) == 0 {
		return p, nil
	}
	switch {
	case key.Matches(v, k.Tab, k.ShiftTab):
		// Hop focus into the detail's meetings list for quick jump-to.
		if len(p.detail.Meetings) > 0 {
			p.focusMeetings = true
			p.meetingCur = 0
		}
	case key.Matches(v, k.Edit, k.Enter):
		p.startEdit()
	case key.Matches(v, k.Delete):
		p.confirm = confirmDelete
	case key.Matches(v, k.Merge):
		if len(p.profiles) < 2 {
			return p, func() tea.Msg { return bannerMsg{Kind: "warn", Text: "need two people to merge"} }
		}
		p.merging = true
		p.mergeIdx = p.firstOtherIndex()
	}
	return p, nil
}

// --- mouse helpers (the click twins of the keyboard list nav) ---

// selectAt selects directory row i, like Up/Down landing on it. While merging,
// a click instead picks the survivor (mergeIdx) — that's what the list is
// asking for in that mode. Returns any detail-load command.
func (p *peopleScreen) selectAt(ctx screenCtx, i int) tea.Cmd {
	if i < 0 || i >= len(p.profiles) {
		return nil
	}
	if p.merging {
		if i != p.cursor {
			p.mergeIdx = i
		}
		p.followCursor(ctx)
		return nil
	}
	p.cursor = i
	p.focusMeetings = false
	p.followCursor(ctx)
	return p.syncDetail(ctx)
}

// openMeetingAt focuses the detail's meetings list on row i and jumps to that
// meeting on the dashboard — the click twin of Tab-into-meetings then Enter.
func (p *peopleScreen) openMeetingAt(i int) tea.Cmd {
	mtgs := p.detail.Meetings
	if i < 0 || i >= len(mtgs) {
		return nil
	}
	p.focusMeetings = true
	p.meetingCur = i
	return pushOrReplaceTo(sDashboard, "meeting:"+mtgs[i].MeetingID)
}

// indexOfProfile returns the list index of the profile with id, or -1.
func (p *peopleScreen) indexOfProfile(id string) int {
	for i := range p.profiles {
		if p.profiles[i].ID == id {
			return i
		}
	}
	return -1
}

// firstOtherIndex returns the first list index that isn't the current
// selection — the initial merge target.
func (p *peopleScreen) firstOtherIndex() int {
	if p.cursor == 0 {
		return 1
	}
	return 0
}

func (p *peopleScreen) handleMergeKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	k := ctx.keys
	switch {
	case isEscapeKey(v):
		p.merging = false
	case key.Matches(v, k.Up):
		p.mergeIdx = p.stepMergeIdx(-1)
		p.followCursor(ctx)
	case key.Matches(v, k.Down):
		p.mergeIdx = p.stepMergeIdx(1)
		p.followCursor(ctx)
	case key.Matches(v, k.Enter):
		if p.mergeIdx == p.cursor || p.mergeIdx < 0 || p.mergeIdx >= len(p.profiles) {
			return p, nil
		}
		// Confirm before folding — merge can't be undone. The survivor/source
		// pick stays put so esc on the confirm returns here to re-pick.
		p.confirm = confirmMerge
	}
	return p, nil
}

// stepMergeIdx moves the merge cursor by delta, skipping the source row (you
// can't merge a person into themselves) and clamping to the list.
func (p *peopleScreen) stepMergeIdx(delta int) int {
	n := len(p.profiles)
	i := p.mergeIdx
	for {
		i += delta
		if i < 0 || i >= n {
			return p.mergeIdx // hit an edge; stay put
		}
		if i != p.cursor {
			return i
		}
	}
}

func (p *peopleScreen) handleConfirmKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	switch {
	case key.Matches(v, ctx.keys.Enter):
		kind := p.confirm
		p.confirm = confirmNone
		switch kind {
		case confirmDelete:
			if p.cursor < 0 || p.cursor >= len(p.profiles) {
				return p, nil
			}
			sel := p.profiles[p.cursor]
			return p, deleteProfileCmd(ctx, sel.ID, sel.DisplayName)
		case confirmMerge:
			p.merging = false
			if p.cursor < 0 || p.cursor >= len(p.profiles) ||
				p.mergeIdx < 0 || p.mergeIdx >= len(p.profiles) {
				return p, nil
			}
			source := p.profiles[p.cursor]   // folded in
			target := p.profiles[p.mergeIdx] // survivor
			return p, mergeProfilesCmd(ctx, target.ID, source.ID, target.DisplayName)
		}
	case isEscapeKey(v):
		// Cancel only the confirm: delete drops back to the list, merge falls
		// back to the survivor pick (merging stays true) so it can be redone.
		p.confirm = confirmNone
	}
	return p, nil
}

// handleMeetingsKey drives the detail's meetings list once Tab has focused it:
// arrows move the highlight, Enter jumps to that meeting on the dashboard, and
// Tab/esc hand focus back to the directory list.
func (p *peopleScreen) handleMeetingsKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	k := ctx.keys
	mtgs := p.detail.Meetings
	switch {
	case isEscapeKey(v), key.Matches(v, k.Tab, k.ShiftTab):
		p.focusMeetings = false
	case key.Matches(v, k.Up):
		if p.meetingCur > 0 {
			p.meetingCur--
		}
	case key.Matches(v, k.Down):
		if p.meetingCur < len(mtgs)-1 {
			p.meetingCur++
		}
	case key.Matches(v, k.Enter):
		if p.meetingCur >= 0 && p.meetingCur < len(mtgs) {
			return p, pushOrReplaceTo(sDashboard, "meeting:"+mtgs[p.meetingCur].MeetingID)
		}
	}
	return p, nil
}

// --- edit mode ---

func (p *peopleScreen) startEdit() {
	p.editing = true
	p.fieldIdx = 0
	p.buildForm(p.detail)
	p.focusField()
}

// buildForm derives the editable rows from a profile: the three scalar fields,
// three rows per affiliation, then the "add affiliation" action.
func (p *peopleScreen) buildForm(prof notoapi.SpeakerProfile) {
	p.form = []formField{
		{kind: ffName, label: "Name"},
		{kind: ffPronouns, label: "Pronouns"},
		{kind: ffNotes, label: "Notes"},
	}
	p.formVals = []string{prof.DisplayName, prof.Pronouns, prof.Notes}
	for i, a := range prof.Affiliations {
		p.form = append(p.form,
			formField{kind: ffAffContext, label: fmt.Sprintf("Affil %d · context", i+1), affIdx: i},
			formField{kind: ffAffOrg, label: fmt.Sprintf("Affil %d · org", i+1), affIdx: i},
			formField{kind: ffAffEmail, label: fmt.Sprintf("Affil %d · email", i+1), affIdx: i},
		)
		p.formVals = append(p.formVals, a.Context, a.Organization, a.Email)
	}
	p.form = append(p.form, formField{kind: ffAddAff, label: "+ add affiliation"})
	p.formVals = append(p.formVals, "")
}

// focusField loads the focused row's value into the text input (action rows
// get a blurred, empty input).
func (p *peopleScreen) focusField() {
	if p.form[p.fieldIdx].kind == ffAddAff {
		p.input.SetValue("")
		p.input.Blur()
		return
	}
	p.input.SetValue(p.formVals[p.fieldIdx])
	p.input.Focus()
	p.input.CursorEnd()
}

// commitField writes the in-progress input value back into the form before the
// cursor leaves the row.
func (p *peopleScreen) commitField() {
	if p.form[p.fieldIdx].kind != ffAddAff {
		p.formVals[p.fieldIdx] = p.input.Value()
	}
}

func (p *peopleScreen) handleEditKey(ctx screenCtx, v tea.KeyPressMsg) (screen, tea.Cmd) {
	k := ctx.keys
	switch {
	case isEscapeKey(v):
		p.editing = false
		p.input.Blur()
		return p, nil
	case key.Matches(v, k.Up):
		p.commitField()
		if p.fieldIdx > 0 {
			p.fieldIdx--
		}
		p.focusField()
		return p, nil
	case key.Matches(v, k.Down):
		p.commitField()
		if p.fieldIdx < len(p.form)-1 {
			p.fieldIdx++
		}
		p.focusField()
		return p, nil
	case key.Matches(v, k.Enter):
		if p.form[p.fieldIdx].kind == ffAddAff {
			p.addAffiliationRow()
			return p, nil
		}
		// Enter on any field saves the whole person.
		p.commitField()
		return p, p.save(ctx)
	}
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(v)
	return p, cmd
}

// addAffiliationRow commits the current edits, appends a blank affiliation to
// the working values, rebuilds the form, and jumps to the new context cell.
func (p *peopleScreen) addAffiliationRow() {
	p.commitField()
	prof := p.formToProfile()
	prof.Affiliations = append(prof.Affiliations, notoapi.Affiliation{})
	p.buildForm(prof)
	// Focus the new affiliation's context row (three before the add action).
	p.fieldIdx = len(p.form) - 4
	if p.fieldIdx < 0 {
		p.fieldIdx = 0
	}
	p.focusField()
}

// formToProfile reads the working values back into a profile, dropping
// affiliations whose three cells are all blank (the implicit "remove" gesture).
func (p *peopleScreen) formToProfile() notoapi.SpeakerProfile {
	out := notoapi.SpeakerProfile{ID: p.detail.ID}
	// Group affiliation cells by index.
	affs := map[int]*notoapi.Affiliation{}
	order := []int{}
	for i, f := range p.form {
		val := strings.TrimSpace(p.formVals[i])
		switch f.kind {
		case ffName:
			out.DisplayName = val
		case ffPronouns:
			out.Pronouns = val
		case ffNotes:
			out.Notes = val
		case ffAffContext, ffAffOrg, ffAffEmail:
			a, ok := affs[f.affIdx]
			if !ok {
				a = &notoapi.Affiliation{}
				affs[f.affIdx] = a
				order = append(order, f.affIdx)
			}
			switch f.kind {
			case ffAffContext:
				a.Context = val
			case ffAffOrg:
				a.Organization = val
			case ffAffEmail:
				a.Email = val
			}
		}
	}
	for _, idx := range order {
		a := affs[idx]
		if a.Context == "" && a.Organization == "" && a.Email == "" {
			continue // all-blank row → dropped
		}
		out.Affiliations = append(out.Affiliations, *a)
	}
	return out
}

func (p *peopleScreen) save(ctx screenCtx) tea.Cmd {
	prof := p.formToProfile()
	name := prof.DisplayName
	pronouns := prof.Pronouns
	notes := prof.Notes
	affs := prof.Affiliations
	patch := notoapi.SpeakerProfilePatch{
		DisplayName:  &name,
		Pronouns:     &pronouns,
		Notes:        &notes,
		Affiliations: &affs,
	}
	p.editing = false
	p.input.Blur()
	return patchProfileCmd(ctx, p.detail.ID, patch)
}

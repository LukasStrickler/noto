package tui

import (
	"context"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// detailPane is the unified preview/detail view rendered to the right
// of the meetings list, and full-screen by the thin screen_detail
// wrapper on narrow terminals. It owns its own data (meeting,
// transcript, summary, files) and its own tab + scroll state, so the
// host just hands it width/height and forwards key events when it
// holds focus.
type detailPane struct {
	id_        string
	meeting    notoapi.Meeting
	transcript notoapi.Transcript
	summary    notoapi.Summary
	files      notoapi.MeetingFiles
	speakers   []speakerStat

	tab              detailTab
	speakerCur       int
	transcriptScroll int
	// bodyScroll is the free-scroll line offset for the summary tab (the one
	// scrollable body with no cursor of its own); the item tabs cursor-follow on
	// itemCur and the transcript scrolls by segment, so neither uses it. bodyMax
	// is the largest valid offset, cached from the last render (which knows the
	// wrapped line count) so Up/Down can bound the scroll without re-wrapping.
	bodyScroll int
	bodyMax    int
	// itemCur is the cursor within the active items tab (decisions / actions /
	// risks / questions); Enter jumps from the selected item to its cited
	// transcript segment. focusSeg is that jump target in the transcript (a
	// segment index, -1 = none) so the landed-on line is highlighted.
	itemCur  int
	focusSeg int

	// Search context — set by the host so highlights + n/N stay live
	// as the user types in the dashboard's filter input.
	query       string
	matchedSegs []int
	matchCursor int
	// requestedMatchSegmentID is set by the dashboard search flow before
	// transcript data may be loaded. recomputeMatches activates it once
	// the segment is available.
	requestedMatchSegmentID string

	err               error
	loadingMeeting    bool
	loadingTranscript bool
	loadingSummary    bool
	loadingFiles      bool

	// Cross-meeting speaker identity, keyed by meeting-speaker id: resolved
	// person name + ranked candidates for the unresolved ones. Loaded
	// alongside the transcript.
	mappings        map[string]notoapi.MeetingSpeakerMapping
	loadingMappings bool

	// Speakers-tab rename editor.
	editorOpen    bool
	editorInput   textinput.Model
	editingID     string
	editorBanner  string
	editorPending bool
	// editorProfileID set ⇒ the editor renames that already-linked person;
	// empty ⇒ it creates a new person from the speaker's voiceprint.
	editorProfileID string

	// The "Identify speaker" dialog — a centered overlay (composited by the
	// dashboard) with a contained search over the people directory, the ranked
	// candidates, and an inline create-new. Works on any speaker, so it doubles
	// as the reassign/correct flow. While open, inputActive() is true so the
	// search captures letters and the root/dashboard defer their global keys.
	// directory is the full people list, lazy-(re)loaded each time it opens.
	assignOpen  bool
	assignQuery string
	assignCur   int
	directory   []notoapi.SpeakerProfile
	dirLoading  bool
}

type detailTab int

const (
	tabSummary detailTab = iota
	tabActions
	tabDecisions
	tabRisks
	tabQuestions
	tabTranscript
	tabSpeakers
	tabCount
)

var detailTabLabels = [...]string{
	tabSummary:    "summary",
	tabActions:    "actions",
	tabDecisions:  "decisions",
	tabRisks:      "risks",
	tabQuestions:  "questions",
	tabTranscript: "transcript",
	tabSpeakers:   "speakers",
}

func newDetailPane() *detailPane {
	ti := textinput.New()
	ti.Placeholder = "name…"
	ti.CharLimit = 80
	return &detailPane{editorInput: ti, focusSeg: -1}
}

// inputActive reports whether the pane is capturing keys that the root router
// would otherwise treat as globals — the rename editor (letters) or the assign
// picker (digits). Bubbles up to the host so they reach the pane.
func (d *detailPane) inputActive() bool { return d.editorOpen || d.assignOpen }

// hasID reports whether a meeting is currently bound.
func (d *detailPane) hasID() bool { return d.id_ != "" }

// meetingID exposes the bound id so the host (e.g. `k` for speakers) can
// route global key actions to it.
func (d *detailPane) meetingID() string { return d.id_ }

// setQuery refreshes the highlight + match-jump context. Called by the
// host every time the search query changes.
func (d *detailPane) setQuery(q string) {
	d.query = q
	d.recomputeMatches()
}

// load fires fetches for the given meeting. Same-id calls no-op so the
// pane survives idle re-renders without thrashing the API.
func (d *detailPane) load(ctx screenCtx, id string) tea.Cmd {
	if id == "" {
		*d = detailPane{tab: d.tab, focusSeg: -1}
		return nil
	}
	if id == d.id_ {
		return nil
	}
	d.id_ = id
	d.meeting = notoapi.Meeting{}
	d.transcript = notoapi.Transcript{}
	d.summary = notoapi.Summary{}
	d.files = notoapi.MeetingFiles{}
	d.speakers = nil
	d.mappings = nil
	d.matchedSegs = nil
	d.matchCursor = 0
	d.requestedMatchSegmentID = ""
	d.speakerCur = 0
	d.transcriptScroll = 0
	d.bodyScroll = 0
	d.itemCur = 0
	d.focusSeg = -1
	d.assignOpen = false
	d.assignQuery = ""
	d.assignCur = 0
	d.err = nil
	d.loadingMeeting = true
	d.loadingTranscript = true
	d.loadingSummary = true
	d.loadingFiles = true
	d.loadingMappings = true
	return tea.Batch(
		fetchMeeting(ctx, id),
		fetchTranscript(ctx, id),
		fetchSummary(ctx, id),
		fetchFiles(ctx, id),
		fetchSpeakerMappings(ctx, id),
	)
}

// speakerRenamedMsg carries the result of a rename API call.
type speakerRenamedMsg struct {
	MeetingID   string
	SpeakerID   string
	DisplayName string
	Err         error
}

// saveSpeakerNameCmd fires the rename API call on a goroutine. Returns
// speakerRenamedMsg on completion (success or failure).
func saveSpeakerNameCmd(ctx screenCtx, meetingID, speakerID, displayName string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		err := ctx.client.UpdateSpeakerName(c, meetingID, speakerID, displayName)
		return speakerRenamedMsg{
			MeetingID:   meetingID,
			SpeakerID:   speakerID,
			DisplayName: displayName,
			Err:         err,
		}
	}
}

// fetchSpeakerMappings pulls the cross-meeting identity layer for a meeting:
// resolved person names + ranked candidates for the unresolved speakers.
func fetchSpeakerMappings(ctx screenCtx, meetingID string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		res, err := ctx.client.GetMeetingSpeakerMappings(c, meetingID)
		return speakerMappingsLoadedMsg{MeetingID: meetingID, Mappings: res.Mappings, Err: err}
	}
}

// assignSpeakerCmd links a meeting speaker to an existing person (status
// "manual"), then writes that person's name onto the transcript label so this
// meeting reads correctly too — "name once → everywhere".
func assignSpeakerCmd(ctx screenCtx, meetingID, speakerID, profileID, name string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		status := "manual"
		_, err := ctx.client.PatchMeetingSpeakerMappings(c, meetingID, notoapi.MeetingSpeakerMappingsPatch{
			Mappings: []notoapi.MeetingSpeakerMappingPatchEntry{{
				MeetingSpeakerID: speakerID, ProfileID: &profileID, MatchStatus: &status,
			}},
		})
		if err == nil && name != "" {
			err = ctx.client.UpdateSpeakerName(c, meetingID, speakerID, name)
		}
		return speakerIdentityMsg{MeetingID: meetingID, Note: "linked to " + name, Err: err}
	}
}

// createPersonForSpeakerCmd creates a new named person seeded from this
// speaker's voiceprint and links the mapping in one step, then labels the
// transcript.
func createPersonForSpeakerCmd(ctx screenCtx, meetingID, speakerID, name string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		_, err := ctx.client.CreateSpeakerProfile(c, notoapi.CreateSpeakerProfileRequest{
			DisplayName: name, FromMeetingID: meetingID, FromSpeakerID: speakerID,
		})
		if err == nil {
			err = ctx.client.UpdateSpeakerName(c, meetingID, speakerID, name)
		}
		return speakerIdentityMsg{MeetingID: meetingID, Note: "created " + name, Err: err}
	}
}

// createPersonForSpeakerOpenCmd creates a person seeded from this speaker's
// voiceprint (a placeholder name, since the backend requires one) and links the
// mapping, then asks the host to open the People screen on it in edit mode so
// the user can name the just-created person. Used by the "open person" jump on
// an unresolved speaker.
func createPersonForSpeakerOpenCmd(ctx screenCtx, meetingID, speakerID, placeholder string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		prof, err := ctx.client.CreateSpeakerProfile(c, notoapi.CreateSpeakerProfileRequest{
			DisplayName: placeholder, FromMeetingID: meetingID, FromSpeakerID: speakerID,
		})
		return speakerPersonOpenMsg{MeetingID: meetingID, ProfileID: prof.ID, Edit: true, Err: err}
	}
}

// renameLinkedProfileCmd renames an already-linked person — which propagates to
// every meeting that person appears in — and updates this meeting's transcript
// label.
func renameLinkedProfileCmd(ctx screenCtx, meetingID, speakerID, profileID, name string) tea.Cmd {
	return func() tea.Msg {
		c, cancel := context.WithTimeout(ctx.ctx, 5*time.Second)
		defer cancel()
		nm := name
		_, err := ctx.client.PatchSpeakerProfile(c, profileID, notoapi.SpeakerProfilePatch{DisplayName: &nm})
		if err == nil {
			err = ctx.client.UpdateSpeakerName(c, meetingID, speakerID, name)
		}
		return speakerIdentityMsg{MeetingID: meetingID, Note: "renamed to " + name, Err: err}
	}
}

// update consumes async fetch messages addressed to the bound id. Key
// events are NOT handled here — the host calls handleKey explicitly so
// it can decide focus.
func (d *detailPane) update(ctx screenCtx, msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
	case speakerIdentityMsg:
		if v.MeetingID != d.id_ {
			return nil
		}
		d.editorPending = false
		if v.Err != nil {
			d.editorBanner = "save failed: " + v.Err.Error()
			return nil
		}
		d.editorBanner = ""
		// Re-pull the identity layer + transcript so the new name shows
		// everywhere it's rendered from.
		return tea.Batch(
			fetchSpeakerMappings(ctx, d.id_),
			fetchTranscript(ctx, d.id_),
			func() tea.Msg { return bannerMsg{Kind: "info", Text: v.Note} },
		)
	case speakerPersonOpenMsg:
		if v.MeetingID != d.id_ {
			return nil
		}
		d.editorPending = false
		if v.Err != nil {
			d.editorBanner = ""
			return func() tea.Msg { return bannerMsg{Kind: "error", Text: v.Err.Error()} }
		}
		d.editorBanner = ""
		param := "person:" + v.ProfileID
		if v.Edit {
			param = "person-edit:" + v.ProfileID
		}
		return pushOrReplaceTo(sPeople, param)
	case speakerRenamedMsg:
		if v.MeetingID != d.id_ {
			return nil
		}
		d.editorPending = false
		if v.Err != nil {
			d.editorBanner = "save failed: " + v.Err.Error()
			return nil
		}
		d.editorBanner = ""
		// Apply locally so the UI reflects the change before the
		// refetch lands, then re-pull the transcript for canonical state.
		for i := range d.transcript.Speakers {
			if d.transcript.Speakers[i].ID == v.SpeakerID {
				d.transcript.Speakers[i].DisplayName = v.DisplayName
			}
		}
		for i := range d.transcript.Segments {
			if d.transcript.Segments[i].SpeakerID == v.SpeakerID {
				d.transcript.Segments[i].Speaker = v.DisplayName
			}
		}
		for i := range d.speakers {
			if d.speakers[i].ID == v.SpeakerID && v.DisplayName != "" {
				d.speakers[i].Name = v.DisplayName
			}
		}
		return fetchTranscript(ctx, d.id_)
	}
	switch v := msg.(type) {
	case meetingLoadedMsg:
		if v.Meeting.ID == d.id_ {
			d.loadingMeeting = false
			if v.Err != nil {
				d.err = v.Err
				return nil
			}
			d.meeting = v.Meeting
		}
	case transcriptLoadedMsg:
		if v.Transcript.MeetingID == d.id_ || (d.id_ != "" && v.Err == nil && v.Transcript.MeetingID == "") {
			d.loadingTranscript = false
			if v.Err == nil {
				d.transcript = v.Transcript
				d.speakers = computeSpeakerStats(d.transcript)
				d.recomputeMatches()
			}
		}
	case summaryLoadedMsg:
		if v.Summary.MeetingID == d.id_ {
			d.loadingSummary = false
			if v.Err == nil {
				d.summary = v.Summary
			}
		}
	case filesLoadedMsg:
		if v.Files.MeetingID == d.id_ {
			d.loadingFiles = false
			if v.Err == nil {
				d.files = v.Files
			}
		}
	case profilesLoadedMsg:
		// The people directory for the assign dialog (lazy-loaded on open).
		d.dirLoading = false
		if v.Err == nil {
			d.directory = v.Profiles
		}
	case speakerMappingsLoadedMsg:
		if v.MeetingID == d.id_ {
			d.loadingMappings = false
			if v.Err == nil {
				d.mappings = make(map[string]notoapi.MeetingSpeakerMapping, len(v.Mappings))
				for _, mp := range v.Mappings {
					d.mappings[mp.MeetingSpeakerID] = mp
				}
			}
		}
	}
	return nil
}

// speakerDisplayName prefers the resolved cross-meeting person name over the
// transcript label, so a name set once shows on every speaker row.
func (d *detailPane) speakerDisplayName(sp speakerStat) string {
	if mp, ok := d.mappings[sp.ID]; ok && mp.ProfileName != "" {
		return mp.ProfileName
	}
	return sp.Name
}

// currentCandidates returns the ranked suggestions for the selected speaker
// (empty unless it's unresolved with at least one candidate).
func (d *detailPane) currentCandidates() []notoapi.SpeakerCandidate {
	if d.speakerCur >= len(d.speakers) {
		return nil
	}
	mp, ok := d.mappings[d.speakers[d.speakerCur].ID]
	if !ok {
		return nil
	}
	return mp.Candidates
}

// isItemTab reports whether the active tab is one of the cited-item lists
// (decisions / actions / risks / questions) that share renderItemsList and the
// item-cursor + jump-to-transcript navigation.
func (d *detailPane) isItemTab() bool {
	switch d.tab {
	case tabActions, tabDecisions, tabRisks, tabQuestions:
		return true
	}
	return false
}

// currentItems returns the active items tab's list as SummaryItems (actions are
// flattened to carry their owner inline), so navigation and rendering share one
// view of "the items shown right now".
func (d *detailPane) currentItems() []notoapi.SummaryItem {
	switch d.tab {
	case tabActions:
		return actionItemsToSummary(d.summary.ActionItems)
	case tabDecisions:
		return d.summary.Decisions
	case tabRisks:
		return d.summary.Risks
	case tabQuestions:
		return d.summary.OpenQuestions
	}
	return nil
}

// jumpToSegmentID switches to the transcript scrolled to (and highlighting) the
// given segment, so Enter on a cited item lands the user on the exact moment it
// references. Returns false when the segment isn't in this transcript.
func (d *detailPane) jumpToSegmentID(id string) bool {
	for i, seg := range d.transcript.Segments {
		if seg.ID == id {
			d.tab = tabTranscript
			d.focusSeg = i
			d.transcriptScroll = i
			return true
		}
	}
	return false
}

// handleKey processes a key when the pane has focus. Returns (handled,
// cmd) so the host can fall through to its own bindings when false.
func (d *detailPane) handleKey(ctx screenCtx, k tea.KeyPressMsg) (bool, tea.Cmd) {
	// Rename editor takes priority over every other key on the Speakers tab.
	if d.editorOpen {
		switch k.String() {
		case "esc":
			d.closeEditor()
			return true, nil
		case "enter":
			return true, d.commitEditor(ctx)
		}
		var cmd tea.Cmd
		d.editorInput, cmd = d.editorInput.Update(k)
		return true, cmd
	}

	// The assign picker captures keys (incl. digits) while open.
	if d.assignOpen {
		return d.handleAssignKey(ctx, k)
	}

	// Speakers-tab actions: open the assign picker (g) or the name editor
	// (e / enter). Editing a resolved speaker renames the person; editing an
	// unresolved one creates a new person from their voiceprint.
	if d.tab == tabSpeakers && d.speakerCur < len(d.speakers) {
		switch {
		case key.Matches(k, ctx.keys.Assign):
			return true, d.openAssignDialog(ctx)
		case key.Matches(k, ctx.keys.Enter):
			// Enter jumps to the speaker's person page (mirrors Enter-to-jump
			// elsewhere); e keeps the fast inline rename in place.
			return true, d.openPerson(ctx)
		case key.Matches(k, ctx.keys.Edit):
			d.openEditor()
			return true, nil
		}
	}

	// Items tabs (decisions / actions / risks / questions): Enter jumps from the
	// selected item to its first cited segment in the transcript.
	if d.isItemTab() && key.Matches(k, ctx.keys.Enter) {
		items := d.currentItems()
		if d.itemCur < len(items) {
			for _, ref := range items[d.itemCur].SegmentRefs {
				if d.jumpToSegmentID(ref) {
					return true, nil
				}
			}
		}
		return true, nil
	}

	switch {
	case key.Matches(k, ctx.keys.TabNext):
		d.tab = (d.tab + 1) % tabCount
		d.itemCur = 0
		d.bodyScroll = 0
		return true, nil
	case key.Matches(k, ctx.keys.TabPrev):
		d.tab = (d.tab + tabCount - 1) % tabCount
		d.itemCur = 0
		d.bodyScroll = 0
		return true, nil
	case key.Matches(k, ctx.keys.Transcript):
		// Jump straight to the transcript / speakers tabs. These work
		// whether the list or the pane holds focus (the host forwards
		// them); number-key jumps were dropped because the digits collide
		// with the global screen-switch bindings.
		d.tab = tabTranscript
		return true, nil
	case key.Matches(k, ctx.keys.Speakers):
		d.tab = tabSpeakers
		return true, nil
	case key.Matches(k, ctx.keys.NextMatch):
		if d.tab == tabTranscript {
			d.jumpMatch(+1)
			return true, nil
		}
	case key.Matches(k, ctx.keys.PrevMatch):
		if d.tab == tabTranscript {
			d.jumpMatch(-1)
			return true, nil
		}
	case key.Matches(k, ctx.keys.Up):
		switch {
		case d.tab == tabSpeakers:
			if d.speakerCur > 0 {
				d.speakerCur--
				return true, nil
			}
		case d.tab == tabTranscript:
			if d.transcriptScroll > 0 {
				d.transcriptScroll--
				return true, nil
			}
		case d.isItemTab():
			if d.itemCur > 0 {
				d.itemCur--
				return true, nil
			}
		case d.tab == tabSummary:
			// The summary is free-scrolling (no cursor): bound to [0, bodyMax],
			// the max offset the last render measured from the wrapped body.
			if d.bodyScroll > 0 {
				d.bodyScroll--
				return true, nil
			}
		}
	case key.Matches(k, ctx.keys.Down):
		switch {
		case d.tab == tabSpeakers:
			if d.speakerCur < len(d.speakers)-1 {
				d.speakerCur++
				return true, nil
			}
		case d.tab == tabTranscript:
			if d.transcriptScroll < len(d.transcript.Segments)-1 {
				d.transcriptScroll++
				return true, nil
			}
		case d.isItemTab():
			if d.itemCur < len(d.currentItems())-1 {
				d.itemCur++
				return true, nil
			}
		case d.tab == tabSummary:
			if d.bodyScroll < d.bodyMax {
				d.bodyScroll++
				return true, nil
			}
		}
	}
	return false, nil
}

func (d *detailPane) recomputeMatches() {
	activeID := d.requestedMatchSegmentID
	if activeID == "" && len(d.matchedSegs) > 0 && d.matchCursor < len(d.matchedSegs) {
		idx := d.matchedSegs[d.matchCursor]
		if idx >= 0 && idx < len(d.transcript.Segments) {
			activeID = d.transcript.Segments[idx].ID
		}
	}
	d.matchedSegs = d.matchedSegs[:0]
	tokens := queryTokens(d.query)
	if len(tokens) == 0 {
		d.matchCursor = 0
		return
	}
	for i, seg := range d.transcript.Segments {
		if textContainsAny(seg.Text, tokens) {
			d.matchedSegs = append(d.matchedSegs, i)
		}
	}
	d.matchCursor = 0
	if activeID != "" {
		d.setActiveMatchSegmentID(activeID)
	}
}

func (d *detailPane) jumpMatch(direction int) {
	d.advanceMatch(direction)
}

func (d *detailPane) advanceMatch(direction int) bool {
	if len(d.matchedSegs) == 0 {
		return false
	}
	prev := d.matchCursor
	d.matchCursor = (d.matchCursor + direction + len(d.matchedSegs)) % len(d.matchedSegs)
	d.transcriptScroll = d.matchedSegs[d.matchCursor]
	if d.transcriptScroll >= 0 && d.transcriptScroll < len(d.transcript.Segments) {
		d.requestedMatchSegmentID = d.transcript.Segments[d.transcriptScroll].ID
	}
	if direction > 0 {
		return d.matchCursor <= prev
	}
	if direction < 0 {
		return d.matchCursor >= prev
	}
	return false
}

func (d *detailPane) setActiveMatchSegmentID(segmentID string) bool {
	d.requestedMatchSegmentID = segmentID
	if segmentID == "" || len(d.matchedSegs) == 0 {
		return false
	}
	for cursor, segIdx := range d.matchedSegs {
		if segIdx < 0 || segIdx >= len(d.transcript.Segments) {
			continue
		}
		if d.transcript.Segments[segIdx].ID == segmentID {
			d.matchCursor = cursor
			d.transcriptScroll = segIdx
			return true
		}
	}
	return false
}

package tui

import (
	"context"
	"strings"
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

	// Speakers-tab rename editor.
	editorOpen    bool
	editorInput   textinput.Model
	editingID     string
	editorBanner  string
	editorPending bool
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
	return &detailPane{editorInput: ti}
}

// inputActive reports whether the rename editor is capturing letter
// keys. Bubbles up to the host so the root router doesn't swallow them.
func (d *detailPane) inputActive() bool { return d.editorOpen }

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
		*d = detailPane{tab: d.tab}
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
	d.matchedSegs = nil
	d.matchCursor = 0
	d.requestedMatchSegmentID = ""
	d.speakerCur = 0
	d.transcriptScroll = 0
	d.err = nil
	d.loadingMeeting = true
	d.loadingTranscript = true
	d.loadingSummary = true
	d.loadingFiles = true
	return tea.Batch(
		fetchMeeting(ctx, id),
		fetchTranscript(ctx, id),
		fetchSummary(ctx, id),
		fetchFiles(ctx, id),
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

// update consumes async fetch messages addressed to the bound id. Key
// events are NOT handled here — the host calls handleKey explicitly so
// it can decide focus.
func (d *detailPane) update(ctx screenCtx, msg tea.Msg) tea.Cmd {
	switch v := msg.(type) {
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
	}
	return nil
}

// handleKey processes a key when the pane has focus. Returns (handled,
// cmd) so the host can fall through to its own bindings when false.
func (d *detailPane) handleKey(ctx screenCtx, k tea.KeyPressMsg) (bool, tea.Cmd) {
	// Rename editor takes priority over every other key on the Speakers tab.
	if d.editorOpen {
		switch k.String() {
		case "esc":
			d.editorOpen = false
			d.editorInput.Blur()
			d.editorInput.SetValue("")
			d.editingID = ""
			return true, nil
		case "enter":
			name := strings.TrimSpace(d.editorInput.Value())
			if d.editingID == "" {
				d.editorOpen = false
				return true, nil
			}
			d.editorPending = true
			d.editorBanner = "saving…"
			cmd := saveSpeakerNameCmd(ctx, d.id_, d.editingID, name)
			d.editorOpen = false
			d.editorInput.Blur()
			d.editorInput.SetValue("")
			return true, cmd
		}
		var cmd tea.Cmd
		d.editorInput, cmd = d.editorInput.Update(k)
		return true, cmd
	}

	// On the Speakers tab, Edit (e) or Enter opens the rename editor.
	if d.tab == tabSpeakers {
		if key.Matches(k, ctx.keys.Edit, ctx.keys.Enter) {
			if d.speakerCur < len(d.speakers) {
				sp := d.speakers[d.speakerCur]
				d.editingID = sp.ID
				d.editorInput.SetValue(sp.Name)
				d.editorInput.Focus()
				d.editorOpen = true
				d.editorBanner = ""
				return true, nil
			}
		}
	}

	switch {
	case key.Matches(k, ctx.keys.TabNext):
		d.tab = (d.tab + 1) % tabCount
		return true, nil
	case key.Matches(k, ctx.keys.TabPrev):
		d.tab = (d.tab + tabCount - 1) % tabCount
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
		switch d.tab {
		case tabSpeakers:
			if d.speakerCur > 0 {
				d.speakerCur--
				return true, nil
			}
		case tabTranscript:
			if d.transcriptScroll > 0 {
				d.transcriptScroll--
				return true, nil
			}
		}
	case key.Matches(k, ctx.keys.Down):
		switch d.tab {
		case tabSpeakers:
			if d.speakerCur < len(d.speakers)-1 {
				d.speakerCur++
				return true, nil
			}
		case tabTranscript:
			if d.transcriptScroll < len(d.transcript.Segments)-1 {
				d.transcriptScroll++
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

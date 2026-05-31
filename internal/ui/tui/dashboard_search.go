package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// runQueryCmd kicks an FTS request (or clears the matched list if the
// query just emptied).
func (m *dashboardScreen) runQueryCmd(ctx screenCtx) tea.Cmd {
	if m.query == "" {
		m.matched = nil
		m.cursor = 0
		m.lastQuery = ""
		m.preserveSearchSelection = false
		return m.loadSelectedCmd(ctx)
	}
	if m.query == m.lastQuery {
		return nil
	}
	m.lastQuery = m.query
	m.cursor = 0
	m.preserveSearchSelection = false
	return runSearch(ctx, m.query)
}

// loadSelectedCmd asks the detail pane to bind to the row currently
// under the cursor. Cheap to call repeatedly: identical IDs no-op.
func (m *dashboardScreen) loadSelectedCmd(ctx screenCtx) tea.Cmd {
	id := m.currentMeetingID()
	m.pane.setQuery(m.query)
	cmd := m.pane.load(ctx, id)
	m.applyCurrentSearchTarget(false)
	return cmd
}

func (m *dashboardScreen) refreshFocusedSearch(ctx screenCtx) tea.Cmd {
	if m.query == "" {
		return nil
	}
	m.preserveSearchSelection = true
	return runSearch(ctx, m.query)
}

func (m *dashboardScreen) advanceSearchHit(ctx screenCtx) tea.Cmd {
	return m.stepSearchHit(ctx, +1)
}

func (m *dashboardScreen) retreatSearchHit(ctx screenCtx) tea.Cmd {
	return m.stepSearchHit(ctx, -1)
}

func (m *dashboardScreen) stepSearchHit(ctx screenCtx, direction int) tea.Cmd {
	if m.query == "" || len(m.matched) == 0 {
		return nil
	}
	if m.pane.meetingID() == m.currentMeetingID() && len(m.pane.matchedSegs) > 0 {
		wrapped := m.pane.advanceMatch(direction)
		m.pane.tab = tabTranscript
		if !wrapped {
			return nil
		}
	} else if m.stepPendingSearchHit(direction) {
		m.pane.tab = tabTranscript
		return nil
	}
	if direction < 0 {
		m.cursor = (m.cursor - 1 + len(m.matched)) % len(m.matched)
	} else {
		m.cursor = (m.cursor + 1) % len(m.matched)
	}
	m.applyCurrentSearchTarget(true)
	return m.loadSelectedCmd(ctx)
}

func (m *dashboardScreen) stepPendingSearchHit(direction int) bool {
	ids := m.currentTranscriptHitSegmentIDs()
	if len(ids) <= 1 {
		return false
	}
	current := m.pane.requestedMatchSegmentID
	if current == "" {
		current = ids[0]
	}
	idx := 0
	for i, id := range ids {
		if id == current {
			idx = i
			break
		}
	}
	next := idx + direction
	if next < 0 || next >= len(ids) {
		return false
	}
	m.pane.setActiveMatchSegmentID(ids[next])
	return true
}

func (m *dashboardScreen) applyCurrentSearchTarget(showTranscript bool) {
	ids := m.currentTranscriptHitSegmentIDs()
	if len(ids) == 0 {
		return
	}
	segmentID := ids[0]
	for _, id := range ids {
		if id == m.pane.requestedMatchSegmentID {
			segmentID = id
			break
		}
	}
	if showTranscript {
		m.pane.tab = tabTranscript
	}
	m.pane.setActiveMatchSegmentID(segmentID)
}

func (m *dashboardScreen) currentTranscriptHitSegmentIDs() []string {
	if m.query == "" || m.cursor < 0 || m.cursor >= len(m.matched) {
		return nil
	}
	out := []string{}
	for _, hit := range m.matched[m.cursor].TopHits {
		if hit.ResultType == "transcript" && hit.SegmentID != "" {
			out = append(out, hit.SegmentID)
		}
	}
	return out
}

func (m *dashboardScreen) selectMeetingID(id string) {
	if id == "" || m.query == "" {
		return
	}
	for i, hit := range m.matched {
		if hit.MeetingID == id {
			m.cursor = i
			return
		}
	}
}

func (m *dashboardScreen) clampCursor() {
	n := m.visibleCount()
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *dashboardScreen) visibleCount() int {
	if m.query != "" {
		return len(m.matched)
	}
	return len(m.all)
}

func (m *dashboardScreen) currentMeetingID() string {
	if m.query != "" {
		if m.cursor >= 0 && m.cursor < len(m.matched) {
			return m.matched[m.cursor].MeetingID
		}
		return ""
	}
	if m.cursor >= 0 && m.cursor < len(m.all) {
		return m.all[m.cursor].ID
	}
	return ""
}

func (m *dashboardScreen) currentMatchedRow() (notoapi.MeetingHits, bool) {
	if m.query == "" {
		return notoapi.MeetingHits{}, false
	}
	if m.cursor < 0 || m.cursor >= len(m.matched) {
		return notoapi.MeetingHits{}, false
	}
	return m.matched[m.cursor], true
}

package tui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// One stream per program; this stays alive across update cycles. The
// recvEventCmd command consumes one event at a time so the model can
// re-issue it.
var (
	streamMu sync.Mutex
	streamCh <-chan notoapi.Event
)

// subscribeEvents opens an event subscription on client and stores the
// channel. The first recvEventCmd will start delivering events.
func subscribeEvents(ctx context.Context, client notoapi.Client) tea.Cmd {
	return func() tea.Msg {
		ch, err := client.StreamEvents(ctx)
		if err != nil {
			return eventStreamMsg{Closed: true}
		}
		streamMu.Lock()
		streamCh = ch
		streamMu.Unlock()
		// First receive synchronously so the caller doesn't have to
		// issue a separate recvEventCmd.
		return waitNextEvent()
	}
}

// recvEventCmd receives the next event (or closure) from the stream.
func recvEventCmd() tea.Cmd {
	return func() tea.Msg {
		return waitNextEvent()
	}
}

func waitNextEvent() tea.Msg {
	streamMu.Lock()
	ch := streamCh
	streamMu.Unlock()
	if ch == nil {
		return eventStreamMsg{Closed: true}
	}
	ev, ok := <-ch
	if !ok {
		return eventStreamMsg{Closed: true}
	}
	return eventStreamMsg{Event: ev}
}

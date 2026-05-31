package service

import (
	"sync"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// eventHub fans out Events to many subscribers. Used by SSE handlers and
// the in-process direct client's StreamEvents. Slow subscribers are
// dropped to keep the hub responsive — events are advisory UI updates,
// not transactional.
type eventHub struct {
	mu     sync.RWMutex
	subs   map[chan notoapi.Event]struct{}
	closed bool
}

func newEventHub() *eventHub {
	return &eventHub{subs: map[chan notoapi.Event]struct{}{}}
}

// subscribe returns a fresh channel that receives every published event.
// Call unsubscribe(ch) when done. Buffered so a momentarily-stuck
// consumer doesn't block publishers.
func (h *eventHub) subscribe() chan notoapi.Event {
	ch := make(chan notoapi.Event, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(ch)
		return ch
	}
	h.subs[ch] = struct{}{}
	return ch
}

func (h *eventHub) unsubscribe(ch chan notoapi.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// publish broadcasts an event to all subscribers. The .At field is set
// here if zero so handlers don't have to remember.
func (h *eventHub) publish(e notoapi.Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
			// drop event for this subscriber to keep the hub flowing
		}
	}
}

// Subscribe returns a fresh channel that receives every published event
// plus a cleanup func the caller MUST invoke when done.
func (s *Service) Subscribe() (<-chan notoapi.Event, func()) {
	ch := s.events.subscribe()
	return ch, func() { s.events.unsubscribe(ch) }
}

func (h *eventHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for ch := range h.subs {
		close(ch)
	}
	h.subs = nil
}

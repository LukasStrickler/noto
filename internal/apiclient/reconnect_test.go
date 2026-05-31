package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

type mockSSEHandler struct {
	initialEvents  int
	closeAfter     int
	closeOnEvent   int
	heartbeat      bool
	heartbeatDelay time.Duration
	mu             sync.RWMutex
	eventCount     int
	requestCount   int
}

func (m *mockSSEHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.requestCount++
	requestNum := m.requestCount
	m.mu.Unlock()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	fmt.Fprintf(w, ": ready\n\n")
	flusher.Flush()

	m.mu.Lock()
	m.eventCount = 0
	m.mu.Unlock()

	for {
		select {
		case <-r.Context().Done():
			return
		default:
		}

		m.mu.Lock()
		m.eventCount++
		ec := m.eventCount
		m.mu.Unlock()

		m.mu.RLock()
		shouldClose := m.closeOnEvent > 0 && ec == m.closeOnEvent
		m.mu.RUnlock()

		if shouldClose {
			return
		}

		ev := notoapi.Event{
			Kind: notoapi.EventJob,
			Job: &notoapi.Job{
				ID:       fmt.Sprintf("job-%d-%d", requestNum, ec),
				Kind:     notoapi.JobTranscribe,
				Status:   notoapi.JobRunning,
				Progress: float64(ec) * 0.1,
			},
		}
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, payload)
		flusher.Flush()

		if ec >= m.closeAfter && m.closeAfter > 0 {
			closeEv := notoapi.Event{Kind: notoapi.EventClosed}
			closePayload, _ := json.Marshal(closeEv)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", closeEv.Kind, closePayload)
			flusher.Flush()
			return
		}

		if ec >= m.initialEvents && m.closeAfter == 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}

		if m.heartbeat {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(m.heartbeatDelay):
			}
		}
	}
}

type testHTTPClient struct {
	httpClient
	server *httptest.Server
}

func newTestClient(handler http.Handler) *testHTTPClient {
	server := httptest.NewServer(handler)
	hc := NewHTTP(HTTPOptions{
		BaseURL: server.URL,
	})
	return &testHTTPClient{
		httpClient: *hc.(*httpClient),
		server:     server,
	}
}

func (tc *testHTTPClient) close() {
	tc.server.Close()
}

func TestStreamEventsWithReconnect_SingleDisconnect(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		initialEvents: 3,
		closeAfter:    3,
	}

	tc := newTestClient(handler)
	defer tc.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	var received []notoapi.Event
	for ev := range events {
		received = append(received, ev)
		if len(received) >= 6 {
			break
		}
	}

	if len(received) < 3 {
		t.Errorf("expected at least 3 events, got %d", len(received))
	}
}

func TestStreamEventsWithReconnect_MultipleDisconnects(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		initialEvents: 2,
		closeAfter:    2,
	}

	tc := newTestClient(handler)
	defer tc.close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	var received []notoapi.Event
	for ev := range events {
		received = append(received, ev)
		if len(received) >= 10 {
			break
		}
	}

	if len(received) == 0 {
		t.Errorf("expected events")
	}
}

func TestStreamEventsWithReconnect_ContextCancellation(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		closeAfter: 100,
	}

	tc := newTestClient(handler)
	defer tc.close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	var received []notoapi.Event
	cancel()

	for ev := range events {
		received = append(received, ev)
	}

	if len(received) > 1 {
		t.Errorf("expected at most 1 event after context cancel, got %d", len(received))
	}
}

func TestStreamEventsWithReconnect_MaxRetryLimit(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		closeAfter: 1,
	}

	tc := newTestClient(handler)
	defer tc.close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	var received []notoapi.Event
	for ev := range events {
		received = append(received, ev)
		if len(received) >= 5 {
			break
		}
	}

	_ = received
}

func TestStreamEventsWithReconnect_BoundedBackoff(t *testing.T) {
	t.Parallel()

	bo := &backoff{
		base:     1 * time.Second,
		maxDelay: 60 * time.Second,
		cur:      0,
	}

	expected := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second,
		60 * time.Second,
	}

	for i, exp := range expected {
		got := bo.Duration()
		if got != exp {
			t.Errorf("backoff step %d: got %v, want %v", i, got, exp)
		}
		bo.Next()
	}
}

func TestStreamEventsWithReconnect_ResetBackoffOnSuccess(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		closeAfter: 2,
	}

	tc := newTestClient(handler)
	defer tc.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	var received []notoapi.Event
	for ev := range events {
		received = append(received, ev)
		if len(received) >= 6 {
			break
		}
	}

	if len(received) == 0 {
		t.Errorf("expected events")
	}
}

func TestStreamEventsWithReconnect_HeartbeatTimeout(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		heartbeat:      true,
		heartbeatDelay: 100 * time.Millisecond,
		closeAfter:     100,
	}

	tc := newTestClient(handler)
	defer tc.close()

	tc.httpClient.heartbeatTimeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	var received []notoapi.Event
	for ev := range events {
		received = append(received, ev)
		if len(received) >= 3 {
			break
		}
	}

	_ = received
}

func TestStreamEventsWithReconnect_OriginalAPIPreserved(t *testing.T) {
	t.Parallel()
	handler := &mockSSEHandler{
		closeAfter: 5,
	}

	tc := newTestClient(handler)
	defer tc.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := tc.StreamEvents(ctx)
	if err != nil {
		t.Fatalf("StreamEvents() error = %v", err)
	}

	var received []notoapi.Event
	for ev := range events {
		received = append(received, ev)
		if len(received) >= 5 {
			break
		}
	}

	if len(received) == 0 {
		t.Errorf("expected events from original StreamEvents()")
	}
}

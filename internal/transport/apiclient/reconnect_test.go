package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
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

// closeCountingTransport wraps each response body in a one-shot close counter so
// a test can assert the reconnect loop releases connections instead of leaking
// them.
type closeCountingTransport struct {
	inner          http.RoundTripper
	mu             sync.Mutex
	opened, closed int
}

func (t *closeCountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	t.mu.Lock()
	t.opened++
	t.mu.Unlock()
	resp.Body = &countingBody{ReadCloser: resp.Body, t: t}
	return resp, err
}

type countingBody struct {
	io.ReadCloser
	once sync.Once
	t    *closeCountingTransport
}

func (b *countingBody) Close() error {
	b.once.Do(func() {
		b.t.mu.Lock()
		b.t.closed++
		b.t.mu.Unlock()
	})
	return b.ReadCloser.Close()
}

// TestStreamEventsWithReconnect_ClosesBodyOnStreamEnd is the regression for the
// connection leak: when the SERVER ends the stream (clean EOF) while ctx is still
// alive, the transport won't auto-clean the request, so the body must be closed
// explicitly. Before the fix only the >=400 and heartbeat paths closed it, so this
// natural stream-end leaked the connection. (ctx-cancel is mitigated by the
// transport, so it can't catch this — a server-driven end is what exposes it.)
func TestStreamEventsWithReconnect_ClosesBodyOnStreamEnd(t *testing.T) {
	handler := &mockSSEHandler{initialEvents: 2, closeAfter: 2} // 2 events then server closes
	tc := newTestClient(handler)
	defer tc.close()
	tc.heartbeatTimeout = 30 * time.Second

	ct := &closeCountingTransport{inner: tc.stream.Transport}
	tc.stream.Transport = ct

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := tc.StreamEventsWithReconnect(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("StreamEventsWithReconnect() error = %v", err)
	}

	// The server ends the stream after 2 events; the client drains then returns
	// (clean end, no parse error, no reconnect). Draining to channel-close means
	// the goroutine has exited via the !ok path that must close the body.
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				goto done
			}
		case <-timeout:
			t.Fatal("stream did not end")
		}
	}
done:
	ct.mu.Lock()
	opened, closed := ct.opened, ct.closed
	ct.mu.Unlock()
	if opened < 1 {
		t.Fatalf("expected at least one stream connection, got %d", opened)
	}
	if closed != opened {
		t.Errorf("leaked response body on stream end: opened=%d closed=%d", opened, closed)
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

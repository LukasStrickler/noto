package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func TestStreamSSE_ReturnsEventsChannel(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		ev := notoapi.Event{
			Kind: notoapi.EventJob,
			Job: &notoapi.Job{
				ID:       "job-1",
				Kind:     notoapi.JobTranscribe,
				Status:   notoapi.JobRunning,
				Progress: 0.5,
			},
		}
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		w.(http.Flusher).Flush()
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	hc := NewHTTP(HTTPOptions{BaseURL: server.URL})
	client := hc.(*httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := client.streamSSE(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("streamSSE returned error: %v", err)
	}
	if events == nil {
		t.Fatal("streamSSE returned nil channel")
	}

	select {
	case ev := <-events:
		if ev.Job == nil || ev.Job.ID != "job-1" {
			t.Errorf("received event = %v; want job-1", ev)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
}

func TestStreamSSE_ClosesChannelOnContextCancel(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		// Send events until the client disconnects.
		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			ev := notoapi.Event{
				Kind: notoapi.EventJob,
				Job:  &notoapi.Job{ID: fmt.Sprintf("job-%d", i)},
			}
			payload, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	hc := NewHTTP(HTTPOptions{BaseURL: server.URL})
	client := hc.(*httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	events, err := client.streamSSE(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("streamSSE returned error: %v", err)
	}

	var received int
	for range events {
		received++
		if received >= 10 {
			break
		}
	}

	if received == 0 {
		t.Error("expected at least some events before context cancellation")
	}
}

func TestStreamSSE_ReturnsErrorOnHTTPError(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	hc := NewHTTP(HTTPOptions{BaseURL: server.URL})
	client := hc.(*httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.streamSSE(ctx, "/v1/jobs/events")
	if err == nil {
		t.Fatal("streamSSE on 500 response: expected error, got nil")
	}
}

func TestStreamSSE_ParsesSSELinesCorrectly(t *testing.T) {
	t.Parallel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()

		// Send multiple events with different formatting.
		fmt.Fprintf(w, ": comment line\n")
		fmt.Fprintf(w, "data: {\"kind\":\"job\"}\n\n")
		w.(http.Flusher).Flush()

		ev := notoapi.Event{Kind: notoapi.EventJob, Job: &notoapi.Job{ID: "j1"}}
		payload, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", payload)
		w.(http.Flusher).Flush()
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	hc := NewHTTP(HTTPOptions{BaseURL: server.URL})
	client := hc.(*httpClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events, err := client.streamSSE(ctx, "/v1/jobs/events")
	if err != nil {
		t.Fatalf("streamSSE returned error: %v", err)
	}

	var count int
	for ev := range events {
		if ev.Kind == notoapi.EventJob && ev.Job != nil {
			count++
		}
	}

	if count != 1 {
		t.Errorf("received %d job events; want 1 (comment line is not a data event)", count)
	}
}

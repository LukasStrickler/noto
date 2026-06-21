package computewire

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestDoWithRetry pins the transient-failure recovery the hosted Modal path relies
// on: a cold-start/autoscale 502/503/504 retries (transcribe/diarize are
// idempotent) instead of failing the job, while a client error or success returns
// at once and the retry budget is bounded.
func TestDoWithRetry(t *testing.T) {
	old := retryBaseDelay
	retryBaseDelay = time.Millisecond // keep the test instant
	t.Cleanup(func() { retryBaseDelay = old })

	newReqFor := func(url string) func() (*http.Request, error) {
		return func() (*http.Request, error) {
			return http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("audio")))
		}
	}

	t.Run("retries transient 503 then succeeds", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&calls, 1) < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		resp, err := DoWithRetry(context.Background(), srv.Client(), newReqFor(srv.URL))
		if err != nil {
			t.Fatalf("DoWithRetry: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d; want 200 after retrying", resp.StatusCode)
		}
		if got := atomic.LoadInt32(&calls); got != 3 {
			t.Errorf("attempts = %d; want 3 (two 503s then a 200)", got)
		}
	})

	t.Run("does not retry a 4xx client error", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer srv.Close()

		resp, err := DoWithRetry(context.Background(), srv.Client(), newReqFor(srv.URL))
		if err != nil {
			t.Fatalf("DoWithRetry: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d; want 400", resp.StatusCode)
		}
		if got := atomic.LoadInt32(&calls); got != 1 {
			t.Errorf("attempts = %d; want 1 (4xx must not be retried)", got)
		}
	})

	t.Run("exhausts retries on persistent 503 and surfaces the last response", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		resp, err := DoWithRetry(context.Background(), srv.Client(), newReqFor(srv.URL))
		if err != nil {
			t.Fatalf("want the last response surfaced, got err: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("status = %d; want 503 after exhausting retries", resp.StatusCode)
		}
		if got := atomic.LoadInt32(&calls); got != maxComputeAttempts {
			t.Errorf("attempts = %d; want %d", got, maxComputeAttempts)
		}
	})

	t.Run("aborts retries when the context is cancelled", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		resp, err := DoWithRetry(ctx, srv.Client(), newReqFor(srv.URL))
		if err == nil {
			resp.Body.Close()
			t.Fatal("want error from a cancelled context, got nil")
		}
		if got := atomic.LoadInt32(&calls); got > 1 {
			t.Errorf("attempts = %d; a cancelled context must not keep retrying", got)
		}
	})
}

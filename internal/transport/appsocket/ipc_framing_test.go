package appsocket

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestReadFramedResponse_SlowHelper proves the regression fix: a response that
// arrives later than the old per-iteration 100ms deadline (which would have
// failed the call and dropped the connection) is now read in full, because the
// deadline is set once up front.
func TestReadFramedResponse_SlowHelper(t *testing.T) {
	client, helper := net.Pipe()
	defer client.Close()

	want := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}` + "\n"
	go func() {
		defer helper.Close()
		// Simulate a helper that takes 250ms to respond — well past the old 100ms.
		time.Sleep(250 * time.Millisecond)
		_, _ = helper.Write([]byte(want))
	}()

	got, err := readFramedResponse(context.Background(), client, 30*time.Second)
	if err != nil {
		t.Fatalf("slow but in-deadline response must succeed: %v", err)
	}
	if string(got) != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestReadFramedResponse_SplitWrites accumulates bytes across multiple reads
// until the newline terminator.
func TestReadFramedResponse_SplitWrites(t *testing.T) {
	client, helper := net.Pipe()
	defer client.Close()

	go func() {
		defer helper.Close()
		_, _ = helper.Write([]byte(`{"jsonrpc":"2.0",`))
		time.Sleep(20 * time.Millisecond)
		_, _ = helper.Write([]byte(`"id":1,"result":1}` + "\n"))
	}()

	got, err := readFramedResponse(context.Background(), client, 5*time.Second)
	if err != nil {
		t.Fatalf("split response must reassemble: %v", err)
	}
	if want := `{"jsonrpc":"2.0","id":1,"result":1}` + "\n"; string(got) != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// TestReadFramedResponse_RespectsCtxDeadline times out via the caller's ctx
// deadline (the earlier of ctx and fallback), not the 30s fallback.
func TestReadFramedResponse_RespectsCtxDeadline(t *testing.T) {
	client, helper := net.Pipe()
	defer client.Close()
	defer helper.Close() // never writes — force a timeout

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := readFramedResponse(ctx, client, 30*time.Second)
	if err == nil {
		t.Fatal("expected a timeout error when the helper never responds")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("should time out near the ctx deadline (~100ms), took %v", elapsed)
	}
}

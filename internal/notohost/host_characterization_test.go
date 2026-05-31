package notohost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

func TestConnect_WithNOTOAPIURLErrorsOnUnreachable(t *testing.T) {
	t.Setenv("NOTO_API_URL", "http://localhost:9999")
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := Connect(ctx, Options{})
	if err == nil {
		t.Fatal("Connect with unreachable NOTO_API_URL: expected error, got nil")
	}
}

func TestConnect_WithNOTOAPIURLAndTokenUsesBoth(t *testing.T) {
	var receivedToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tmp := t.TempDir()
	t.Setenv("NOTO_API_URL", server.URL)
	t.Setenv("NOTO_API_TOKEN", "my-secret-token")
	t.Setenv("NOTO_CONFIG_DIR", tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, _, err := Connect(ctx, Options{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	if receivedToken != "Bearer my-secret-token" {
		t.Errorf("Authorization header = %q; want %q", receivedToken, "Bearer my-secret-token")
	}
}

func TestConnect_SkipsUDSAndInProcessWhenNOTOAPIURLSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	tmp := t.TempDir()
	t.Setenv("NOTO_API_URL", server.URL)
	t.Setenv("NOTO_CONFIG_DIR", tmp)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, closeFn, err := Connect(ctx, Options{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// When NOTO_API_URL is set, closeFn should be nil (no local host to close).
	if closeFn != nil {
		t.Error("closeFn should be nil when connecting to remote via NOTO_API_URL")
		closeFn()
	}

	_ = client.Close()
}

func TestConnect_WithUnreachableUDSAndNoAPIURLSpawnsInProcess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("NOTO_CONFIG_DIR", tmp)
	t.Setenv("NOTO_ARTIFACT_ROOT", tmp)

	// Ensure no UDS exists.
	udsPath := filepath.Join(tmp, "api.sock")
	os.RemoveAll(udsPath)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, closeFn, err := Connect(ctx, Options{})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if closeFn == nil {
		t.Fatal("closeFn should not be nil when in-process host was spawned")
	}
	defer closeFn()

	// Verify the client works.
	h, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("client.Health: %v", err)
	}
	if !h.OK {
		t.Error("health not OK")
	}
}

func TestDefaultSocketPath(t *testing.T) {
	tmp := t.TempDir()
	got := DefaultSocketPath(tmp)
	expected := filepath.Join(tmp, "api.sock")
	if got != expected {
		t.Errorf("DefaultSocketPath(%q) = %q; want %q", tmp, got, expected)
	}
}

func TestNewToken(t *testing.T) {
	token1 := NewToken()
	token2 := NewToken()

	if token1 == "" || len(token1) != 32 {
		t.Errorf("NewToken() = %q (len %d); want 32-char hex string", token1, len(token1))
	}
	if token1 == token2 {
		t.Error("two calls to NewToken() returned the same value")
	}
}

func TestProbeSocket_Nonexistent(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	if probeSocket(context.Background(), sock) {
		t.Error("probeSocket on nonexistent socket: want false, got true")
	}
}

func TestHostAddrAndToken(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "h.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	host, err := Start(ctx, Options{
		Network: "unix",
		Address: sock,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()

	if host.Addr() == "" {
		t.Error("host.Addr() is empty")
	}
	if host.Token() != "" {
		t.Error("host.Token() should be empty for UDS listener")
	}
	if host.SocketPath() == "" {
		t.Error("host.SocketPath() should return the UDS path for unix network")
	}
}

func TestHostCloseIdempotent(t *testing.T) {
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "h2.sock")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	host, err := Start(ctx, Options{
		Network: "unix",
		Address: sock,
		Version: "test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Multiple closes should not panic.
	for i := 0; i < 3; i++ {
		_ = host.Close()
	}
}

var _ = notoapi.Health{}

package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// testManifest builds a manifest served by a local httptest server.
func testManifest(base string, body []byte) Manifest {
	return Manifest{
		SchemaVersion: "test.v1",
		Models: []Model{{
			ID:      "demo",
			Role:    RoleSTT,
			License: "MIT",
			Variants: []Variant{
				{
					Backend: BackendCPU,
					Tier:    TierFast,
					Primary: "model.onnx",
					Assets: []Asset{
						{Filename: "model.onnx", URL: base + "/model.onnx", SHA256: sha256Hex(body), Size: int64(len(body))},
						{Filename: "tokens.txt", URL: base + "/tokens.txt", Size: int64(len(body))},
					},
				},
				{
					Backend: BackendAny,
					Primary: "any.onnx",
					Assets:  []Asset{{Filename: "any.onnx", URL: base + "/model.onnx"}},
				},
			},
		}},
	}
}

func newServer(t *testing.T, body []byte) (*httptest.Server, *int) {
	t.Helper()
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Length", itoa(len(body)))
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestDownloadAndResolve(t *testing.T) {
	body := []byte("the quick brown fox onnx weights")
	srv, hits := newServer(t, body)
	dir := t.TempDir()
	m := NewWithManifest(dir, testManifest(srv.URL, body))

	if m.Available("demo", BackendCPU, TierFast) {
		t.Fatal("should not be available before download")
	}
	if err := m.Download(context.Background(), "demo", BackendCPU, TierFast, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if !m.Available("demo", BackendCPU, TierFast) {
		t.Fatal("should be available after download")
	}
	mp, err := m.ResolvePaths("demo", BackendCPU, TierFast)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if filepath.Base(mp.File) != "model.onnx" {
		t.Errorf("primary File = %s, want model.onnx", mp.File)
	}
	if mp.Dir != filepath.Join(dir, "models", "demo", "cpu-fast") {
		t.Errorf("dir = %s", mp.Dir)
	}
	for _, name := range []string{"model.onnx", "tokens.txt"} {
		if _, err := os.Stat(mp.Files[name]); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	before := *hits
	// Ensure is a no-op once present (idempotent skip; no new HTTP hits).
	if err := m.Ensure(context.Background(), "demo", BackendCPU, TierFast, nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if *hits != before {
		t.Errorf("Ensure re-downloaded: hits %d -> %d", before, *hits)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	body := []byte("good bytes")
	srv, _ := newServer(t, body)
	dir := t.TempDir()
	man := testManifest(srv.URL, body)
	// Corrupt the expected checksum so the download must reject it.
	man.Models[0].Variants[0].Assets[0].SHA256 = sha256Hex([]byte("different"))
	m := NewWithManifest(dir, man)

	err := m.Download(context.Background(), "demo", BackendCPU, TierFast, nil)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	// The corrupt file must not be left on disk.
	mp, _ := m.ResolvePaths("demo", BackendCPU, TierFast)
	if _, statErr := os.Stat(mp.Files["model.onnx"]); statErr == nil {
		t.Error("mismatched file should have been removed")
	}
}

func TestVerifyPasses(t *testing.T) {
	body := []byte("verifiable")
	srv, _ := newServer(t, body)
	dir := t.TempDir()
	m := NewWithManifest(dir, testManifest(srv.URL, body))
	if err := m.Download(context.Background(), "demo", BackendCPU, TierFast, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	mp, _ := m.ResolvePaths("demo", BackendCPU, TierFast)
	if err := m.Verify(mp); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVariantFallback(t *testing.T) {
	body := []byte("x")
	srv, _ := newServer(t, body)
	m := NewWithManifest(t.TempDir(), testManifest(srv.URL, body))
	// No coreml variant declared → falls back to the BackendAny variant.
	mp, err := m.ResolvePaths("demo", BackendCoreML, TierAccurate)
	if err != nil {
		t.Fatalf("resolve coreml: %v", err)
	}
	if filepath.Base(mp.File) != "any.onnx" {
		t.Errorf("expected any-variant fallback, got %s", mp.File)
	}
}

func TestProgressCallback(t *testing.T) {
	body := make([]byte, 3<<20) // 3 MiB so the 1 MiB throttle emits multiple times
	for i := range body {
		body[i] = byte(i)
	}
	srv, _ := newServer(t, body)
	m := NewWithManifest(t.TempDir(), testManifest(srv.URL, body))

	var mu sync.Mutex
	var lastDownloaded, lastTotal int64
	calls := 0
	m.OnProgress(func(asset string, downloaded, total int64) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		lastDownloaded, lastTotal = downloaded, total
	})
	if err := m.Download(context.Background(), "demo", BackendCPU, TierFast, nil); err != nil {
		t.Fatalf("download: %v", err)
	}
	if calls == 0 {
		t.Fatal("progress callback never fired")
	}
	if lastTotal != int64(len(body)) {
		t.Errorf("total = %d, want %d", lastTotal, len(body))
	}
	if lastDownloaded != int64(len(body)) {
		t.Errorf("final downloaded = %d, want %d", lastDownloaded, len(body))
	}
}

func TestUnknownModel(t *testing.T) {
	m := NewWithManifest(t.TempDir(), DefaultManifest())
	if _, err := m.ResolvePaths("nope", BackendCPU, TierFast); err == nil {
		t.Error("expected error for unknown model")
	}
	if m.Available("nope", BackendCPU, TierFast) {
		t.Error("unknown model should not be available")
	}
}

func TestDefaultManifestResolves(t *testing.T) {
	m := NewWithManifest(t.TempDir(), DefaultManifest())
	// ECAPA is backend-agnostic (single BackendAny variant).
	mp, err := m.ResolvePaths("ecapa512", BackendCPU, TierAccurate)
	if err != nil {
		t.Fatalf("resolve ecapa: %v", err)
	}
	if filepath.Base(mp.File) != "ecapa512.onnx" {
		t.Errorf("ecapa primary = %s", mp.File)
	}
	// Parakeet has fast + accurate CPU variants.
	if _, err := m.ResolvePaths("parakeet-tdt-0.6b-v3", BackendCPU, TierFast); err != nil {
		t.Errorf("resolve parakeet fast: %v", err)
	}
}

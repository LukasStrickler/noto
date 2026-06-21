package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// TestRemoteImportUploadsBytes verifies that a remote (TCP) client streams the
// local file's bytes to /v1/imports/audio as octet-stream with the filename +
// title headers, rather than sending only a path the server can't resolve.
func TestRemoteImportUploadsBytes(t *testing.T) {
	want := []byte("uploaded audio payload \x00\xff")
	tmp := filepath.Join(t.TempDir(), "meeting.m4a")
	if err := os.WriteFile(tmp, want, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotBody []byte
	var gotCT, gotFilename, gotTitle, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotFilename = r.Header.Get("X-Noto-Filename")
		gotTitle = r.Header.Get("X-Noto-Title")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(notoapi.ImportAudioResult{
			Meeting: notoapi.Meeting{ID: "m-1", Title: "Remote upload"},
			Job:     notoapi.Job{ID: "j-1"},
		})
	}))
	defer srv.Close()

	// SocketPath empty + a real BaseURL ⇒ remote client.
	c := NewHTTP(HTTPOptions{BaseURL: srv.URL, Token: "secret"})
	res, err := c.ImportAudio(context.Background(), notoapi.ImportAudioOpts{Path: tmp, Title: "Remote upload"})
	if err != nil {
		t.Fatalf("ImportAudio: %v", err)
	}

	if string(gotBody) != string(want) {
		t.Errorf("uploaded body mismatch: got %q want %q", gotBody, want)
	}
	if gotCT != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", gotCT)
	}
	if gotFilename != "meeting.m4a" {
		t.Errorf("X-Noto-Filename = %q, want meeting.m4a", gotFilename)
	}
	if gotTitle != "Remote upload" {
		t.Errorf("X-Noto-Title = %q", gotTitle)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want Bearer secret", gotAuth)
	}
	if res.Meeting.ID != "m-1" || res.Job.ID != "j-1" {
		t.Errorf("unexpected result: %+v", res)
	}
}

// TestRemoteImportEncodesNonASCIIHeaders pins that an international title/filename
// is percent-encoded onto the wire (so a proxy fronting a hosted backend can't strip
// the non-ASCII header), flagged with X-Noto-Text-Encoding, and decodes back exactly.
func TestRemoteImportEncodesNonASCIIHeaders(t *testing.T) {
	title := "Réunion équipe — Q3"
	fname := "Réunion équipe.m4a"
	tmp := filepath.Join(t.TempDir(), fname)
	if err := os.WriteFile(tmp, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotEnc, gotTitle, gotFilename string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnc = r.Header.Get("X-Noto-Text-Encoding")
		gotTitle = r.Header.Get("X-Noto-Title")
		gotFilename = r.Header.Get("X-Noto-Filename")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(notoapi.ImportAudioResult{Meeting: notoapi.Meeting{ID: "m"}})
	}))
	defer srv.Close()

	c := NewHTTP(HTTPOptions{BaseURL: srv.URL})
	if _, err := c.ImportAudio(context.Background(), notoapi.ImportAudioOpts{Path: tmp, Title: title}); err != nil {
		t.Fatalf("ImportAudio: %v", err)
	}

	if gotEnc != "percent" {
		t.Fatalf("X-Noto-Text-Encoding = %q, want percent", gotEnc)
	}
	// The transmitted header bytes must be pure printable ASCII (proxy-safe).
	for _, h := range []string{gotTitle, gotFilename} {
		for i := 0; i < len(h); i++ {
			if h[i] < 0x20 || h[i] > 0x7e {
				t.Fatalf("encoded header still carries a non-ASCII byte: %q", h)
			}
		}
	}
	// ...and must decode back to the exact original (no mojibake, no loss).
	if dec, _ := url.PathUnescape(gotTitle); dec != title {
		t.Errorf("title round-trip: got %q want %q", dec, title)
	}
	if dec, _ := url.PathUnescape(gotFilename); dec != fname {
		t.Errorf("filename round-trip: got %q want %q", dec, fname)
	}
}

// TestUDSImportSendsPath verifies the local/UDS client keeps the zero-copy
// path-based JSON import (no byte upload) — we must not regress the local fast
// path. It serves a real unix-socket HTTP server so the request actually lands.
func TestUDSImportSendsPath(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "api.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close()

	var gotCT string
	var gotBody notoapi.ImportAudioOpts
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(notoapi.ImportAudioResult{Meeting: notoapi.Meeting{ID: "m-2", Title: gotBody.Title}})
	})}
	go srv.Serve(ln)
	defer srv.Close()

	// A SocketPath ⇒ UDS (local) client ⇒ remote=false ⇒ path-based JSON.
	c := NewHTTP(HTTPOptions{SocketPath: sock})
	res, err := c.ImportAudio(context.Background(), notoapi.ImportAudioOpts{Path: "/server/side/file.m4a", Title: "Local"})
	if err != nil {
		t.Fatalf("ImportAudio: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (UDS must send a path, not bytes)", gotCT)
	}
	if gotBody.Path != "/server/side/file.m4a" {
		t.Errorf("path = %q, want /server/side/file.m4a", gotBody.Path)
	}
	if res.Meeting.ID != "m-2" {
		t.Errorf("result meeting = %q", res.Meeting.ID)
	}
}

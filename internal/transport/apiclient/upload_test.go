package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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

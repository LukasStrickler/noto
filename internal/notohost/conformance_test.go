package notohost_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/internal/apiclient"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/notohost"
)

// The Client contract has 47 methods and two hand-written transports
// (direct in-process, HTTP over UDS). This suite runs ONE behavioral
// contract against BOTH so they can never silently diverge — including
// error-code parity, which proves the HTTP envelope round-trips the same
// coded errors the in-process service returns.

// startConfHost spins up a fresh backend on a temp unix socket with
// isolated config + artifact dirs. Cleanup is registered on t.
func startConfHost(t *testing.T) *notohost.Host {
	t.Helper()
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h, err := notohost.Start(ctx, notohost.Options{
		Network: "unix",
		Address: sock,
		Version: "conf-test",
	})
	if err != nil {
		t.Fatalf("start host: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h
}

func TestClientConformance_Direct(t *testing.T) {
	h := startConfHost(t)
	clientConformance(t, h.Client())
}

func TestClientConformance_HTTP(t *testing.T) {
	h := startConfHost(t)
	c := apiclient.NewHTTP(apiclient.HTTPOptions{SocketPath: h.SocketPath()})
	t.Cleanup(func() { _ = c.Close() })
	clientConformance(t, c)
}

// clientConformance is the shared contract. Subtests are written to be
// order-independent (each creates and cleans up its own data).
func clientConformance(t *testing.T, c notoapi.Client) {
	ctx := context.Background()

	t.Run("Health", func(t *testing.T) {
		h, err := c.Health(ctx)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if !h.OK {
			t.Error("Health.OK = false; want true")
		}
		if h.Version != "conf-test" {
			t.Errorf("Health.Version = %q; want conf-test", h.Version)
		}
	})

	t.Run("ListProviders", func(t *testing.T) {
		ps, err := c.ListProviders(ctx)
		if err != nil {
			t.Fatalf("ListProviders: %v", err)
		}
		if len(ps) == 0 {
			t.Fatal("ListProviders returned none; want the built-in registry")
		}
		for i, p := range ps {
			if p.ID == "" {
				t.Errorf("provider %d has empty ID", i)
			}
		}
	})

	t.Run("GetConfig", func(t *testing.T) {
		if _, err := c.GetConfig(ctx); err != nil {
			t.Fatalf("GetConfig: %v", err)
		}
	})

	t.Run("GetPaths", func(t *testing.T) {
		p, err := c.GetPaths(ctx)
		if err != nil {
			t.Fatalf("GetPaths: %v", err)
		}
		if p.ConfigDir == "" || p.ArtifactRoot == "" {
			t.Errorf("GetPaths returned empty dirs: %+v", p)
		}
	})

	t.Run("Search", func(t *testing.T) {
		if _, err := c.Search(ctx, notoapi.SearchOpts{Query: "anything", Limit: 5}); err != nil {
			t.Fatalf("Search: %v", err)
		}
	})

	t.Run("ImportListGetDelete", func(t *testing.T) {
		audio := filepath.Join(t.TempDir(), "clip.m4a")
		if err := os.WriteFile(audio, []byte("fake-audio-bytes"), 0o644); err != nil {
			t.Fatalf("write audio: %v", err)
		}
		imp, err := c.ImportAudio(ctx, notoapi.ImportAudioOpts{Path: audio, Title: "Conformance import"})
		if err != nil {
			t.Fatalf("ImportAudio: %v", err)
		}
		id := imp.Meeting.ID
		if id == "" {
			t.Fatal("ImportAudio returned no meeting id")
		}

		got, err := c.GetMeeting(ctx, id)
		if err != nil {
			t.Fatalf("GetMeeting(%s): %v", id, err)
		}
		if got.ID != id {
			t.Errorf("GetMeeting.ID = %q; want %q", got.ID, id)
		}

		list, err := c.ListMeetings(ctx, notoapi.ListMeetingsOpts{Limit: 50})
		if err != nil {
			t.Fatalf("ListMeetings: %v", err)
		}
		if !containsMeeting(list.Meetings, id) {
			t.Errorf("ListMeetings does not contain imported meeting %q", id)
		}

		if err := c.DeleteMeeting(ctx, id); err != nil {
			t.Fatalf("DeleteMeeting: %v", err)
		}
		if _, err := c.GetMeeting(ctx, id); err == nil {
			t.Error("GetMeeting after delete returned nil error; want not_found")
		}
	})

	// The headline cross-transport guarantee: a missing meeting yields a
	// not_found *coded* error on BOTH transports, recognizable via
	// notoapi.As. If the HTTP envelope dropped the code (or direct leaked a
	// different error type), this fails.
	t.Run("NotFoundErrorParity", func(t *testing.T) {
		_, err := c.GetMeeting(ctx, "00000000-0000-0000-0000-000000000000")
		if err == nil {
			t.Fatal("GetMeeting(missing) returned nil error")
		}
		apiErr, ok := notoapi.As(err)
		if !ok {
			t.Fatalf("error %v (%T) is not a *notoapi.Error — code lost across transport", err, err)
		}
		if apiErr.Code != notoapi.CodeNotFound {
			t.Errorf("error code = %q; want %q", apiErr.Code, notoapi.CodeNotFound)
		}
	})
}

func containsMeeting(ms []notoapi.Meeting, id string) bool {
	for _, m := range ms {
		if m.ID == id {
			return true
		}
	}
	return false
}

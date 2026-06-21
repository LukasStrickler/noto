package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/app/service"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/transport/server"
)

// echoSTT bakes the received options + audio length into the transcript, so a
// round-trip proves the server parsed every X-Noto-* header and streamed the
// body through to the adapter.
type echoSTT struct{}

func (echoSTT) ProviderID() string { return "echo" }
func (echoSTT) FeatureMap() stt.ProviderFeatures {
	return stt.ProviderFeatures{ProviderID: "echo", IsLocal: true}
}
func (echoSTT) Transcribe(_ context.Context, audio []byte, opts stt.TranscribeOptions) (*artifacts.Transcript, error) {
	return &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     opts.MeetingID,
		Provider:      artifacts.TranscriptProvider{ID: "echo", JobID: opts.Language},
		Segments: []artifacts.Segment{{
			ID:   "seg_0",
			Text: fmt.Sprintf("n=%d bias=%d audio=%d", opts.NumSpeakers, len(opts.ContextBias), len(audio)),
		}},
	}, nil
}

type twoTurnDiar struct{}

func (twoTurnDiar) ProviderID() string { return "fake-diar" }
func (twoTurnDiar) Diarize(_ context.Context, _ []byte, _ diarize.DiarizeOptions) ([]diarize.Turn, error) {
	return []diarize.Turn{{Speaker: "0", StartSeconds: 0, EndSeconds: 2}, {Speaker: "1", StartSeconds: 2, EndSeconds: 4}}, nil
}

func startComputeServer(t *testing.T, diar diarize.Diarizer) (baseURL string, hc *http.Client) {
	t.Helper()
	dir := t.TempDir()
	svc := service.New(service.Deps{
		Config:            config.Config{ConfigDir: dir, ArtifactRoot: dir},
		Registry:          providers.DefaultRegistry(),
		STTAdapterFactory: func(string) (stt.STTProvider, error) { return echoSTT{}, nil },
		Diarizer:          diar,
		Version:           "test",
	})
	t.Cleanup(func() { _ = svc.Close() })

	sock := filepath.Join(t.TempDir(), "compute.sock")
	srv, err := server.New(svc, server.Options{Address: sock})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	hc = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	return "http://unix", hc
}

func TestComputeTranscribeEndpointRoundTrip(t *testing.T) {
	baseURL, hc := startComputeServer(t, twoTurnDiar{})

	r := stt.NewRemoteSTT(baseURL, "")
	r.HTTP = hc
	audio := []byte("0123456789") // len 10
	tr, err := r.Transcribe(context.Background(), audio, stt.TranscribeOptions{
		Language:    "fr",
		MeetingID:   "mtg-42",
		NumSpeakers: 4,
		ContextBias: []string{"a", "b", "c"},
	})
	if err != nil {
		t.Fatalf("remote transcribe through server: %v", err)
	}
	if tr.MeetingID != "mtg-42" {
		t.Errorf("meeting id = %q", tr.MeetingID)
	}
	if tr.Provider.JobID != "fr" {
		t.Errorf("language not threaded through: %q", tr.Provider.JobID)
	}
	if len(tr.Segments) != 1 || tr.Segments[0].Text != "n=4 bias=3 audio=10" {
		t.Errorf("options/audio not threaded through: %+v", tr.Segments)
	}
}

func TestComputeDiarizeEndpointRoundTrip(t *testing.T) {
	baseURL, hc := startComputeServer(t, twoTurnDiar{})

	d := diarize.NewRemoteDiarizer(baseURL, "")
	d.HTTP = hc
	turns, err := d.Diarize(context.Background(), []byte("audio"), diarize.DiarizeOptions{MeetingID: "m", NumSpeakers: 2})
	if err != nil {
		t.Fatalf("remote diarize through server: %v", err)
	}
	if len(turns) != 2 || turns[0].Speaker != "0" || turns[1].EndSeconds != 4 {
		t.Errorf("turns = %+v", turns)
	}
}

// failingDiar models a compute backend whose diarizer dies with a bare error
// carrying internals — subprocess stderr, a Python traceback, an env var name —
// exactly the shape the local pyannote/parakeet adapters produce on failure.
type failingDiar struct{ msg string }

func (failingDiar) ProviderID() string { return "failing-diar" }
func (f failingDiar) Diarize(_ context.Context, _ []byte, _ diarize.DiarizeOptions) ([]diarize.Turn, error) {
	return nil, fmt.Errorf("%s", f.msg)
}

// TestComputeEndpointDoesNotLeakInternalError pins the network-plane contract: a
// non-notoapi adapter failure must NOT be copied verbatim into the HTTP error body
// (it would expose filesystem paths, env var names, and subprocess stderr to the
// calling node). The response stays a structured envelope with a generic internal
// code, and the secret detail never crosses the wire.
func TestComputeEndpointDoesNotLeakInternalError(t *testing.T) {
	const secret = "exec /opt/noto/.venv/bin/python: NOTO_PARAKEET_PYTHON missing; Traceback (most recent call last): RuntimeError"
	baseURL, hc := startComputeServer(t, failingDiar{msg: secret})

	req, err := http.NewRequest(http.MethodPost, baseURL+computewire.DiarizePath, bytes.NewReader([]byte("audio")))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500", resp.StatusCode)
	}
	if strings.Contains(string(body), secret) ||
		strings.Contains(string(body), "NOTO_PARAKEET_PYTHON") ||
		strings.Contains(string(body), "Traceback") {
		t.Errorf("compute endpoint leaked internal detail over the plane:\n%s", body)
	}

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v\n%s", err, body)
	}
	if env.Error.Code != "internal" {
		t.Errorf("error code = %q; want internal", env.Error.Code)
	}
}

func TestComputeDiarizeEndpointUnsupportedWhenNoDiarizer(t *testing.T) {
	baseURL, hc := startComputeServer(t, nil) // no diarizer wired on this backend

	d := diarize.NewRemoteDiarizer(baseURL, "")
	d.HTTP = hc
	_, err := d.Diarize(context.Background(), []byte("audio"), diarize.DiarizeOptions{})
	if err == nil {
		t.Fatal("expected unsupported_capability when backend has no diarizer")
	}
}

package server_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/app/service"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/platform/providers"
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

func TestComputeDiarizeEndpointUnsupportedWhenNoDiarizer(t *testing.T) {
	baseURL, hc := startComputeServer(t, nil) // no diarizer wired on this backend

	d := diarize.NewRemoteDiarizer(baseURL, "")
	d.HTTP = hc
	_, err := d.Diarize(context.Background(), []byte("audio"), diarize.DiarizeOptions{})
	if err == nil {
		t.Fatal("expected unsupported_capability when backend has no diarizer")
	}
}

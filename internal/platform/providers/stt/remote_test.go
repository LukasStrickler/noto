package stt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
)

func TestRemoteSTTStreamsAudioAndOptions(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(artifacts.Transcript{
			SchemaVersion: "transcript.v1",
			MeetingID:     r.Header.Get(computewire.HeaderMeetingID),
		})
	}))
	defer srv.Close()

	audio := []byte("RIFF....fake-wav-bytes....")
	r := NewRemoteSTT(srv.URL, "secret-token")
	tr, err := r.Transcribe(context.Background(), audio, TranscribeOptions{
		Language:    "de",
		MeetingID:   "m-1",
		NumSpeakers: 3,
		ContextBias: []string{"Noto", "Lukas"},
	})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotPath != computewire.TranscribePath {
		t.Errorf("path = %q, want %q", gotPath, computewire.TranscribePath)
	}
	if string(gotBody) != string(audio) {
		t.Errorf("audio not streamed verbatim: got %q", gotBody)
	}
	if ct := gotHeaders.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type = %q (audio must stream, not base64-in-JSON)", ct)
	}
	if gotHeaders.Get(computewire.HeaderLanguage) != "de" {
		t.Errorf("language header = %q", gotHeaders.Get(computewire.HeaderLanguage))
	}
	if gotHeaders.Get(computewire.HeaderNumSpeakers) != "3" {
		t.Errorf("num-speakers header = %q", gotHeaders.Get(computewire.HeaderNumSpeakers))
	}
	if gotHeaders.Get("Authorization") != "Bearer secret-token" {
		t.Errorf("authorization header = %q", gotHeaders.Get("Authorization"))
	}
	var bias []string
	_ = json.Unmarshal([]byte(gotHeaders.Get(computewire.HeaderContextBias)), &bias)
	if len(bias) != 2 || bias[0] != "Noto" {
		t.Errorf("context-bias header decoded to %v", bias)
	}
	if tr.MeetingID != "m-1" {
		t.Errorf("transcript meeting id = %q", tr.MeetingID)
	}
}

func TestRemoteSTTSurfacesRemoteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"provider_failed","message":"gpu oom"}}`))
	}))
	defer srv.Close()
	_, err := NewRemoteSTT(srv.URL, "").Transcribe(context.Background(), []byte("x"), TranscribeOptions{})
	if err == nil {
		t.Fatal("expected error from non-2xx response")
	}
	if !strings.Contains(err.Error(), "gpu oom") {
		t.Errorf("error should surface remote message, got %v", err)
	}
}

func TestRemoteSTTUnconfigured(t *testing.T) {
	_, err := NewRemoteSTT("", "").Transcribe(context.Background(), []byte("x"), TranscribeOptions{})
	if err == nil {
		t.Fatal("expected error when endpoint is empty")
	}
}

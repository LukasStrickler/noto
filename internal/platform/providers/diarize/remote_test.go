package diarize

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
)

func TestRemoteDiarizerStreamsAudioAndParsesTurns(t *testing.T) {
	var gotBody []byte
	var gotPath string
	var gotNumSpeakers string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotNumSpeakers = r.Header.Get(computewire.HeaderNumSpeakers)
		gotBody, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(TurnsResponse{Turns: []Turn{
			{Speaker: "0", StartSeconds: 0, EndSeconds: 2},
			{Speaker: "1", StartSeconds: 2, EndSeconds: 4.5},
		}})
	}))
	defer srv.Close()

	audio := []byte("fake-audio")
	turns, err := NewRemoteDiarizer(srv.URL, "").Diarize(context.Background(), audio, DiarizeOptions{
		MeetingID:   "m-1",
		NumSpeakers: 2,
	})
	if err != nil {
		t.Fatalf("Diarize: %v", err)
	}
	if gotPath != computewire.DiarizePath {
		t.Errorf("path = %q, want %q", gotPath, computewire.DiarizePath)
	}
	if string(gotBody) != string(audio) {
		t.Errorf("audio not streamed verbatim")
	}
	if gotNumSpeakers != "2" {
		t.Errorf("num-speakers header = %q", gotNumSpeakers)
	}
	if len(turns) != 2 || turns[1].Speaker != "1" || turns[1].EndSeconds != 4.5 {
		t.Errorf("turns parsed wrong: %+v", turns)
	}
}

func TestRemoteDiarizerSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":{"code":"unsupported_capability","message":"no diarizer"}}`))
	}))
	defer srv.Close()
	_, err := NewRemoteDiarizer(srv.URL, "").Diarize(context.Background(), []byte("x"), DiarizeOptions{})
	if err == nil {
		t.Fatal("expected error")
	}
}

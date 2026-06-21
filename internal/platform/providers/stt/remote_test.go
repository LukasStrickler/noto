package stt

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// TestMarshalBoundedBias bounds the context-bias header so an unbounded glossary
// (all known profiles) can't grow past serverless header limits and get the whole
// request rejected. Small lists pass through whole; oversized lists keep the
// largest leading prefix that fits; the result always parses and stays in-bounds.
func TestMarshalBoundedBias(t *testing.T) {
	// A small list encodes whole.
	small := []string{"Alice", "Bob", "Quarterly planning"}
	got := marshalBoundedBias(small, maxBiasHeaderBytes)
	var round []string
	if err := json.Unmarshal([]byte(got), &round); err != nil {
		t.Fatalf("small bias must be valid JSON: %v", err)
	}
	if len(round) != len(small) {
		t.Errorf("small bias should pass through whole: got %d want %d", len(round), len(small))
	}

	// A huge list is trimmed to fit, stays valid JSON, and keeps the leading
	// (most-relevant-first) terms.
	huge := make([]string, 5000)
	for i := range huge {
		huge[i] = "Person-Name-Number-" + strconv.Itoa(i)
	}
	got = marshalBoundedBias(huge, maxBiasHeaderBytes)
	if got == "" {
		t.Fatal("a huge list must still yield a bounded header, not empty")
	}
	if len(got) > maxBiasHeaderBytes {
		t.Errorf("bounded header is %d bytes; must be <= %d", len(got), maxBiasHeaderBytes)
	}
	var trimmed []string
	if err := json.Unmarshal([]byte(got), &trimmed); err != nil {
		t.Fatalf("trimmed bias must be valid JSON: %v", err)
	}
	if len(trimmed) == 0 || len(trimmed) >= len(huge) {
		t.Errorf("huge list should be trimmed to a proper subset, kept %d of %d", len(trimmed), len(huge))
	}
	if trimmed[0] != huge[0] {
		t.Errorf("trimming must keep the leading terms; got first %q want %q", trimmed[0], huge[0])
	}
}

// TestMarshalBoundedBiasASCIISafe pins that international participant names produce a
// PURE-ASCII header value (so a proxy fronting a hosted compute endpoint can't strip
// the non-ASCII header and lose the bias glossary) while still decoding back to the
// exact original names — \uXXXX is valid JSON, so the remote needs no change.
func TestMarshalBoundedBiasASCIISafe(t *testing.T) {
	terms := []string{"Réunion", "François", "会議", "naïve café", "Bob"}
	got := marshalBoundedBias(terms, maxBiasHeaderBytes)
	if got == "" {
		t.Fatal("expected a bias header value")
	}
	for i := 0; i < len(got); i++ {
		if got[i] >= 0x80 {
			t.Fatalf("bias header carries a non-ASCII byte at %d: %q", i, got)
		}
	}
	var round []string
	if err := json.Unmarshal([]byte(got), &round); err != nil {
		t.Fatalf("escaped bias must be valid JSON: %v", err)
	}
	if len(round) != len(terms) {
		t.Fatalf("round-trip changed the term count: got %v want %v", round, terms)
	}
	for i := range terms {
		if round[i] != terms[i] {
			t.Errorf("round-trip[%d] = %q, want %q", i, round[i], terms[i])
		}
	}
}

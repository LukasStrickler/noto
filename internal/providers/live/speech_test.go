package live

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestElevenLabsClientBuildsSpeechToTextRequest(t *testing.T) {
	audio := tempAudio(t)
	var sawKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/speech-to-text" {
			t.Fatalf("path = %s, want /speech-to-text", r.URL.Path)
		}
		sawKey = r.Header.Get("xi-api-key") == "test-key"
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}
		if got := r.FormValue("model_id"); got != "scribe_v2" {
			t.Fatalf("model_id = %s, want scribe_v2", got)
		}
		_, _ = io.WriteString(w, `{"language_code":"en","text":"hello","words":[]}`)
	}))
	defer server.Close()

	_, err := (ElevenLabsSpeechClient{BaseURL: server.URL}).Transcribe(context.Background(), SpeechRequest{APIKey: "test-key", AudioPath: audio})
	if err != nil {
		t.Fatalf("Transcribe returned error: %v", err)
	}
	if !sawKey {
		t.Fatal("request did not include xi-api-key header")
	}
}

func TestMistralClientBuildsAudioTranscriptionRequest(t *testing.T) {
	audio := tempAudio(t)
	var sawBearer bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Fatalf("path = %s, want /audio/transcriptions", r.URL.Path)
		}
		sawBearer = r.Header.Get("Authorization") == "Bearer test-key"
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm returned error: %v", err)
		}
		if got := r.FormValue("model"); got != "voxtral-mini-latest" {
			t.Fatalf("model = %s, want voxtral-mini-latest", got)
		}
		_, _ = io.WriteString(w, `{"text":"hello"}`)
	}))
	defer server.Close()

	_, err := (MistralSpeechClient{BaseURL: server.URL}).Transcribe(context.Background(), SpeechRequest{APIKey: "test-key", AudioPath: audio})
	if err != nil {
		t.Fatalf("Transcribe returned error: %v", err)
	}
	if !sawBearer {
		t.Fatal("request did not include bearer auth")
	}
}

func TestAssemblyAIClientUploadSubmitAndPoll(t *testing.T) {
	audio := tempAudio(t)
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("authorization") != "test-key" {
			t.Fatalf("missing authorization header")
		}
		switch r.URL.Path {
		case "/v2/upload":
			_ = json.NewEncoder(w).Encode(map[string]string{"upload_url": "https://assembly.example/uploaded"})
		case "/v2/transcript":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "tx_123"})
		case "/v2/transcript/tx_123":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "completed", "text": "hello"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	_, err := (AssemblyAISpeechClient{BaseURL: server.URL, PollInterval: time.Nanosecond, MaxPolls: 1}).Transcribe(context.Background(), SpeechRequest{APIKey: "test-key", AudioPath: audio})
	if err != nil {
		t.Fatalf("Transcribe returned error: %v", err)
	}
	got := strings.Join(calls, ",")
	want := "POST /v2/upload,POST /v2/transcript,GET /v2/transcript/tx_123"
	if got != want {
		t.Fatalf("calls = %s, want %s", got, want)
	}
}

func tempAudio(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "audio-*.wav")
	if err != nil {
		t.Fatalf("CreateTemp returned error: %v", err)
	}
	if _, err := f.WriteString("not real audio"); err != nil {
		t.Fatalf("WriteString returned error: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	return f.Name()
}

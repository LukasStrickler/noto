package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

func TestOpenRouterSummaryCitesTranscriptSegments(t *testing.T) {
	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test",
		Provider:      artifacts.TranscriptProvider{ID: "fake-stt"},
		Speakers:      []artifacts.Speaker{{ID: "spk_0", Label: "Speaker 0"}},
		Segments: []artifacts.Segment{{
			ID:           "seg_000001",
			SpeakerID:    "spk_0",
			StartSeconds: 0,
			EndSeconds:   3,
			Text:         "We decided to use OpenRouter for summaries.",
		}},
	}
	if err := artifacts.ValidateTranscript(transcript); err != nil {
		t.Fatalf("fixture transcript invalid: %v", err)
	}
	summary, err := (OpenRouterSummaryAdapter{ModelID: "anthropic/claude-3.5-sonnet"}).Summarize(context.Background(), transcript)
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if summary.Model.Provider != "openrouter" {
		t.Fatalf("summary provider = %s, want openrouter", summary.Model.Provider)
	}
	if err := artifacts.ValidateSummary(summary, transcript); err != nil {
		t.Fatalf("ValidateSummary returned error: %v", err)
	}
}

func TestOpenRouterClientUsesChatCompletionsEndpoint(t *testing.T) {
	var sawBearer bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		sawBearer = r.Header.Get("Authorization") == "Bearer test-key"
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("Decode returned error: %v", err)
		}
		if payload["model"] != "anthropic/claude-3.5-sonnet" {
			t.Fatalf("model = %v", payload["model"])
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	got, err := (OpenRouterClient{BaseURL: server.URL}).Chat(context.Background(), ChatRequest{
		APIKey:  "test-key",
		ModelID: "anthropic/claude-3.5-sonnet",
		Messages: []ChatMessage{{
			Role:    "user",
			Content: "Summarize this.",
		}},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if got != "ok" {
		t.Fatalf("Chat = %q, want ok", got)
	}
	if !sawBearer {
		t.Fatal("request did not include bearer auth")
	}
}

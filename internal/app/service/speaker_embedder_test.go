package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

func TestHTTPSpeakerEmbedderContract(t *testing.T) {
	var captured speakerEmbeddingRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/v1/speaker-embeddings" {
			t.Fatalf("path = %s, want /v1/speaker-embeddings", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(speakerEmbeddingResponse{
			Model:      "titanet-large",
			Embeddings: map[string][]float64{"A": {0.1, 0.2, 0.3}},
		})
	}))
	defer server.Close()

	embedder := NewHTTPSpeakerEmbedder(server.URL)
	transcript := &artifacts.Transcript{
		MeetingID: "meeting-1",
		Speakers:  []artifacts.Speaker{{ID: "spk_a", ProviderLabel: "A", DisplayName: "Speaker A"}},
		Segments:  []artifacts.Segment{{ID: "seg_1", SpeakerID: "spk_a", StartSeconds: 1.5, EndSeconds: 4.0}},
	}
	embeddings, err := embedder.EmbedSpeakers(context.Background(), []byte("audio bytes"), transcript)
	if err != nil {
		t.Fatalf("EmbedSpeakers returned error: %v", err)
	}
	if got := captured.Audio; got != base64.StdEncoding.EncodeToString([]byte("audio bytes")) {
		t.Fatalf("audio payload = %q, want base64 audio", got)
	}
	if len(captured.Speakers) != 1 || captured.Speakers[0].ProviderLabel != "A" {
		t.Fatalf("captured speakers = %#v", captured.Speakers)
	}
	if len(captured.Segments) != 1 || captured.Segments[0].ProviderLabel != "A" {
		t.Fatalf("captured segments = %#v", captured.Segments)
	}
	if len(embeddings["A"]) != 3 {
		t.Fatalf("embedding for A = %#v", embeddings["A"])
	}
}

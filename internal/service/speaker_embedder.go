package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
)

type SpeakerEmbedder interface {
	EmbedSpeakers(ctx context.Context, audio []byte, transcript *artifacts.Transcript) (map[string][]float64, error)
}

type HTTPSpeakerEmbedder struct {
	Endpoint string
	HTTP     *http.Client
}

func NewHTTPSpeakerEmbedder(endpoint string) *HTTPSpeakerEmbedder {
	return &HTTPSpeakerEmbedder{Endpoint: strings.TrimRight(endpoint, "/")}
}

type speakerEmbeddingRequest struct {
	MeetingID string                    `json:"meeting_id"`
	Audio     string                    `json:"audio_base64"`
	Speakers  []speakerEmbeddingSpeaker `json:"speakers"`
	Segments  []speakerEmbeddingSegment `json:"segments"`
}

type speakerEmbeddingSpeaker struct {
	ID            string `json:"id"`
	ProviderLabel string `json:"provider_label"`
	DisplayName   string `json:"display_name"`
}

type speakerEmbeddingSegment struct {
	ID            string  `json:"id"`
	SpeakerID     string  `json:"speaker_id"`
	StartSeconds  float64 `json:"start_seconds"`
	EndSeconds    float64 `json:"end_seconds"`
	ProviderLabel string  `json:"provider_label"`
}

type speakerEmbeddingResponse struct {
	Model      string               `json:"model"`
	Embeddings map[string][]float64 `json:"embeddings"`
}

func (e *HTTPSpeakerEmbedder) EmbedSpeakers(ctx context.Context, audio []byte, transcript *artifacts.Transcript) (map[string][]float64, error) {
	if e == nil || strings.TrimSpace(e.Endpoint) == "" {
		return nil, nil
	}
	if len(audio) == 0 || transcript == nil || len(transcript.Speakers) == 0 {
		return nil, nil
	}

	labelsByID := make(map[string]string, len(transcript.Speakers))
	reqBody := speakerEmbeddingRequest{
		MeetingID: transcript.MeetingID,
		Audio:     base64.StdEncoding.EncodeToString(audio),
		Speakers:  make([]speakerEmbeddingSpeaker, 0, len(transcript.Speakers)),
		Segments:  make([]speakerEmbeddingSegment, 0, len(transcript.Segments)),
	}
	for _, sp := range transcript.Speakers {
		labelsByID[sp.ID] = sp.ProviderLabel
		reqBody.Speakers = append(reqBody.Speakers, speakerEmbeddingSpeaker{
			ID:            sp.ID,
			ProviderLabel: sp.ProviderLabel,
			DisplayName:   sp.DisplayName,
		})
	}
	for _, seg := range transcript.Segments {
		reqBody.Segments = append(reqBody.Segments, speakerEmbeddingSegment{
			ID:            seg.ID,
			SpeakerID:     seg.SpeakerID,
			StartSeconds:  seg.StartSeconds,
			EndSeconds:    seg.EndSeconds,
			ProviderLabel: labelsByID[seg.SpeakerID],
		})
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, notoerr.Wrap("speaker_embedding_request_invalid", "Could not encode speaker embedding request.", err)
	}
	client := e.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.Endpoint+"/v1/speaker-embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, notoerr.Wrap("speaker_embedding_request_failed", "Could not create speaker embedding request.", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("speaker_embedding_remote_error", "Speaker embedding service request failed.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notoerr.New("speaker_embedding_failed", "Speaker embedding service returned an error.", map[string]any{"status_code": resp.StatusCode})
	}
	var out speakerEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, notoerr.Wrap("speaker_embedding_response_invalid", "Could not decode speaker embedding response.", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, nil
	}
	for label, emb := range out.Embeddings {
		if strings.TrimSpace(label) == "" {
			return nil, notoerr.New("speaker_embedding_response_invalid", "Speaker embedding response contains an empty speaker label.", nil)
		}
		if len(emb) == 0 {
			return nil, notoerr.New("speaker_embedding_response_invalid", fmt.Sprintf("Speaker embedding for %q is empty.", label), nil)
		}
	}
	return out.Embeddings, nil
}

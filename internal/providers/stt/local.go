package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
)

// LocalAdapter sends audio to a user-managed OpenAI-compatible STT
// server. Compatible with: whisper.cpp's HTTP server, faster-whisper-
// server, NVIDIA NIM Parakeet endpoints, openedai-speech, vLLM, etc.
//
// The endpoint is sourced from NOTO_LOCAL_STT_URL or the optional
// CredentialRef "provider:local" (read by the service layer); falls
// back to http://127.0.0.1:8000/v1/audio/transcriptions.
//
// Diarization isn't part of the OpenAI STT contract; if your local
// server doesn't return speakers, AssemblyAI remains the better V1
// choice for diarized meetings.
type LocalAdapter struct {
	BaseURL string
	APIKey  string
	HTTP    HTTPDoer
}

func (a *LocalAdapter) ProviderID() string { return "local" }

func (a *LocalAdapter) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	url := a.BaseURL
	if url == "" {
		url = os.Getenv("NOTO_LOCAL_STT_URL")
	}
	if url == "" {
		url = "http://127.0.0.1:8000/v1/audio/transcriptions"
	}

	body, contentType, err := localMultipartBody(audio, opts)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create local STT request.", err)
	}
	req.Header.Set("Content-Type", contentType)
	if a.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.APIKey)
	}

	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("retryable_remote_error", "Local STT request failed (server reachable?).", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not read local STT response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notoerr.New("provider_failed", "Local STT server returned error.", map[string]any{
			"status_code": resp.StatusCode,
			"body":        string(raw),
		})
	}
	return parseOpenAITranscript(raw, opts.MeetingID)
}

func localMultipartBody(audio []byte, opts TranscribeOptions) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("model", coalesce(opts.Model, "whisper-1")); err != nil {
		return nil, "", err
	}
	if opts.Language != "" {
		_ = w.WriteField("language", opts.Language)
	}
	_ = w.WriteField("response_format", "verbose_json")
	if len(opts.ContextBias) > 0 {
		_ = w.WriteField("prompt", strings.Join(opts.ContextBias, ", "))
	}
	part, err := w.CreateFormFile("file", "noto.wav")
	if err != nil {
		return nil, "", err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// parseOpenAITranscript handles the OpenAI verbose_json shape that
// whisper.cpp and most local servers return. Speakers default to a
// single "speaker_1" since OpenAI's contract has no diarization.
func parseOpenAITranscript(raw []byte, meetingID string) (*artifacts.Transcript, error) {
	var resp struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Segments []struct {
			ID    int     `json:"id"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not parse local STT JSON.", err)
	}
	speakers := []artifacts.Speaker{{
		ID:    "speaker_1",
		Label: "Speaker 1",
	}}
	segments := make([]artifacts.Segment, 0, len(resp.Segments))
	for _, seg := range resp.Segments {
		segments = append(segments, artifacts.Segment{
			ID:           localSegmentID(seg.ID + 1),
			SpeakerID:    "speaker_1",
			StartSeconds: seg.Start,
			EndSeconds:   seg.End,
			Text:         strings.TrimSpace(seg.Text),
		})
	}
	if len(segments) == 0 && strings.TrimSpace(resp.Text) != "" {
		segments = append(segments, artifacts.Segment{
			ID:           "seg_000001",
			SpeakerID:    "speaker_1",
			StartSeconds: 0,
			EndSeconds:   resp.Duration,
			Text:         strings.TrimSpace(resp.Text),
		})
	}
	t := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       meetingID,
		Language:        resp.Language,
		DurationSeconds: resp.Duration,
		Provider: artifacts.TranscriptProvider{
			ID: "local",
		},
		Speakers: speakers,
		Segments: segments,
		Capabilities: artifacts.TranscriptCapabilities{
			SpeakerDiarization: false,
			WordTimestamps:     false,
		},
	}
	if err := t.Validate(); err != nil {
		return nil, notoerr.Wrap("transcript_invalid", "Local STT transcript failed validation.", err)
	}
	return t, nil
}

func localSegmentID(n int) string {
	return "seg_" + leftPad(n, 6)
}

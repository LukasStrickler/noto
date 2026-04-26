package live

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/notoerr"
)

type SpeechRequest struct {
	APIKey      string
	AudioPath   string
	Language    string
	ContextBias []string
	NumSpeakers int
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type MistralSpeechClient struct {
	BaseURL string
	HTTP    HTTPDoer
}

type ElevenLabsSpeechClient struct {
	BaseURL string
	HTTP    HTTPDoer
}

type AssemblyAISpeechClient struct {
	BaseURL      string
	HTTP         HTTPDoer
	PollInterval time.Duration
	MaxPolls     int
}

func (c MistralSpeechClient) Transcribe(ctx context.Context, req SpeechRequest) ([]byte, error) {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://api.mistral.ai/v1"
	}
	body, contentType, err := multipartBody(req.AudioPath, map[string]string{
		"model":                   "voxtral-mini-latest",
		"diarize":                 "true",
		"timestamp_granularities": "word",
		"language":                req.Language,
		"context_bias":            strings.Join(req.ContextBias, ","),
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/audio/transcriptions", body)
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create Mistral transcription request.", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	httpReq.Header.Set("Content-Type", contentType)
	return doJSON(c.HTTP, httpReq, "mistral")
}

func (c ElevenLabsSpeechClient) Transcribe(ctx context.Context, req SpeechRequest) ([]byte, error) {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://api.elevenlabs.io/v1"
	}
	fields := map[string]string{
		"model_id":               "scribe_v2",
		"diarize":                "true",
		"timestamps_granularity": "word",
		"tag_audio_events":       "true",
		"language_code":          req.Language,
	}
	if req.NumSpeakers > 0 {
		fields["num_speakers"] = intString(req.NumSpeakers)
	}
	body, contentType, err := multipartBody(req.AudioPath, fields)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/speech-to-text", body)
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create ElevenLabs transcription request.", err)
	}
	httpReq.Header.Set("xi-api-key", req.APIKey)
	httpReq.Header.Set("Content-Type", contentType)
	return doJSON(c.HTTP, httpReq, "elevenlabs")
}

func (c AssemblyAISpeechClient) Transcribe(ctx context.Context, req SpeechRequest) ([]byte, error) {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://api.assemblyai.com"
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	uploadURL, err := c.upload(ctx, httpClient, baseURL, req)
	if err != nil {
		return nil, err
	}
	id, err := c.submit(ctx, httpClient, baseURL, req, uploadURL)
	if err != nil {
		return nil, err
	}
	return c.poll(ctx, httpClient, baseURL, req.APIKey, id)
}

func (c AssemblyAISpeechClient) upload(ctx context.Context, httpClient HTTPDoer, baseURL string, req SpeechRequest) (string, error) {
	f, err := os.Open(req.AudioPath)
	if err != nil {
		return "", notoerr.Wrap("audio_read_failed", "Could not open audio file.", err)
	}
	defer f.Close()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v2/upload", f)
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create AssemblyAI upload request.", err)
	}
	httpReq.Header.Set("authorization", req.APIKey)
	respBytes, err := doJSON(httpClient, httpReq, "assemblyai")
	if err != nil {
		return "", err
	}
	var upload struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(respBytes, &upload); err != nil || upload.UploadURL == "" {
		return "", notoerr.Wrap("provider_response_invalid", "AssemblyAI upload response did not include upload_url.", err)
	}
	return upload.UploadURL, nil
}

func (c AssemblyAISpeechClient) submit(ctx context.Context, httpClient HTTPDoer, baseURL string, req SpeechRequest, uploadURL string) (string, error) {
	payload := map[string]any{
		"audio_url":          uploadURL,
		"language_detection": req.Language == "",
		"speech_models":      []string{"universal-3-pro", "universal-2"},
		"speaker_labels":     true,
	}
	if req.Language != "" {
		payload["language_code"] = req.Language
	}
	if len(req.ContextBias) > 0 {
		payload["keyterms_prompt"] = req.ContextBias
	}
	b, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v2/transcript", bytes.NewReader(b))
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create AssemblyAI transcript request.", err)
	}
	httpReq.Header.Set("authorization", req.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	respBytes, err := doJSON(httpClient, httpReq, "assemblyai")
	if err != nil {
		return "", err
	}
	var submitted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBytes, &submitted); err != nil || submitted.ID == "" {
		return "", notoerr.Wrap("provider_response_invalid", "AssemblyAI submit response did not include transcript id.", err)
	}
	return submitted.ID, nil
}

func (c AssemblyAISpeechClient) poll(ctx context.Context, httpClient HTTPDoer, baseURL string, apiKey string, id string) ([]byte, error) {
	interval := c.PollInterval
	if interval == 0 {
		interval = 3 * time.Second
	}
	maxPolls := c.MaxPolls
	if maxPolls == 0 {
		maxPolls = 120
	}
	for i := 0; i < maxPolls; i++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v2/transcript/"+id, nil)
		if err != nil {
			return nil, notoerr.Wrap("provider_request_failed", "Could not create AssemblyAI polling request.", err)
		}
		httpReq.Header.Set("authorization", apiKey)
		respBytes, err := doJSON(httpClient, httpReq, "assemblyai")
		if err != nil {
			return nil, err
		}
		var status struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(respBytes, &status); err != nil {
			return nil, notoerr.Wrap("provider_response_invalid", "Could not parse AssemblyAI polling response.", err)
		}
		switch status.Status {
		case "completed":
			return respBytes, nil
		case "error":
			return nil, notoerr.New("provider_failed", "AssemblyAI transcription failed.", map[string]any{"provider": "assemblyai", "error": status.Error})
		}
		select {
		case <-ctx.Done():
			return nil, notoerr.Wrap("provider_cancelled", "AssemblyAI transcription polling was cancelled.", ctx.Err())
		case <-time.After(interval):
		}
	}
	return nil, notoerr.New("provider_timeout", "AssemblyAI transcription did not finish before the polling limit.", map[string]any{"provider": "assemblyai", "transcript_id": id})
}

func multipartBody(audioPath string, fields map[string]string) (io.Reader, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if value == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", notoerr.Wrap("provider_request_failed", "Could not write multipart field.", err)
		}
	}
	f, err := os.Open(audioPath)
	if err != nil {
		return nil, "", notoerr.Wrap("audio_read_failed", "Could not open audio file.", err)
	}
	defer f.Close()
	part, err := writer.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return nil, "", notoerr.Wrap("provider_request_failed", "Could not create multipart audio field.", err)
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", notoerr.Wrap("provider_request_failed", "Could not write multipart audio data.", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", notoerr.Wrap("provider_request_failed", "Could not close multipart body.", err)
	}
	return &body, writer.FormDataContentType(), nil
}

func doJSON(client HTTPDoer, req *http.Request, provider string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("retryable_remote_error", "Provider request failed.", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not read provider response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notoerr.New("provider_failed", "Provider returned a non-success status.", map[string]any{
			"provider":    provider,
			"status_code": resp.StatusCode,
			"body":        string(b),
		})
	}
	return b, nil
}

func intString(n int) string {
	var buf [20]byte
	i := len(buf)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

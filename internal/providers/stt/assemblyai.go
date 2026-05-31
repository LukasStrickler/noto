package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
)

type AssemblyAIAdapter struct {
	BaseURL      string
	APIKey       string
	HTTP         HTTPDoer
	PollInterval time.Duration
	MaxPolls     int
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func (a *AssemblyAIAdapter) ProviderID() string {
	return "assemblyai"
}

func (a *AssemblyAIAdapter) FeatureMap() ProviderFeatures {
	return ProviderFeatures{
		ProviderID: "assemblyai",
		Features: []Feature{
			FeatureTranscribe,
			FeatureWordTimestamps,
			FeatureSpeakerDiarize,
			FeatureContextBiasing,
			FeatureOverlapDetection,
		},
		SpeedTier: "accurate",
		IsLocal:   false,
	}
}

func (a *AssemblyAIAdapter) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}

	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = "https://api.assemblyai.com"
	}

	uploadURL, err := a.upload(ctx, client, baseURL, audio)
	if err != nil {
		return nil, err
	}

	jobID, err := a.submit(ctx, client, baseURL, uploadURL, opts)
	if err != nil {
		return nil, err
	}

	rawResp, err := a.poll(ctx, client, baseURL, jobID)
	if err != nil {
		return nil, err
	}

	return a.parseResponse(rawResp, opts.MeetingID)
}

func (a *AssemblyAIAdapter) upload(ctx context.Context, client HTTPDoer, baseURL string, audio []byte) (string, error) {
	if a.APIKey == "" {
		return "", notoerr.New("missing_credential", "AssemblyAI API key is not configured.", nil)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v2/upload", bytes.NewReader(audio))
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create AssemblyAI upload request.", err)
	}
	req.Header.Set("authorization", a.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return "", notoerr.Wrap("retryable_remote_error", "AssemblyAI upload request failed.", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", notoerr.Wrap("provider_response_invalid", "Could not read AssemblyAI upload response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", notoerr.New("provider_failed", "AssemblyAI upload failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}

	var result struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil || result.UploadURL == "" {
		return "", notoerr.Wrap("provider_response_invalid", "AssemblyAI upload response missing upload_url.", err)
	}
	return result.UploadURL, nil
}

func (a *AssemblyAIAdapter) submit(ctx context.Context, client HTTPDoer, baseURL string, uploadURL string, opts TranscribeOptions) (string, error) {
	payload := map[string]any{
		"audio_url":          uploadURL,
		"language_detection": opts.Language == "",
		"speech_models":      []string{"universal-3-pro", "universal-2"},
		"speaker_labels":     true,
	}
	if opts.Language != "" {
		payload["language_code"] = opts.Language
	}
	if len(opts.ContextBias) > 0 {
		// AssemblyAI expects keyterms_prompt as an array of distinct terms,
		// not a single joined string.
		payload["keyterms_prompt"] = opts.ContextBias
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not marshal AssemblyAI transcript payload.", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v2/transcript", bytes.NewReader(body))
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create AssemblyAI transcript request.", err)
	}
	req.Header.Set("authorization", a.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", notoerr.Wrap("retryable_remote_error", "AssemblyAI transcript request failed.", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", notoerr.Wrap("provider_response_invalid", "Could not read AssemblyAI submit response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", notoerr.New("provider_failed", "AssemblyAI submit failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil || result.ID == "" {
		return "", notoerr.Wrap("provider_response_invalid", "AssemblyAI submit response missing id.", err)
	}
	return result.ID, nil
}

func (a *AssemblyAIAdapter) poll(ctx context.Context, client HTTPDoer, baseURL string, jobID string) ([]byte, error) {
	interval := a.PollInterval
	if interval == 0 {
		interval = 3 * time.Second
	}
	maxPolls := a.MaxPolls
	if maxPolls == 0 {
		maxPolls = 120
	}

	pollURL := strings.TrimRight(baseURL, "/") + "/v2/transcript/" + jobID

	timer := time.NewTimer(interval)
	defer timer.Stop()

	for i := 0; i < maxPolls; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, notoerr.Wrap("provider_request_failed", "Could not create AssemblyAI polling request.", err)
		}
		req.Header.Set("authorization", a.APIKey)

		resp, err := client.Do(req)
		if err != nil {
			return nil, notoerr.Wrap("retryable_remote_error", "AssemblyAI polling request failed.", err)
		}

		respBytes, err := io.ReadAll(resp.Body)
		// A Close error after a successful read is advisory (common with
		// keep-alive) and must not abort a transcription whose body we
		// already have; only the read error is fatal.
		_ = resp.Body.Close()
		if err != nil {
			return nil, notoerr.Wrap("provider_response_invalid", "Could not read AssemblyAI poll response.", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, notoerr.New("provider_failed", "AssemblyAI poll failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
		}

		var status struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(respBytes, &status); err != nil {
			return nil, notoerr.Wrap("provider_response_invalid", "Could not parse AssemblyAI poll status.", err)
		}

		switch status.Status {
		case "completed":
			return respBytes, nil
		case "error":
			return nil, notoerr.New("provider_failed", "AssemblyAI transcription failed.", map[string]any{"error": status.Error})
		case "queued", "processing":
		case "undefined":
		}

		select {
		case <-ctx.Done():
			return nil, notoerr.Wrap("provider_cancelled", "AssemblyAI transcription polling cancelled.", ctx.Err())
		case <-timer.C:
			// timer.C was already consumed by this case arm; calling
			// Stop() + draining again would block forever. Just reset.
			timer.Reset(interval)
		}
	}

	return nil, notoerr.New("provider_timeout", "AssemblyAI transcription timed out.", map[string]any{"transcript_id": jobID})
}

type assemblyAIResponse struct {
	Status        string      `json:"status"`
	ID            string      `json:"id"`
	LanguageCode  string      `json:"language_code"`
	Text          string      `json:"text"`
	Words         []word      `json:"words"`
	Utterances    []utterance `json:"utterances"`
	AudioDuration float64     `json:"audio_duration"`
}

type word struct {
	Text       string  `json:"text"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	Speaker    string  `json:"speaker"`
}

type utterance struct {
	Text       string  `json:"text"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Speaker    string  `json:"speaker"`
	Confidence float64 `json:"confidence"`
}

func (a *AssemblyAIAdapter) parseResponse(raw []byte, meetingID string) (*artifacts.Transcript, error) {
	var resp assemblyAIResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not parse AssemblyAI response.", err)
	}

	speakerMap := map[string]string{}
	var speakers []artifacts.Speaker
	var segments []artifacts.Segment

	for i, utt := range resp.Utterances {
		speakerLabel := utt.Speaker
		if speakerLabel == "" {
			speakerLabel = "unknown"
		}

		speakerID, ok := speakerMap[speakerLabel]
		if !ok {
			speakerID = speakerLabel
			speakerMap[speakerLabel] = speakerID
			speakers = append(speakers, artifacts.Speaker{
				ID:            speakerID,
				Label:         "Speaker " + speakerLabel,
				Origin:        "unknown",
				ProviderLabel: speakerLabel,
			})
		}

		conf := utt.Confidence
		confidence := &conf

		segments = append(segments, artifacts.Segment{
			ID:           segmentID(i),
			SpeakerID:    speakerID,
			StartSeconds: utt.Start,
			EndSeconds:   utt.End,
			Text:         utt.Text,
			Confidence:   confidence,
		})
	}

	var words []artifacts.Word
	for _, w := range resp.Words {
		speakerID := ""
		if w.Speaker != "" {
			speakerID = w.Speaker
		}
		conf := w.Confidence
		words = append(words, artifacts.Word{
			ID:           "word_" + w.Text,
			StartSeconds: w.Start,
			EndSeconds:   w.End,
			Text:         w.Text,
			Confidence:   &conf,
			SpeakerID:    speakerID,
		})
	}

	duration := resp.AudioDuration
	if len(resp.Utterances) > 0 {
		lastUtt := resp.Utterances[len(resp.Utterances)-1]
		if lastUtt.End > duration {
			duration = lastUtt.End
		}
	}

	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       meetingID,
		Language:        resp.LanguageCode,
		DurationSeconds: duration,
		Provider: artifacts.TranscriptProvider{
			ID:    "assemblyai",
			JobID: resp.ID,
		},
		Speakers: speakers,
		Segments: segments,
		Words:    words,
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps:     len(words) > 0,
			SpeakerDiarization: len(speakers) > 0,
		},
	}

	if err := transcript.Validate(); err != nil {
		return nil, notoerr.Wrap("transcript_invalid", "AssemblyAI transcript failed validation.", err)
	}

	return transcript, nil
}

func segmentID(i int) string {
	return fmt.Sprintf("seg_%06d", i+1)
}

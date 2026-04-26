package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
)

type SpeechmaticsAdapter struct {
	BaseURL string
	APIKey  string
	HTTP    HTTPDoer
}

func (a *SpeechmaticsAdapter) ProviderID() string {
	return "speechmatics"
}

func (a *SpeechmaticsAdapter) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = "https://speechmatics.com/api/v2"
	}

	jobID, err := a.submit(ctx, client, baseURL, audio, opts)
	if err != nil {
		return nil, err
	}

	rawResp, err := a.fetch(ctx, client, baseURL, jobID)
	if err != nil {
		return nil, err
	}

	return a.parseResponse(rawResp, opts.MeetingID)
}

func (a *SpeechmaticsAdapter) submit(ctx context.Context, client HTTPDoer, baseURL string, audio []byte, opts TranscribeOptions) (string, error) {
	fields := map[string]string{
		"model":                           "base",
		"language":                        opts.Language,
		"enable_speakers":                 "true",
		"enable_word_level_timestamps":    "true",
	}
	if opts.Language == "" {
		fields["language"] = "auto"
	}

	body, contentType, err := multipartWriter(fields, audio, "audio.mp3")
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(baseURL, "/") + "/asr"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create Speechmatics submit request.", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := client.Do(req)
	if err != nil {
		return "", notoerr.Wrap("retryable_remote_error", "Speechmatics submit request failed.", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", notoerr.Wrap("provider_response_invalid", "Could not read Speechmatics submit response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", notoerr.New("provider_failed", "Speechmatics submit failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}

	var result struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(respBytes, &result); err != nil || result.JobID == "" {
		return "", notoerr.Wrap("provider_response_invalid", "Speechmatics submit response missing job_id.", err)
	}
	return result.JobID, nil
}

func (a *SpeechmaticsAdapter) fetch(ctx context.Context, client HTTPDoer, baseURL string, jobID string) ([]byte, error) {
	url := strings.TrimRight(baseURL, "/") + "/asr/" + jobID + "/transcript"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create Speechmatics fetch request.", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("retryable_remote_error", "Speechmatics fetch request failed.", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not read Speechmatics fetch response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notoerr.New("provider_failed", "Speechmatics fetch failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}

	return respBytes, nil
}

type speechmaticsResponse struct {
	JobID     string `json:"job_id"`
	Text      string `json:"text"`
	Language  string `json:"language"`
	Duration  float64 `json:"duration"`
	Words     []word `json:"words"`
	Utterances []utterance `json:"utterances"`
}

type utterance struct {
	Text      string  `json:"text"`
	Start    float64 `json:"start"`
	End      float64 `json:"end"`
	Speaker  string  `json:"speaker"`
	Confidence float64 `json:"confidence"`
}

func (a *SpeechmaticsAdapter) parseResponse(raw []byte, meetingID string) (*artifacts.Transcript, error) {
	var resp speechmaticsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not parse Speechmatics response.", err)
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
		segments = append(segments, artifacts.Segment{
			ID:           segmentID(i),
			SpeakerID:    speakerID,
			StartSeconds: utt.Start,
			EndSeconds:   utt.End,
			Text:         utt.Text,
			Confidence:   &conf,
		})
	}

	var words []artifacts.Word
	for _, w := range resp.Words {
		conf := w.Confidence
		words = append(words, artifacts.Word{
			ID:           "word_" + w.Text,
			StartSeconds: w.Start,
			EndSeconds:   w.End,
			Text:         w.Text,
			Confidence:   &conf,
		})
	}

	transcript := &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       meetingID,
		Language:        resp.Language,
		DurationSeconds: resp.Duration,
		Provider: artifacts.TranscriptProvider{
			ID:    "speechmatics",
			JobID: resp.JobID,
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
		return nil, notoerr.Wrap("transcript_invalid", "Speechmatics transcript failed validation.", err)
	}

	return transcript, nil
}
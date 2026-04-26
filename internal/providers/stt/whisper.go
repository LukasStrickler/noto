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

type WhisperAdapter struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    HTTPDoer
}

func (a *WhisperAdapter) ProviderID() string {
	return "whisper"
}

func (a *WhisperAdapter) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	model := a.Model
	if model == "" {
		model = "whisper-1"
	}

	fields := map[string]string{
		"model": model,
		"response_format": "verbose_json",
	}
	if opts.Language != "" {
		fields["language"] = opts.Language
	}

	body, contentType, err := multipartWriter(fields, audio, "audio.mp3")
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(baseURL, "/") + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create Whisper request.", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("retryable_remote_error", "Whisper request failed.", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not read Whisper response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notoerr.New("provider_failed", "Whisper transcription failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}

	return a.parseResponse(respBytes, opts.MeetingID)
}

type whisperResponse struct {
	Text       string  `json:"text"`
	Language   string  `json:"language"`
	Duration   float64 `json:"duration"`
	Words      []word  `json:"words"`
	Segments   []seg   `json:"segments"`
}

type seg struct {
	ID             int     `json:"id"`
	Text           string  `json:"text"`
	Start          float64 `json:"start"`
	End            float64 `json:"end"`
	Confidence     float64 `json:"confidence"`
	AvgLogProb     float64 `json:"avg_logprob"`
	NoSpeechProb   float64 `json:"no_speech_prob"`
	SpeakerID      string  `json:"speaker_id,omitempty"`
	SpeakerLabel   string  `json:"speaker_label,omitempty"`
}

func (a *WhisperAdapter) parseResponse(raw []byte, meetingID string) (*artifacts.Transcript, error) {
	var resp whisperResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not parse Whisper response.", err)
	}

	speakerMap := map[string]string{}
	var speakers []artifacts.Speaker
	var segments []artifacts.Segment

	for i, seg := range resp.Segments {
		speakerLabel := seg.SpeakerLabel
		if speakerLabel == "" {
			speakerLabel = seg.SpeakerID
		}
		if speakerLabel == "" {
			speakerLabel = "speaker_1"
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

		conf := seg.Confidence
		segments = append(segments, artifacts.Segment{
			ID:           segmentID(i),
			SpeakerID:    speakerID,
			StartSeconds: seg.Start,
			EndSeconds:   seg.End,
			Text:         seg.Text,
			Confidence:   &conf,
		})
	}

	if len(segments) == 0 && resp.Text != "" {
		speakers = append(speakers, artifacts.Speaker{
			ID:            "speaker_1",
			Label:         "Speaker 1",
			Origin:        "unknown",
			ProviderLabel: "speaker_1",
		})
		segments = append(segments, artifacts.Segment{
			ID:           segmentID(0),
			SpeakerID:    "speaker_1",
			StartSeconds: 0,
			EndSeconds:   resp.Duration,
			Text:         resp.Text,
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
			ID: "whisper",
		},
		Speakers: speakers,
		Segments: segments,
		Words:    words,
		Capabilities: artifacts.TranscriptCapabilities{
			WordTimestamps:     len(words) > 0,
			SpeakerDiarization: len(speakers) > 1,
		},
	}

	if err := transcript.Validate(); err != nil {
		return nil, notoerr.Wrap("transcript_invalid", "Whisper transcript failed validation.", err)
	}

	return transcript, nil
}
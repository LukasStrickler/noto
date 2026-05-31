package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
	"github.com/lukasstrickler/noto/internal/prompts"
)

// summaryPromptVersion identifies the prompt-template revision recorded on each
// Summary. Defined once so the request builder and the parsed response can't
// drift apart.
const summaryPromptVersion = "summary.v1"

type OpenRouterAdapter struct {
	BaseURL string
	APIKey  string
	ModelID string
	HTTP    HTTPDoer
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func (a *OpenRouterAdapter) ProviderID() string {
	return "openrouter"
}

func (a *OpenRouterAdapter) Summarize(ctx context.Context, transcript artifacts.Transcript, opts SummarizeOptions) (*artifacts.Summary, error) {
	if strings.TrimSpace(a.APIKey) == "" {
		return nil, notoerr.New("provider_config_invalid", "OpenRouter API key is required.", nil)
	}

	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	modelID := a.ModelID
	if modelID == "" {
		modelID = "openai/gpt-4.1-mini"
	}

	messages := buildSummaryMessages(transcript)

	payload := map[string]any{
		"model":    modelID,
		"messages": messages,
	}
	if opts.Temperature != nil {
		payload["temperature"] = *opts.Temperature
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not marshal OpenRouter request body.", err)
	}
	if len(body) > 1024*1024 {
		return nil, notoerr.New("provider_request_too_large", "OpenRouter request body exceeds 1MB limit.", nil)
	}
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create OpenRouter request.", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/lukasstrickler/noto")
	req.Header.Set("X-Title", "Noto")

	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		resp, err = client.Do(req)
		if err != nil {
			return nil, notoerr.Wrap("retryable_remote_error", "OpenRouter request failed.", err)
		}

		respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if err != nil {
			return nil, notoerr.Wrap("provider_response_invalid", "Could not read OpenRouter response.", err)
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			if err := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return nil, notoerr.Wrap("provider_cancelled", "OpenRouter request cancelled during backoff.", err)
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return nil, notoerr.New("provider_client_error", "OpenRouter summarization failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
			}
			if err := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return nil, notoerr.Wrap("provider_cancelled", "OpenRouter request cancelled during backoff.", err)
			}
			continue
		}

		return parseOpenRouterResponse(respBytes, transcript, opts.MeetingID, modelID)
	}

	return nil, notoerr.New("provider_server_error", "OpenRouter service unavailable after retries.", nil)
}

func buildSummaryMessages(transcript artifacts.Transcript) []prompts.ChatMessage {
	var textBuilder strings.Builder
	totalChars := 0
	maxChars := 100_000
	maxSegments := 150

	for i, seg := range transcript.Segments {
		if i >= maxSegments || totalChars > maxChars {
			textBuilder.WriteString("... (truncated)")
			break
		}
		speaker := "Unknown"
		for _, sp := range transcript.Speakers {
			if sp.ID == seg.SpeakerID {
				speaker = sp.Label
				break
			}
		}
		segText := "[" + seg.ID + "] " + speaker + ": " + seg.Text + "\n"
		textBuilder.WriteString(segText)
		totalChars += len(segText)
	}

	// The system prompt comes from the versioned, few-shot/chain-of-thought
	// prompt builder (single source of truth). The user message keeps the
	// truncating serialization above so oversized transcripts stay under the
	// 1MB request guard.
	systemPrompt := prompts.NewPromptBuilder(summaryPromptVersion).SystemPrompt(prompts.SummaryTypeFull)
	userContent := "Please summarize this meeting transcript:\n\n" + textBuilder.String()

	return []prompts.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}
}

// sleepWithContext waits for d or until ctx is cancelled, whichever comes
// first, so retry backoff stays responsive to job cancellation.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseOpenRouterResponse(raw []byte, transcript artifacts.Transcript, meetingID string, modelID string) (*artifacts.Summary, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not parse OpenRouter response.", err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, notoerr.New("provider_response_invalid", "OpenRouter response did not include message content.", nil)
	}

	content := resp.Choices[0].Message.Content
	content = strings.Trim(content, " \n")

	var parsed struct {
		ShortSummary string `json:"short_summary"`
		Decisions    []struct {
			Text       string   `json:"text"`
			SpeakerIDs []string `json:"speaker_ids"`
			Evidence   []struct {
				SegmentID string `json:"segment_id"`
				Quote     string `json:"quote"`
			} `json:"evidence"`
		} `json:"decisions"`
		ActionItems []struct {
			Text     string `json:"text"`
			Owner    string `json:"owner"`
			DueAt    string `json:"due_at"`
			Evidence []struct {
				SegmentID string `json:"segment_id"`
				Quote     string `json:"quote"`
			} `json:"evidence"`
		} `json:"action_items"`
		Risks []struct {
			Text     string `json:"text"`
			Evidence []struct {
				SegmentID string `json:"segment_id"`
				Quote     string `json:"quote"`
			} `json:"evidence"`
		} `json:"risks"`
		OpenQuestions []struct {
			Text     string `json:"text"`
			Evidence []struct {
				SegmentID string `json:"segment_id"`
				Quote     string `json:"quote"`
			} `json:"evidence"`
		} `json:"open_questions"`
	}

	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		// The model ignored the JSON instruction and returned prose. Rather
		// than failing the whole summarization, fall back to using the raw
		// content as the short summary with no structured items. A failed
		// Unmarshal of non-JSON content leaves parsed at its zero value.
		parsed.ShortSummary = content
	}

	decisions := make([]artifacts.SummaryItem, len(parsed.Decisions))
	for i, d := range parsed.Decisions {
		evidence := make([]artifacts.Evidence, len(d.Evidence))
		for j, e := range d.Evidence {
			evidence[j] = artifacts.Evidence{
				SegmentID: e.SegmentID,
				Quote:     e.Quote,
			}
		}
		decisions[i] = artifacts.SummaryItem{
			Text:       d.Text,
			SpeakerIDs: d.SpeakerIDs,
			Evidence:   evidence,
		}
	}

	actionItems := make([]artifacts.ActionItem, len(parsed.ActionItems))
	for i, ai := range parsed.ActionItems {
		evidence := make([]artifacts.Evidence, len(ai.Evidence))
		for j, e := range ai.Evidence {
			evidence[j] = artifacts.Evidence{
				SegmentID: e.SegmentID,
				Quote:     e.Quote,
			}
		}
		actionItems[i] = artifacts.ActionItem{
			Text:     ai.Text,
			Owner:    ai.Owner,
			DueAt:    ai.DueAt,
			Evidence: evidence,
		}
	}

	risks := make([]artifacts.SummaryItem, len(parsed.Risks))
	for i, r := range parsed.Risks {
		evidence := make([]artifacts.Evidence, len(r.Evidence))
		for j, e := range r.Evidence {
			evidence[j] = artifacts.Evidence{
				SegmentID: e.SegmentID,
				Quote:     e.Quote,
			}
		}
		risks[i] = artifacts.SummaryItem{
			Text:     r.Text,
			Evidence: evidence,
		}
	}

	openQuestions := make([]artifacts.SummaryItem, len(parsed.OpenQuestions))
	for i, oq := range parsed.OpenQuestions {
		evidence := make([]artifacts.Evidence, len(oq.Evidence))
		for j, e := range oq.Evidence {
			evidence[j] = artifacts.Evidence{
				SegmentID: e.SegmentID,
				Quote:     e.Quote,
			}
		}
		openQuestions[i] = artifacts.SummaryItem{
			Text:     oq.Text,
			Evidence: evidence,
		}
	}

	summary := &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		ShortSummary:  parsed.ShortSummary,
		Decisions:     decisions,
		ActionItems:   actionItems,
		OpenQuestions: openQuestions,
		Risks:         risks,
		Model: artifacts.SummaryModel{
			Provider:      "openrouter",
			ModelID:       modelID,
			PromptVersion: summaryPromptVersion,
		},
	}

	if err := artifacts.ValidateSummary(*summary, transcript); err != nil {
		return nil, notoerr.Wrap("summary_invalid", "OpenRouter summary failed validation.", err)
	}

	return summary, nil
}

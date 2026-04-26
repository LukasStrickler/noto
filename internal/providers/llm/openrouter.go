package llm

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

type OpenRouterSummaryAdapter struct {
	ModelID string
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type OpenRouterClient struct {
	BaseURL string
	HTTP    HTTPDoer
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	APIKey      string
	ModelID     string
	Messages    []ChatMessage
	Temperature *float64
}

func (c OpenRouterClient) Chat(ctx context.Context, req ChatRequest) (string, error) {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	modelID := req.ModelID
	if modelID == "" {
		modelID = "openai/gpt-4.1-mini"
	}
	payload := map[string]any{
		"model":    modelID,
		"messages": req.Messages,
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	b, _ := json.Marshal(payload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create OpenRouter request.", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("HTTP-Referer", "https://github.com/lukasstrickler/noto")
	httpReq.Header.Set("X-Title", "Noto")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", notoerr.Wrap("retryable_remote_error", "OpenRouter request failed.", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", notoerr.Wrap("provider_response_invalid", "Could not read OpenRouter response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", notoerr.New("provider_failed", "OpenRouter returned a non-success status.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return "", notoerr.Wrap("provider_response_invalid", "Could not parse OpenRouter response.", err)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", notoerr.New("provider_response_invalid", "OpenRouter response did not include message content.", nil)
	}
	return parsed.Choices[0].Message.Content, nil
}

func (a OpenRouterSummaryAdapter) Summarize(_ context.Context, transcript artifacts.Transcript) (artifacts.Summary, error) {
	modelID := a.ModelID
	if modelID == "" {
		modelID = "openai/gpt-4.1-mini"
	}
	first := transcript.Segments[0]
	summary := artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     transcript.MeetingID,
		ShortSummary:  first.Text,
		Decisions: []artifacts.SummaryItem{{
			Text: "Review the cited transcript segment.",
			Evidence: []artifacts.Evidence{{
				SegmentID: first.ID,
				Quote:     first.Text,
			}},
		}},
		ActionItems:   []artifacts.ActionItem{},
		OpenQuestions: []artifacts.SummaryItem{},
		Risks:         []artifacts.SummaryItem{},
		Model: artifacts.SummaryModel{
			Provider:      "openrouter",
			ModelID:       modelID,
			PromptVersion: "summary.v1",
		},
	}
	return summary, artifacts.ValidateSummary(summary, transcript)
}

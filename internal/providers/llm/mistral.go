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

type MistralAdapter struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    HTTPDoer
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func (a *MistralAdapter) ProviderID() string {
	return "mistral"
}

func (a *MistralAdapter) Summarize(ctx context.Context, transcript artifacts.Transcript, opts SummarizeOptions) (*artifacts.Summary, error) {
	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = "https://api.mistral.ai/v1"
	}

	model := a.Model
	if model == "" {
		model = "mistral-large-latest"
	}

	messages := buildMessages(transcript)

	payload := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if opts.Temperature != nil {
		payload["temperature"] = *opts.Temperature
	} else {
		payload["temperature"] = 0.2
	}

	body, _ := json.Marshal(payload)
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, notoerr.Wrap("provider_request_failed", "Could not create Mistral request.", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("retryable_remote_error", "Mistral request failed.", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not read Mistral response.", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, notoerr.New("provider_failed", "Mistral summarization failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
	}

	return a.parseResponse(respBytes, transcript, opts.MeetingID, model)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type mistralResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int    `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func buildMessages(transcript artifacts.Transcript) []chatMessage {
	var textBuilder strings.Builder
	for i, seg := range transcript.Segments {
		speaker := "Unknown"
		for _, sp := range transcript.Speakers {
			if sp.ID == seg.SpeakerID {
				speaker = sp.Label
				break
			}
		}
		textBuilder.WriteString(speaker)
		textBuilder.WriteString(": ")
		textBuilder.WriteString(seg.Text)
		textBuilder.WriteString("\n")
		if i >= 50 {
			textBuilder.WriteString("... (truncated)")
			break
		}
	}

	systemPrompt := `You are a meeting summarization assistant. Given a transcript, extract:
1. A short 2-sentence summary of the meeting
2. Key decisions made (with brief description)
3. Action items (with potential assignees, use @person format)
4. Risks or concerns mentioned
5. Open questions or unresolved topics

Return your response as a JSON object with the following structure:
{
  "short_summary": "...",
  "decisions": [{"text": "...", "speaker_ids": [...], "evidence": [{"segment_id": "...", "quote": "..."}]}],
  "action_items": [{"text": "...", "owner": "@person", "evidence": [...]}],
  "risks": [{"text": "...", "evidence": [...]}],
  "open_questions": [{"text": "...", "evidence": [...]}]
}`

	userContent := "Please summarize this meeting transcript:\n\n" + textBuilder.String()

	return []chatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}
}

func (a *MistralAdapter) parseResponse(raw []byte, transcript artifacts.Transcript, meetingID string, model string) (*artifacts.Summary, error) {
	var resp mistralResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, notoerr.Wrap("provider_response_invalid", "Could not parse Mistral response.", err)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, notoerr.New("provider_response_invalid", "Mistral response did not include message content.", nil)
	}

	content := resp.Choices[0].Message.Content
	content = strings.Trim(content, " \n")

	var parsed struct {
		ShortSummary  string `json:"short_summary"`
		Decisions     []struct {
			Text       string `json:"text"`
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
		summary := artifacts.Summary{
			SchemaVersion: "summary.v1",
			MeetingID:     meetingID,
			ShortSummary:  content,
			Model: artifacts.SummaryModel{
				Provider: "mistral",
				ModelID:  model,
			},
		}
		return &summary, nil
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
			Provider:      "mistral",
			ModelID:       model,
			PromptVersion: "summary.v1",
		},
	}

	if err := summary.Validate(); err != nil {
		return nil, notoerr.Wrap("summary_invalid", "Mistral summary failed validation.", err)
	}

	return summary, nil
}
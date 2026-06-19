package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/notoerr"
	"github.com/lukasstrickler/noto/internal/platform/providers/llm/prompts"
)

// summaryPromptVersion identifies the prompt-template revision recorded on each
// Summary. Bumped to v2 for the @S1-token + two-pass (extract → verify →
// refine/gap) pipeline.
const summaryPromptVersion = "summary.v2"

// defaults for the generation request.
const (
	defaultModelID     = "google/gemini-3.1-flash-preview"
	defaultTemperature = 0.1
	defaultMaxTokens   = 8000
)

type OpenRouterAdapter struct {
	BaseURL string
	APIKey  string
	ModelID string
	HTTP    HTTPDoer

	// Temperature overrides the default extraction temperature (0.1) when set.
	Temperature *float64
	// MaxTokens caps the response; 0 uses defaultMaxTokens.
	MaxTokens int

	// Privacy guards map onto OpenRouter's `provider` routing block. They are
	// set from config (default ON) so transcripts only reach privacy-respecting
	// endpoints unless the user relaxes them.
	ZDR                bool
	DenyDataCollection bool
	RequireParameters  bool
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

func (a *OpenRouterAdapter) ProviderID() string {
	return "openrouter"
}

// Summarize runs the two-pass, evidence-grounded summarization pipeline:
//
//  1. extract  — one structured-output call over the whole (token-attributed)
//     transcript;
//  2. verify   — deterministic quote grounding (no LLM, free);
//  3. refine   — one call that does gap analysis, repairs ungrounded quotes,
//     and prunes hallucinations;
//  4. verify   — re-score and attach coverage insights.
//
// On any provider error the best result obtained so far is returned; the job
// only fails if the very first call fails.
func (a *OpenRouterAdapter) Summarize(ctx context.Context, transcript artifacts.Transcript, opts SummarizeOptions) (*artifacts.Summary, error) {
	if strings.TrimSpace(a.APIKey) == "" {
		return nil, notoerr.New("provider_config_invalid", "OpenRouter API key is required.", nil)
	}
	if transcript.MeetingID == "" {
		transcript.MeetingID = opts.MeetingID
	}

	modelID := a.ModelID
	if modelID == "" {
		modelID = defaultModelID
	}

	builder := prompts.NewPromptBuilder(summaryPromptVersion)

	// Pass 1 — extract.
	extractContent, err := a.chat(ctx, []prompts.ChatMessage{
		{Role: "system", Content: builder.SystemPrompt(prompts.SummaryTypeFull)},
		{Role: "user", Content: transcriptUserMessage(builder, transcript)},
	})
	if err != nil {
		return nil, err
	}
	draft := parseSummaryContent(extractContent, opts.MeetingID, modelID)
	sanitizeEvidence(draft, transcript)
	artifacts.VerifyAndScore(draft, transcript)

	best := draft

	// Pass 2 — refine + gap analysis. Best-effort: a failure here keeps the draft.
	refineUser := refineUserMessage(builder, transcript, draft)
	if refineContent, rerr := a.chat(ctx, []prompts.ChatMessage{
		{Role: "system", Content: builder.RefineSystemPrompt()},
		{Role: "user", Content: refineUser},
	}); rerr == nil {
		refined := parseSummaryContent(refineContent, opts.MeetingID, modelID)
		sanitizeEvidence(refined, transcript)
		artifacts.VerifyAndScore(refined, transcript)
		// Adopt the refined pass unless it regressed the count of grounded
		// items (which would mean the reviewer dropped real, verified content).
		if refined.Coverage == nil || best.Coverage == nil || refined.Coverage.ItemsGrounded >= best.Coverage.ItemsGrounded {
			best = refined
		}
	}

	best.MeetingID = opts.MeetingID
	best.Model = artifacts.SummaryModel{Provider: "openrouter", ModelID: modelID, PromptVersion: summaryPromptVersion}

	if err := artifacts.ValidateSummary(*best, transcript); err != nil {
		return nil, notoerr.Wrap("summary_invalid", "OpenRouter summary failed validation.", err)
	}
	return best, nil
}

// transcriptUserMessage renders the token-attributed transcript (no embedded
// system prompt — that travels as the system message). Build only fails on a
// missing meeting_id, which Summarize has already populated.
func transcriptUserMessage(b *prompts.PromptBuilder, transcript artifacts.Transcript) string {
	msg, err := b.Build("", transcript)
	if err != nil {
		return "## Meeting Transcript\n(unavailable)"
	}
	return msg
}

// refineUserMessage gives the reviewer the transcript, the draft as JSON, and an
// explicit list of items whose quotes did not verify, so it can target repairs.
func refineUserMessage(b *prompts.PromptBuilder, transcript artifacts.Transcript, draft *artifacts.Summary) string {
	var sb strings.Builder
	sb.WriteString(transcriptUserMessage(b, transcript))
	sb.WriteString("\n\n## Draft summary (JSON)\n")
	if raw, err := json.Marshal(draftView(draft)); err == nil {
		sb.Write(raw)
	}
	if unverified := unverifiedItems(draft); len(unverified) > 0 {
		sb.WriteString("\n\n## Unverified items (quote not found verbatim in cited segment — fix or remove)\n")
		for _, u := range unverified {
			sb.WriteString("- ")
			sb.WriteString(u)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// draftView is the model-facing projection of the draft (drops internal scoring
// fields so the reviewer sees the same shape it must return).
func draftView(s *artifacts.Summary) map[string]any {
	return map[string]any{
		"short_summary":  s.ShortSummary,
		"decisions":      s.Decisions,
		"action_items":   s.ActionItems,
		"risks":          s.Risks,
		"open_questions": s.OpenQuestions,
	}
}

func unverifiedItems(s *artifacts.Summary) []string {
	var out []string
	collect := func(kind string, items []artifacts.SummaryItem) {
		for _, it := range items {
			if it.Confidence == 0 {
				out = append(out, kind+": "+it.Text)
			}
		}
	}
	collect("decision", s.Decisions)
	collect("risk", s.Risks)
	collect("open_question", s.OpenQuestions)
	for _, it := range s.ActionItems {
		if it.Confidence == 0 {
			out = append(out, "action_item: "+it.Text)
		}
	}
	return out
}

// chat issues a single chat-completions request with structured output and the
// privacy provider-routing block, retrying transient failures with backoff.
// Returns the assistant message content (code fences stripped).
func (a *OpenRouterAdapter) chat(ctx context.Context, messages []prompts.ChatMessage) (string, error) {
	return a.post(ctx, messages, a.responseFormat())
}

// CompleteText sends one optional system + one user turn and returns the model's
// plain-text reply. Unlike the summary path it forces NO JSON schema, so a caller that
// wants structure must prompt for it. Same auth/retry/transport as Summarize. It exists
// for the bench context-correction repair source — a short, text-only span fix — and is
// not used by the product pipeline.
func (a *OpenRouterAdapter) CompleteText(ctx context.Context, system, user string) (string, error) {
	if strings.TrimSpace(a.APIKey) == "" {
		return "", notoerr.New("provider_config_invalid", "OpenRouter API key is required.", nil)
	}
	msgs := make([]prompts.ChatMessage, 0, 2)
	if strings.TrimSpace(system) != "" {
		msgs = append(msgs, prompts.ChatMessage{Role: "system", Content: system})
	}
	msgs = append(msgs, prompts.ChatMessage{Role: "user", Content: user})
	return a.post(ctx, msgs, nil)
}

// post is the shared OpenRouter chat-completions transport: marshal + size-guard the
// body, authenticate, retry with backoff on 429/5xx, and extract the assistant text.
// responseFormat is sent only when non-nil — the summary path forces a JSON schema; a
// text completion omits it so the model is free to reply in prose.
func (a *OpenRouterAdapter) post(ctx context.Context, messages []prompts.ChatMessage, responseFormat map[string]any) (string, error) {
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	baseURL := a.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	modelID := a.ModelID
	if modelID == "" {
		modelID = defaultModelID
	}
	temperature := defaultTemperature
	if a.Temperature != nil {
		temperature = *a.Temperature
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	payload := map[string]any{
		"model":       modelID,
		"messages":    messages,
		"temperature": temperature,
		"max_tokens":  maxTokens,
	}
	if responseFormat != nil {
		payload["response_format"] = responseFormat
	}
	if provider := a.providerRouting(); len(provider) > 0 {
		payload["provider"] = provider
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not marshal OpenRouter request body.", err)
	}
	if len(body) > 1024*1024 {
		return "", notoerr.New("provider_request_too_large", "OpenRouter request body exceeds 1MB limit.", nil)
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", notoerr.Wrap("provider_request_failed", "Could not create OpenRouter request.", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/lukasstrickler/noto")
	req.Header.Set("X-Title", "Noto")

	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", notoerr.Wrap("retryable_remote_error", "OpenRouter request failed.", err)
		}

		respBytes, rerr := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if rerr != nil {
			return "", notoerr.Wrap("provider_response_invalid", "Could not read OpenRouter response.", rerr)
		}

		if resp.StatusCode == 429 || resp.StatusCode == 502 || resp.StatusCode == 503 || resp.StatusCode == 504 {
			if werr := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); werr != nil {
				return "", notoerr.Wrap("provider_cancelled", "OpenRouter request cancelled during backoff.", werr)
			}
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if resp.StatusCode >= 400 && resp.StatusCode < 500 {
				return "", notoerr.New("provider_client_error", "OpenRouter summarization failed.", map[string]any{"status_code": resp.StatusCode, "body": string(respBytes)})
			}
			if werr := sleepWithContext(ctx, time.Duration(1<<attempt)*time.Second); werr != nil {
				return "", notoerr.Wrap("provider_cancelled", "OpenRouter request cancelled during backoff.", werr)
			}
			continue
		}
		return extractContent(respBytes)
	}
	return "", notoerr.New("provider_server_error", "OpenRouter service unavailable after retries.", nil)
}

// responseFormat asks for JSON. With RequireParameters on, OpenRouter only
// routes to providers that honor a strict json_schema, so we use it; otherwise
// we fall back to the broadly-supported json_object to still nudge valid JSON.
func (a *OpenRouterAdapter) responseFormat() map[string]any {
	if a.RequireParameters {
		return map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "meeting_summary",
				"strict": false,
				"schema": summaryJSONSchema(),
			},
		}
	}
	return map[string]any{"type": "json_object"}
}

// providerRouting builds OpenRouter's `provider` block from the privacy guards.
func (a *OpenRouterAdapter) providerRouting() map[string]any {
	p := map[string]any{}
	if a.ZDR {
		p["zdr"] = true
	}
	if a.DenyDataCollection {
		p["data_collection"] = "deny"
	}
	if a.RequireParameters {
		p["require_parameters"] = true
	}
	return p
}

func summaryJSONSchema() map[string]any {
	evidence := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"segment_id": map[string]any{"type": "string"},
				"quote":      map[string]any{"type": "string"},
			},
		},
	}
	item := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":        map[string]any{"type": "string"},
				"speaker_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"evidence":    evidence,
			},
		},
	}
	action := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":     map[string]any{"type": "string"},
				"owner":    map[string]any{"type": "string"},
				"due_at":   map[string]any{"type": "string"},
				"evidence": evidence,
			},
		},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"short_summary":  map[string]any{"type": "string"},
			"decisions":      item,
			"action_items":   action,
			"risks":          item,
			"open_questions": item,
		},
	}
}

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

// extractContent pulls the assistant message text out of a chat-completions
// response and strips any markdown code fences a model wrapped it in.
func extractContent(raw []byte) (string, error) {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", notoerr.Wrap("provider_response_invalid", "Could not parse OpenRouter response.", err)
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return "", notoerr.New("provider_response_invalid", "OpenRouter response did not include message content.", nil)
	}
	return stripCodeFences(strings.TrimSpace(resp.Choices[0].Message.Content)), nil
}

// stripCodeFences removes a leading ```json / ``` fence and trailing ``` so a
// fenced JSON object parses. Leaves already-bare content untouched.
func stripCodeFences(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	// Drop an optional language tag on the first line (e.g. "json").
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		first := strings.TrimSpace(s[:i])
		if first == "" || !strings.ContainsAny(first, "{[") {
			s = s[i+1:]
		}
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// parseSummaryContent unmarshals model JSON into a Summary. If the content is
// not valid JSON it degrades gracefully, using the raw text as the short
// summary with no structured items (so a misbehaving model never loses the
// whole job). Validation/grounding happen in the caller.
func parseSummaryContent(content, meetingID, modelID string) *artifacts.Summary {
	var parsed struct {
		ShortSummary string `json:"short_summary"`
		Decisions    []struct {
			Text       string   `json:"text"`
			SpeakerIDs []string `json:"speaker_ids"`
			Evidence   []rawEvi `json:"evidence"`
		} `json:"decisions"`
		ActionItems []struct {
			Text     string   `json:"text"`
			Owner    string   `json:"owner"`
			DueAt    string   `json:"due_at"`
			Evidence []rawEvi `json:"evidence"`
		} `json:"action_items"`
		Risks []struct {
			Text       string   `json:"text"`
			SpeakerIDs []string `json:"speaker_ids"`
			Evidence   []rawEvi `json:"evidence"`
		} `json:"risks"`
		OpenQuestions []struct {
			Text       string   `json:"text"`
			SpeakerIDs []string `json:"speaker_ids"`
			Evidence   []rawEvi `json:"evidence"`
		} `json:"open_questions"`
	}

	summary := &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		Model:         artifacts.SummaryModel{Provider: "openrouter", ModelID: modelID, PromptVersion: summaryPromptVersion},
	}

	if err := json.Unmarshal([]byte(extractJSONObject(content)), &parsed); err != nil {
		summary.ShortSummary = content
		return summary
	}

	summary.ShortSummary = parsed.ShortSummary
	for _, d := range parsed.Decisions {
		summary.Decisions = append(summary.Decisions, artifacts.SummaryItem{Text: d.Text, SpeakerIDs: d.SpeakerIDs, Evidence: toEvidence(d.Evidence)})
	}
	for _, ai := range parsed.ActionItems {
		summary.ActionItems = append(summary.ActionItems, artifacts.ActionItem{Text: ai.Text, Owner: ai.Owner, DueAt: ai.DueAt, Evidence: toEvidence(ai.Evidence)})
	}
	for _, r := range parsed.Risks {
		summary.Risks = append(summary.Risks, artifacts.SummaryItem{Text: r.Text, SpeakerIDs: r.SpeakerIDs, Evidence: toEvidence(r.Evidence)})
	}
	for _, q := range parsed.OpenQuestions {
		summary.OpenQuestions = append(summary.OpenQuestions, artifacts.SummaryItem{Text: q.Text, SpeakerIDs: q.SpeakerIDs, Evidence: toEvidence(q.Evidence)})
	}
	return summary
}

type rawEvi struct {
	SegmentID string `json:"segment_id"`
	Quote     string `json:"quote"`
}

func toEvidence(in []rawEvi) []artifacts.Evidence {
	if len(in) == 0 {
		return nil
	}
	out := make([]artifacts.Evidence, len(in))
	for i, e := range in {
		out[i] = artifacts.Evidence{SegmentID: e.SegmentID, Quote: e.Quote}
	}
	return out
}

// extractJSONObject returns the substring from the first '{' to the last '}',
// salvaging a JSON object a model wrapped in stray prose. Returns the input
// unchanged when no braces are found.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}

// sanitizeEvidence drops evidence whose segment_id is empty or not present in
// the transcript, so a model citing a non-existent segment can't fail
// ValidateSummary for the whole job. Ungrounded items survive (with empty
// evidence) and are surfaced as low-confidence by VerifyAndScore.
func sanitizeEvidence(summary *artifacts.Summary, transcript artifacts.Transcript) {
	valid := make(map[string]bool, len(transcript.Segments))
	for _, seg := range transcript.Segments {
		valid[seg.ID] = true
	}
	clean := func(ev []artifacts.Evidence) []artifacts.Evidence {
		out := ev[:0]
		for _, e := range ev {
			if e.SegmentID != "" && valid[e.SegmentID] {
				out = append(out, e)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	for i := range summary.Decisions {
		summary.Decisions[i].Evidence = clean(summary.Decisions[i].Evidence)
	}
	for i := range summary.ActionItems {
		summary.ActionItems[i].Evidence = clean(summary.ActionItems[i].Evidence)
	}
	for i := range summary.Risks {
		summary.Risks[i].Evidence = clean(summary.Risks[i].Evidence)
	}
	for i := range summary.OpenQuestions {
		summary.OpenQuestions[i].Evidence = clean(summary.OpenQuestions[i].Evidence)
	}
}

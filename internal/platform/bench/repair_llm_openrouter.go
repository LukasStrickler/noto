package bench

import (
	"context"
	"strings"
)

// repair_llm_openrouter.go is the live SpanCorrector: it turns a CorrectionRequest into a
// tight, context-grounded prompt, sends it through an injected text completion (the
// OpenRouter adapter's CompleteText in production, a closure in tests), and parses the
// reply back into bare span text. The prompt is deliberately CONSERVATIVE — return the
// span unchanged when unsure — because the B7 gate already rejects net-negative
// corrections; the prompt's job is to avoid gratuitous rewrites, not to police accuracy.
//
// The repair core stays free of any provider import: the LLM arrives as a CompleteFunc
// injected at the wiring layer, so this file has no network and is fully fake-tested.

// CompleteFunc is the one LLM capability the corrector needs — a single system+user turn
// returning the model's text. *llm.OpenRouterAdapter.CompleteText satisfies it; tests
// pass a closure. Keeping it a func value (rather than importing the llm package here)
// keeps the bench repair core decoupled from the provider.
type CompleteFunc func(ctx context.Context, system, user string) (string, error)

const correctionSystemPrompt = "You are a careful speech-to-text post-editor. " +
	"You receive a SHORT SPAN of an automatic transcript that may contain recognition errors, " +
	"plus the surrounding text for context. Using ONLY the context and ordinary language and spelling, " +
	"return the corrected words FOR THE SPAN ONLY. Rules: keep roughly the same number of words; " +
	"never add or rewrite the surrounding context; output no commentary, quotes, or labels; " +
	"if the span already looks correct or you are unsure, return it unchanged. " +
	"Output only the corrected span text."

type llmSpanCorrector struct{ complete CompleteFunc }

// NewLLMSpanCorrector builds a SpanCorrector backed by a text-completion function.
func NewLLMSpanCorrector(complete CompleteFunc) SpanCorrector { return &llmSpanCorrector{complete: complete} }

// Correct prompts the model with the span + its context and returns the cleaned span
// text. An empty span needs no call; an empty/unusable reply falls back to the original
// span (a wash, never worse than do-nothing).
func (c *llmSpanCorrector) Correct(ctx context.Context, req CorrectionRequest) (string, error) {
	if strings.TrimSpace(req.SpanText) == "" {
		return "", nil
	}
	out, err := c.complete(ctx, correctionSystemPrompt, buildCorrectionUser(req))
	if err != nil {
		return "", err
	}
	return cleanCorrection(out, req.SpanText), nil
}

func buildCorrectionUser(req CorrectionRequest) string {
	var b strings.Builder
	b.WriteString("Context before: ")
	b.WriteString(req.Before)
	b.WriteString("\nSpan to correct: ")
	b.WriteString(req.SpanText)
	b.WriteString("\nContext after: ")
	b.WriteString(req.After)
	b.WriteString("\nCorrected span:")
	return b.String()
}

// cleanCorrection normalizes a model reply into bare span text: strip code fences and
// quotes, drop a leading "Corrected span:" label the model sometimes echoes, and keep
// only the first non-empty line (models occasionally append a note). An empty result
// falls back to the original span.
func cleanCorrection(out, fallback string) string {
	s := stripFences(out)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	for _, label := range []string{"Corrected span:", "Corrected:", "Correction:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, label))
	}
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`))
	if s == "" {
		return fallback
	}
	return s
}

// stripFences removes a leading ```/```lang fence and a trailing ``` if the model
// wrapped its answer in a code block.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

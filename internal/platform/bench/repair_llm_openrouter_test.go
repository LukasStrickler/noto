package bench

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestLLMSpanCorrector_ReturnsCleanedText(t *testing.T) {
	cases := []struct{ reply, want string }{
		{"quick brown", "quick brown"},
		{"Corrected span: quick brown", "quick brown"},
		{"```\nquick brown\n```", "quick brown"},
		{"\"quick brown\"", "quick brown"},
		{"quick brown\n(unsure about the second word)", "quick brown"},
	}
	for _, tc := range cases {
		c := NewLLMSpanCorrector(func(context.Context, string, string) (string, error) { return tc.reply, nil })
		got, err := c.Correct(context.Background(), CorrectionRequest{SpanText: "kwik braun"})
		if err != nil {
			t.Fatalf("Correct(%q): %v", tc.reply, err)
		}
		if got != tc.want {
			t.Errorf("reply %q → %q, want %q", tc.reply, got, tc.want)
		}
	}
}

func TestLLMSpanCorrector_EmptyReplyFallsBackToSpan(t *testing.T) {
	c := NewLLMSpanCorrector(func(context.Context, string, string) (string, error) { return "   ", nil })
	got, err := c.Correct(context.Background(), CorrectionRequest{SpanText: "kwik braun"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "kwik braun" {
		t.Errorf("empty reply must fall back to the span, got %q", got)
	}
}

func TestLLMSpanCorrector_EmptySpanSkipsCall(t *testing.T) {
	called := false
	c := NewLLMSpanCorrector(func(context.Context, string, string) (string, error) { called = true; return "x", nil })
	if _, err := c.Correct(context.Background(), CorrectionRequest{SpanText: "  "}); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("an empty span must not trigger an LLM call")
	}
}

func TestLLMSpanCorrector_PropagatesError(t *testing.T) {
	c := NewLLMSpanCorrector(func(context.Context, string, string) (string, error) { return "", errors.New("llm down") })
	if _, err := c.Correct(context.Background(), CorrectionRequest{SpanText: "kwik"}); err == nil {
		t.Error("expected the completion error to propagate")
	}
}

func TestLLMSpanCorrector_PromptCarriesContext(t *testing.T) {
	var gotUser string
	c := NewLLMSpanCorrector(func(_ context.Context, _ string, user string) (string, error) { gotUser = user; return "quick brown", nil })
	_, err := c.Correct(context.Background(), CorrectionRequest{Before: "the", SpanText: "kwik braun", After: "fox"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"the", "kwik braun", "fox"} {
		if !strings.Contains(gotUser, want) {
			t.Errorf("prompt missing %q:\n%s", want, gotUser)
		}
	}
}

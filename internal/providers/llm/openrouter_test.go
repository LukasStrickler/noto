package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoerr"
)

// fakeDoer returns a canned response (or error) and counts invocations so
// tests can assert retry behaviour.
type fakeDoer struct {
	status int
	body   string
	err    error
	calls  int
}

func (f *fakeDoer) Do(_ *http.Request) (*http.Response, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     make(http.Header),
	}, nil
}

func testTranscript() artifacts.Transcript {
	return artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test",
		Provider:      artifacts.TranscriptProvider{ID: "test"},
		Speakers:      []artifacts.Speaker{{ID: "spk_0", Label: "Alice"}},
		Segments:      []artifacts.Segment{{ID: "seg_000001", SpeakerID: "spk_0", Text: "We will ship v1 next week."}},
	}
}

// chatResponse wraps model content in the OpenRouter chat-completions shape,
// JSON-encoding the content so it is escaped correctly.
func chatResponse(content string) string {
	b, _ := json.Marshal(content)
	return `{"choices":[{"message":{"content":` + string(b) + `}}]}`
}

func errCode(err error) string {
	var ne *notoerr.Error
	if errors.As(err, &ne) {
		return ne.Code
	}
	return ""
}

func TestSummarize_StructuredJSON(t *testing.T) {
	content := `{"short_summary":"Team agreed to ship v1.","decisions":[{"text":"Ship v1","speaker_ids":["spk_0"],"evidence":[{"segment_id":"seg_000001","quote":"ship v1"}]}]}`
	doer := &fakeDoer{status: 200, body: chatResponse(content)}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer}

	sum, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if sum.ShortSummary != "Team agreed to ship v1." {
		t.Errorf("short summary = %q", sum.ShortSummary)
	}
	if len(sum.Decisions) != 1 || sum.Decisions[0].Text != "Ship v1" {
		t.Fatalf("decisions = %+v", sum.Decisions)
	}
	if len(sum.Decisions[0].Evidence) != 1 || sum.Decisions[0].Evidence[0].SegmentID != "seg_000001" {
		t.Errorf("evidence = %+v", sum.Decisions[0].Evidence)
	}
	if sum.Model.Provider != "openrouter" {
		t.Errorf("model provider = %q, want openrouter", sum.Model.Provider)
	}
}

func TestSummarize_NonJSONFallback(t *testing.T) {
	doer := &fakeDoer{status: 200, body: chatResponse("Here is a plain prose summary, not JSON.")}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer}

	sum, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"})
	if err != nil {
		t.Fatalf("Summarize should fall back on non-JSON content, got error: %v", err)
	}
	if sum.ShortSummary != "Here is a plain prose summary, not JSON." {
		t.Errorf("expected raw content used as short summary, got %q", sum.ShortSummary)
	}
	if len(sum.Decisions) != 0 || len(sum.ActionItems) != 0 || len(sum.Risks) != 0 || len(sum.OpenQuestions) != 0 {
		t.Errorf("expected no structured items on fallback")
	}
}

func TestSummarize_ClientErrorNoRetry(t *testing.T) {
	doer := &fakeDoer{status: 400, body: `{"error":"bad request"}`}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer}

	_, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"})
	if err == nil {
		t.Fatal("expected an error on HTTP 400")
	}
	if code := errCode(err); code != "provider_client_error" {
		t.Errorf("error code = %q, want provider_client_error", code)
	}
	if doer.calls != 1 {
		t.Errorf("expected exactly 1 call (4xx must not retry), got %d", doer.calls)
	}
}

func TestSummarize_BackoffRespectsContextCancellation(t *testing.T) {
	doer := &fakeDoer{status: 503, body: "server overloaded"}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the call; the retry backoff must bail out at once

	start := time.Now()
	_, err := a.Summarize(ctx, testTranscript(), SummarizeOptions{MeetingID: "mtg_test"})
	if err == nil {
		t.Fatal("expected an error when context is cancelled during backoff")
	}
	if code := errCode(err); code != "provider_cancelled" {
		t.Errorf("error code = %q, want provider_cancelled", code)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("backoff ignored context cancellation: took %v", elapsed)
	}
}

func TestSummarize_MissingAPIKey(t *testing.T) {
	a := &OpenRouterAdapter{APIKey: "   "}
	_, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"})
	if code := errCode(err); code != "provider_config_invalid" {
		t.Errorf("error code = %q, want provider_config_invalid", code)
	}
}

// The system prompt must be sourced from the internal/prompts package (the
// versioned, few-shot/chain-of-thought templates), not a terse inline string.
// "expert meeting analyst" is the prompts template's framing; the old inline
// prompt opened with "You are a meeting summarization assistant" and is gone.
func TestBuildSummaryMessages_UsesPromptsSystemPrompt(t *testing.T) {
	msgs := buildSummaryMessages(testTranscript())
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("expected [system,user] messages, got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "expert meeting analyst") {
		t.Errorf("system prompt not sourced from prompts package:\n%s", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "You are a meeting summarization assistant") {
		t.Errorf("stale inline system prompt still present")
	}
}

// Wiring the prompts package must NOT drop openrouter's transcript truncation:
// prompts.Build does not truncate, so a transcript past the 150-segment cap has
// to still produce a truncation marker, keeping the request under the 1MB guard.
func TestBuildSummaryMessages_TruncatesLargeTranscript(t *testing.T) {
	tr := testTranscript()
	tr.Segments = make([]artifacts.Segment, 200)
	for i := range tr.Segments {
		tr.Segments[i] = artifacts.Segment{ID: "seg", SpeakerID: "spk_0", Text: "filler text for a long meeting"}
	}
	user := buildSummaryMessages(tr)[1].Content
	if !strings.Contains(user, "... (truncated)") {
		t.Errorf("expected truncation marker for a >150-segment transcript")
	}
}

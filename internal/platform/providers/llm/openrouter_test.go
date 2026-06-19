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

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/notoerr"
	"github.com/lukasstrickler/noto/internal/platform/providers/llm/prompts"
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

// capturingDoer records the last request body so tests can assert the wire
// shape, and returns a canned structured response.
type capturingDoer struct {
	bodies []string
	status int
	body   string
}

func (c *capturingDoer) Do(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		c.bodies = append(c.bodies, string(b))
	}
	return &http.Response{
		StatusCode: c.status,
		Body:       io.NopCloser(strings.NewReader(c.body)),
		Header:     make(http.Header),
	}, nil
}

// The transcript sent to the model must attribute speakers by their fixed @S
// token (never the raw label), and the system prompt must come from the
// versioned prompts package ("expert meeting analyst").
func TestExtractMessages_TokenAttributedAndPromptSourced(t *testing.T) {
	b := prompts.NewPromptBuilder("test")
	if sys := b.SystemPrompt(prompts.SummaryTypeFull); !strings.Contains(sys, "expert meeting analyst") {
		t.Errorf("system prompt not sourced from prompts package:\n%s", sys)
	}
	user := transcriptUserMessage(b, testTranscript())
	if !strings.Contains(user, "@S1") {
		t.Errorf("user message must attribute speakers by @S token, got:\n%s", user)
	}
	if strings.Contains(user, "Alice") {
		t.Errorf("raw speaker label leaked into the model prompt:\n%s", user)
	}
}

// With privacy guards on, the request must carry structured-output +
// provider-routing fields: response_format json_schema and a provider block
// with zdr, data_collection=deny, require_parameters.
func TestSummarize_RequestShapeHonorsPrivacy(t *testing.T) {
	content := `{"short_summary":"s","decisions":[],"action_items":[],"risks":[],"open_questions":[]}`
	doer := &capturingDoer{status: 200, body: chatResponse(content)}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer, ModelID: "google/gemini-3.1-flash-preview", ZDR: true, DenyDataCollection: true, RequireParameters: true}

	if _, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"}); err != nil {
		t.Fatalf("Summarize error: %v", err)
	}
	if len(doer.bodies) == 0 {
		t.Fatal("no request captured")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(doer.bodies[0]), &payload); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if payload["model"] != "google/gemini-3.1-flash-preview" {
		t.Errorf("model = %v", payload["model"])
	}
	rf, _ := payload["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Errorf("response_format type = %v, want json_schema", rf["type"])
	}
	prov, ok := payload["provider"].(map[string]any)
	if !ok {
		t.Fatalf("missing provider routing block: %v", payload["provider"])
	}
	if prov["zdr"] != true || prov["data_collection"] != "deny" || prov["require_parameters"] != true {
		t.Errorf("provider routing = %v, want zdr/deny/require_parameters", prov)
	}
}

// With privacy relaxed, no provider block is sent and we fall back to the
// broadly-supported json_object response format.
func TestSummarize_RequestShapeRelaxed(t *testing.T) {
	content := `{"short_summary":"s","decisions":[],"action_items":[],"risks":[],"open_questions":[]}`
	doer := &capturingDoer{status: 200, body: chatResponse(content)}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer}

	if _, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"}); err != nil {
		t.Fatalf("Summarize error: %v", err)
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(doer.bodies[0]), &payload)
	if _, hasProvider := payload["provider"]; hasProvider {
		t.Errorf("expected no provider block when privacy guards are off")
	}
	rf, _ := payload["response_format"].(map[string]any)
	if rf["type"] != "json_object" {
		t.Errorf("response_format type = %v, want json_object", rf["type"])
	}
}

// A model that wraps its JSON in a markdown code fence must still parse.
func TestSummarize_StripsCodeFences(t *testing.T) {
	fenced := "```json\n{\"short_summary\":\"fenced\",\"decisions\":[],\"action_items\":[],\"risks\":[],\"open_questions\":[]}\n```"
	doer := &fakeDoer{status: 200, body: chatResponse(fenced)}
	a := &OpenRouterAdapter{APIKey: "key", HTTP: doer}

	sum, err := a.Summarize(context.Background(), testTranscript(), SummarizeOptions{MeetingID: "mtg_test"})
	if err != nil {
		t.Fatalf("Summarize error: %v", err)
	}
	if sum.ShortSummary != "fenced" {
		t.Errorf("short summary = %q, want fenced (fence not stripped)", sum.ShortSummary)
	}
}

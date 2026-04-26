package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

type mockHTTPDoer struct {
	respStatus int
	respBody   []byte
	respErr    error
}

func (m *mockHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	if m.respErr != nil {
		return nil, m.respErr
	}
	return &http.Response{
		StatusCode: m.respStatus,
		Body:       &mockReadCloser{data: m.respBody},
	}, nil
}

type mockReadCloser struct {
	data []byte
	pos  int
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.pos >= len(m.data) {
		return 0, errors.New("EOF")
	}
	n = copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockReadCloser) Close() error {
	return nil
}

func TestMistralAdapterParseResponse(t *testing.T) {
	adapter := &MistralAdapter{}

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    "meeting-123",
		Speakers: []artifacts.Speaker{
			{ID: "speaker_1", Label: "Speaker 1"},
			{ID: "speaker_2", Label: "Speaker 2"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "speaker_1", Text: "Hello world"},
		},
	}

	rawResp := `{
		"id": "mistral-response-id",
		"object": "chat.completion",
		"choices": [{
			"message": {
				"content": "{\"short_summary\":\"Test summary\",\"decisions\":[],\"action_items\":[],\"risks\":[],\"open_questions\":[]}"
			}
		}]
	}`

	summary, err := adapter.parseResponse([]byte(rawResp), transcript, "meeting-123", "mistral-large")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}

	if summary.MeetingID != "meeting-123" {
		t.Errorf("MeetingID = %s, want meeting-123", summary.MeetingID)
	}
	if summary.ShortSummary != "Test summary" {
		t.Errorf("ShortSummary = %s, want Test summary", summary.ShortSummary)
	}
	if summary.Model.Provider != "mistral" {
		t.Errorf("Model.Provider = %s, want mistral", summary.Model.Provider)
	}
	if summary.Model.ModelID != "mistral-large" {
		t.Errorf("Model.ModelID = %s, want mistral-large", summary.Model.ModelID)
	}
}

func TestMistralAdapterFallbackResponse(t *testing.T) {
	adapter := &MistralAdapter{}

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    "meeting-456",
		Segments:     []artifacts.Segment{},
	}

	rawResp := `{"error": "something went wrong"}`

	summary, err := adapter.parseResponse([]byte(rawResp), transcript, "meeting-456", "mistral-small")
	if err != nil {
		t.Fatalf("parseResponse should not error on malformed response, got: %v", err)
	}
	if summary == nil {
		t.Fatalf("parseResponse returned nil summary")
	}
}

func TestOpenRouterAdapterProviderID(t *testing.T) {
	adapter := &OpenRouterAdapter{}
	if adapter.ProviderID() != "openrouter" {
		t.Errorf("ProviderID() = %s, want openrouter", adapter.ProviderID())
	}
}

func TestMistralAdapterProviderID(t *testing.T) {
	adapter := &MistralAdapter{}
	if adapter.ProviderID() != "mistral" {
		t.Errorf("ProviderID() = %s, want mistral", adapter.ProviderID())
	}
}

func TestBuildMessages(t *testing.T) {
	transcript := artifacts.Transcript{
		Speakers: []artifacts.Speaker{
			{ID: "speaker_1", Label: "Alice"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "speaker_1", Text: "Hello"},
			{ID: "seg_000002", SpeakerID: "speaker_1", Text: "World"},
		},
	}

	messages := buildMessages(transcript)
	if len(messages) < 2 {
		t.Fatalf("buildMessages returned %d messages, want at least 2", len(messages))
	}

	foundUser := false
	for _, msg := range messages {
		if msg.Role == "user" && len(msg.Content) > 0 {
			foundUser = true
			break
		}
	}
	if !foundUser {
		t.Error("buildMessages should include a user message with transcript content")
	}
}

func TestBuildSummaryMessages(t *testing.T) {
	transcript := artifacts.Transcript{
		Speakers: []artifacts.Speaker{
			{ID: "speaker_1", Label: "Bob"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "speaker_1", Text: "Test segment"},
		},
	}

	messages := buildSummaryMessages(transcript)
	if len(messages) < 2 {
		t.Fatalf("buildSummaryMessages returned %d messages, want at least 2", len(messages))
	}
}

func TestSummarizeOptions(t *testing.T) {
	opts := SummarizeOptions{
		MeetingID:     "meeting-opt-test",
		PromptVersion: "v1",
	}

	if opts.MeetingID != "meeting-opt-test" {
		t.Errorf("MeetingID = %s, want meeting-opt-test", opts.MeetingID)
	}
	if opts.PromptVersion != "v1" {
		t.Errorf("PromptVersion = %s, want v1", opts.PromptVersion)
	}
}

func TestChatMessage(t *testing.T) {
	msg := ChatMessage{
		Role:    "system",
		Content: "You are helpful.",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	var parsed ChatMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if parsed.Role != "system" {
		t.Errorf("Role = %s, want system", parsed.Role)
	}
	if parsed.Content != "You are helpful." {
		t.Errorf("Content = %s, want You are helpful.", parsed.Content)
	}
}

func TestParseOpenRouterResponse(t *testing.T) {
	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    "meeting-or",
		Segments:     []artifacts.Segment{},
	}

	rawResp := `{
		"choices": [{
			"message": {
				"content": "{\"short_summary\":\"OR Summary\",\"decisions\":[{\"text\":\"decided\",\"speaker_ids\":[],\"evidence\":[]}],\"action_items\":[],\"risks\":[],\"open_questions\":[]}"
			}
		}]
	}`

	summary, err := parseOpenRouterResponse([]byte(rawResp), transcript, "meeting-or", "openai/gpt-4.1-mini")
	if err != nil {
		t.Fatalf("parseOpenRouterResponse returned error: %v", err)
	}

	if summary.ShortSummary != "OR Summary" {
		t.Errorf("ShortSummary = %s, want OR Summary", summary.ShortSummary)
	}
	if len(summary.Decisions) != 1 {
		t.Errorf("len(Decisions) = %d, want 1", len(summary.Decisions))
	}
}

func TestParseOpenRouterResponseMalformed(t *testing.T) {
	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    "meeting-mal",
		Segments:     []artifacts.Segment{},
	}

	rawResp := `not valid json at all`

	summary, err := parseOpenRouterResponse([]byte(rawResp), transcript, "meeting-mal", "test-model")
	if err != nil {
		t.Fatalf("parseOpenRouterResponse should handle malformed JSON, got error: %v", err)
	}
	if summary == nil {
		t.Fatalf("parseOpenRouterResponse should return fallback summary")
	}
}

func TestLLMProviderInterface(t *testing.T) {
	var _ LLMProvider = (*MistralAdapter)(nil)
	var _ LLMProvider = (*OpenRouterAdapter)(nil)
}

func TestSTTProviderInterface(t *testing.T) {
	var _ STTProvider = (*WhisperAdapter)(nil)
	var _ STTProvider = (*SpeechmaticsAdapter)(nil)
}

type fakeLLMProvider struct {
	summarizeErr error
	summary      *artifacts.Summary
}

func (f *fakeLLMProvider) ProviderID() string {
	return "fake"
}

func (f *fakeLLMProvider) Summarize(ctx context.Context, transcript artifacts.Transcript, opts SummarizeOptions) (*artifacts.Summary, error) {
	if f.summarizeErr != nil {
		return nil, f.summarizeErr
	}
	return f.summary, nil
}

func TestFakeLLMProvider(t *testing.T) {
	fake := &fakeLLMProvider{
		summary: &artifacts.Summary{
			SchemaVersion: "summary.v1",
			MeetingID:    "meeting-fake",
			ShortSummary: "Fake summary",
		},
	}

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    "meeting-fake",
		Segments:     []artifacts.Segment{},
	}

	summary, err := fake.Summarize(context.Background(), transcript, SummarizeOptions{})
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if summary.ShortSummary != "Fake summary" {
		t.Errorf("ShortSummary = %s, want Fake summary", summary.ShortSummary)
	}
}

func TestFakeLLMProviderError(t *testing.T) {
	fake := &fakeLLMProvider{
		summarizeErr: errors.New("provider error"),
	}

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    "meeting-err",
		Segments:     []artifacts.Segment{},
	}

	_, err := fake.Summarize(context.Background(), transcript, SummarizeOptions{})
	if err == nil {
		t.Error("Summarize should have returned error")
	}
}
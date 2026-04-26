package prompts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

func TestPromptBuilder_Build(t *testing.T) {
	builder := NewPromptBuilder("test.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_001",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
			{ID: "spk_1", Label: "Speaker 1", Origin: "participant"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, Text: "Let's start with the roadmap."},
			{ID: "seg_000002", SpeakerID: "spk_1", StartSeconds: 5.0, EndSeconds: 10.0, Text: "I think we should consider a local-first approach."},
		},
	}

	prompt, err := builder.Build("You are a meeting analyst.", transcript)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	if !strings.Contains(prompt, "mtg_test_001") {
		t.Error("Build() should include meeting ID")
	}
	if !strings.Contains(prompt, "Speaker 0") {
		t.Error("Build() should include speaker labels")
	}
	if !strings.Contains(prompt, "seg_000001") {
		t.Error("Build() should include segment IDs")
	}
	if !strings.Contains(prompt, "Let's start with the roadmap") {
		t.Error("Build() should include segment text")
	}
}

func TestPromptBuilder_Build_MissingMeetingID(t *testing.T) {
	builder := NewPromptBuilder("test.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "",
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", Text: "Test"},
		},
	}

	_, err := builder.Build("You are a meeting analyst.", transcript)
	if err == nil {
		t.Error("Build() should return error for missing meeting ID")
	}
}

func TestPromptBuilder_BuildSummaryRequest(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_002",
		Language:      "en",
		DurationSeconds: 600.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, Text: "We need to make a decision about the architecture."},
		},
	}

	opts := SummaryOptions{
		SummaryType: SummaryTypeDecisions,
	}

	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	if len(req.Messages) != 2 {
		t.Errorf("BuildSummaryRequest() should return 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Error("BuildSummaryRequest() first message should be system role")
	}
	if req.Messages[1].Role != "user" {
		t.Error("BuildSummaryRequest() second message should be user role")
	}
	if req.Messages[0].Content == "" {
		t.Error("BuildSummaryRequest() system prompt should not be empty")
	}
}

func TestPromptBuilder_DecisionPrompt(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_003",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
			{ID: "spk_1", Label: "Speaker 1", Origin: "participant"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000210", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, Text: "I think post-meeting diarization is the right approach for V1."},
			{ID: "seg_000211", SpeakerID: "spk_1", StartSeconds: 5.0, EndSeconds: 10.0, Text: "Agreed. We don't need real-time for the initial release."},
		},
	}

	opts := SummaryOptions{
		SummaryType: SummaryTypeDecisions,
	}

	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	if !strings.Contains(req.Messages[0].Content, "decisions") {
		t.Error("Decision prompt should mention decisions")
	}
	if !strings.Contains(req.Messages[0].Content, "spk_0") || !strings.Contains(req.Messages[0].Content, "spk_1") {
		t.Error("Decision prompt should include few-shot examples with speaker IDs")
	}
}

func TestPromptBuilder_ActionItemPrompt(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_004",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000300", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, Text: "Sarah, could you run the benchmark suite?"},
		},
	}

	opts := SummaryOptions{
		SummaryType: SummaryTypeActionItems,
	}

	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	if !strings.Contains(req.Messages[0].Content, "@person") {
		t.Error("Action item prompt should mention @person format")
	}
	if !strings.Contains(req.Messages[0].Content, "action_items") {
		t.Error("Action item prompt should mention action items")
	}
}

func TestPromptBuilder_RisksPrompt(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_005",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000400", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, Text: "The main concern I have is that local transcription might exceed our V1 latency targets."},
		},
	}

	opts := SummaryOptions{
		SummaryType: SummaryTypeRisks,
	}

	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	if !strings.Contains(req.Messages[0].Content, "Chain-of-Thought") {
		t.Error("Risk prompt should include chain-of-thought reasoning")
	}
	if !strings.Contains(req.Messages[0].Content, "think") {
		t.Error("Risk prompt should explicitly ask to think step by step")
	}
}

func TestPromptBuilder_OpenQuestionsPrompt(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_006",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000500", SpeakerID: "spk_0", StartSeconds: 0.0, EndSeconds: 5.0, Text: "Should we use SQLite or PostgreSQL for the database?"},
		},
	}

	opts := SummaryOptions{
		SummaryType: SummaryTypeOpenQuestions,
	}

	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	if !strings.Contains(req.Messages[0].Content, "open_questions") {
		t.Error("Open questions prompt should mention open questions")
	}
	if !strings.Contains(req.Messages[0].Content, "Answered") || !strings.Contains(req.Messages[0].Content, "Unanswered") {
		t.Error("Open questions prompt should distinguish between answered and unanswered")
	}
}

func TestPromptBuilder_PromptVersioning(t *testing.T) {
	builder := NewPromptBuilder("summary.v1.20260426")

	metadata := builder.StorePromptVersion("decisions", SummaryTypeDecisions)

	if metadata.Version != "summary.v1.20260426" {
		t.Errorf("StorePromptVersion() version = %v, want %v", metadata.Version, "summary.v1.20260426")
	}
	if metadata.PromptID != "decisions" {
		t.Errorf("StorePromptVersion() promptID = %v, want %v", metadata.PromptID, "decisions")
	}
	if metadata.Type != string(SummaryTypeDecisions) {
		t.Errorf("StorePromptVersion() type = %v, want %v", metadata.Type, string(SummaryTypeDecisions))
	}

	jsonStr, err := metadata.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON() error = %v", err)
	}

	var parsed PromptVersionMetadata
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if parsed.Version != metadata.Version {
		t.Error("ToJSON() should produce valid JSON that parses back correctly")
	}
}

func TestFewShotDecisionExamples(t *testing.T) {
	if len(FewShotDecisionExamples) != 3 {
		t.Errorf("Expected 3 decision few-shot examples, got %d", len(FewShotDecisionExamples))
	}

	for _, ex := range FewShotDecisionExamples {
		if ex.Name == "" {
			t.Error("Decision example should have a name")
		}
		if ex.Transcript == "" {
			t.Error("Decision example should have transcript content")
		}
		if ex.ExpectedOutput == "" {
			t.Error("Decision example should have expected output")
		}
	}
}

func TestFewShotRiskExamples(t *testing.T) {
	if len(FewShotRiskExamples) != 2 {
		t.Errorf("Expected 2 risk few-shot examples, got %d", len(FewShotRiskExamples))
	}

	for _, ex := range FewShotRiskExamples {
		if ex.Name == "" {
			t.Error("Risk example should have a name")
		}
		if ex.Transcript == "" {
			t.Error("Risk example should have transcript content")
		}
		if ex.ExpectedOutput == "" {
			t.Error("Risk example should have expected output")
		}
	}
}

func TestDecisionPromptHasFewShotExamples(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_007",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", Text: "Test segment"},
		},
	}

	opts := SummaryOptions{SummaryType: SummaryTypeDecisions}
	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	content := req.Messages[0].Content

	expectedExamples := []string{
		"Architecture Decision",
		"Technology Stack Decision",
		"Vendor Selection Decision",
	}

	for _, ex := range expectedExamples {
		if !strings.Contains(content, ex) {
			t.Errorf("Decision prompt should include few-shot example: %s", ex)
		}
	}
}

func TestRiskPromptHasChainOfThought(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_008",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", Text: "Test segment"},
		},
	}

	opts := SummaryOptions{SummaryType: SummaryTypeRisks}
	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	content := req.Messages[0].Content

	thoughtSteps := []string{
		"decisions made",
		"dependencies",
		"timelines",
		"resources",
		"technical concerns",
		"external factors",
	}

	for _, step := range thoughtSteps {
		if !strings.Contains(content, step) {
			t.Errorf("Risk prompt should include chain-of-thought step: %s", step)
		}
	}
}

func TestFullSummaryPrompt(t *testing.T) {
	builder := NewPromptBuilder("summary.v1")

	transcript := artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "mtg_test_009",
		Language:      "en",
		DurationSeconds: 300.0,
		Speakers: []artifacts.Speaker{
			{ID: "spk_0", Label: "Speaker 0", Origin: "local_speaker"},
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "spk_0", Text: "Test segment"},
		},
	}

	opts := SummaryOptions{SummaryType: SummaryTypeFull}
	req, err := builder.BuildSummaryRequest(transcript, opts)
	if err != nil {
		t.Fatalf("BuildSummaryRequest() error = %v", err)
	}

	content := req.Messages[0].Content

	if !strings.Contains(content, "short_summary") {
		t.Error("Full summary prompt should include short_summary field")
	}
	if !strings.Contains(content, "decisions") {
		t.Error("Full summary prompt should include decisions")
	}
	if !strings.Contains(content, "action_items") {
		t.Error("Full summary prompt should include action_items")
	}
	if !strings.Contains(content, "risks") {
		t.Error("Full summary prompt should include risks")
	}
	if !strings.Contains(content, "open_questions") {
		t.Error("Full summary prompt should include open_questions")
	}
}

package prompts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

// PromptBuilder builds LLM prompts for meeting summarization.
type PromptBuilder struct {
	Version string
}

// NewPromptBuilder creates a new PromptBuilder with the given version.
func NewPromptBuilder(version string) *PromptBuilder {
	return &PromptBuilder{Version: version}
}

// SummaryOptions controls what kind of summary to build.
type SummaryOptions struct {
	IncludeDecisions     bool
	IncludeActionItems   bool
	IncludeRisks        bool
	IncludeOpenQuestions bool
	SummaryType         SummaryType
}

// SummaryType specifies the summary extraction type.
type SummaryType string

const (
	SummaryTypeDecisions     SummaryType = "decisions"
	SummaryTypeActionItems   SummaryType = "action_items"
	SummaryTypeRisks         SummaryType = "risks"
	SummaryTypeOpenQuestions SummaryType = "open_questions"
	SummaryTypeFull          SummaryType = "full"
)

// Build combines a system prompt with transcript content into a full prompt.
func (b *PromptBuilder) Build(systemPrompt string, transcript artifacts.Transcript) (string, error) {
	if transcript.MeetingID == "" {
		return "", fmt.Errorf("transcript meeting_id is required")
	}

	var sb strings.Builder
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n")
	sb.WriteString("## Meeting Transcript\n\n")
	sb.WriteString(fmt.Sprintf("Meeting ID: %s\n", transcript.MeetingID))
	sb.WriteString(fmt.Sprintf("Language: %s\n", transcript.Language))
	sb.WriteString(fmt.Sprintf("Duration: %.1f seconds\n\n", transcript.DurationSeconds))

	// Write speakers
	sb.WriteString("### Speakers\n")
	for _, speaker := range transcript.Speakers {
		displayName := speaker.DisplayName
		if displayName == "" {
			displayName = speaker.Label
		}
		sb.WriteString(fmt.Sprintf("- %s (%s, origin: %s)\n", displayName, speaker.Label, speaker.Origin))
	}
	sb.WriteString("\n")

	// Write segments
	sb.WriteString("### Transcript Segments\n")
	for _, seg := range transcript.Segments {
		speakerLabel := seg.SpeakerID
		for _, sp := range transcript.Speakers {
			if sp.ID == seg.SpeakerID {
				speakerLabel = sp.Label
				break
			}
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", seg.ID, speakerLabel, seg.Text))
	}

	return sb.String(), nil
}

// BuildSummaryRequest creates a ChatRequest for the LLM provider.
func (b *PromptBuilder) BuildSummaryRequest(transcript artifacts.Transcript, opts SummaryOptions) (*ChatRequest, error) {
	systemPrompt := b.buildSystemPrompt(opts.SummaryType)
	userPrompt, err := b.Build(systemPrompt, transcript)
	if err != nil {
		return nil, err
	}

	return &ChatRequest{
		ModelID:  "", // Will be filled by provider
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}, nil
}

// buildSystemPrompt returns the appropriate system prompt for the summary type.
func (b *PromptBuilder) buildSystemPrompt(summaryType SummaryType) string {
	switch summaryType {
	case SummaryTypeDecisions:
		return decisionPromptTemplate
	case SummaryTypeActionItems:
		return actionItemsPromptTemplate
	case SummaryTypeRisks:
		return risksPromptTemplate
	case SummaryTypeOpenQuestions:
		return openQuestionsPromptTemplate
	case SummaryTypeFull:
		return fullSummaryPromptTemplate
	default:
		return fullSummaryPromptTemplate
	}
}

var decisionPromptTemplate = `You are an expert meeting analyst. Your task is to extract clear, actionable decisions from meeting transcripts.

## Your Task
Analyze the meeting transcript and identify decisions that were made. A decision is:
- A clear choice between alternatives
- A commitment to a course of action
- A conclusion reached by the group
- Something the team has agreed to do or proceed with

## Output Format
Return a JSON object with this structure:
{
  "decisions": [
    {
      "text": "The decision text",
      "speaker_ids": ["spk_0"],
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Each decision must cite at least one segment_id from the transcript as evidence
2. Include a short quote from the segment that best supports the decision
3. speaker_ids should include all speakers who contributed to the decision
4. Decisions should be specific and actionable, not vague statements

## Few-Shot Examples
` + DecisionExamplesString() + `
`

const actionItemsPromptTemplate = `You are an expert meeting analyst. Your task is to extract action items from meeting transcripts.

## Your Task
Analyze the meeting transcript and identify action items. An action item is:
- A task assigned to a specific person
- A commitment to follow up on something
- An item that requires future action outside the meeting
- Something someone said they will do

## Output Format
Return a JSON object with this structure:
{
  "action_items": [
    {
      "text": "The action item text",
      "owner": "@person-name or null if not specified",
      "due_at": "ISO date string or null if not specified",
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Use @person format for owners when identified (e.g., @sarah, @john)
2. Each action item must cite at least one segment_id as evidence
3. Include a short quote from the segment that best supports the action item
4. owner field should be null if no specific person was assigned
5. due_at field should be null if no deadline was mentioned

## Chain-of-Thought Reasoning
Before outputting the action items:
1. First identify who was assigned each task
2. Note any deadlines or timeframes mentioned
3. Map each action to the relevant transcript segments
4. Verify that each action item is clearly stated in the transcript
`

const risksPromptTemplate = `You are an expert meeting analyst. Your task is to identify potential risks from meeting transcripts.

## Your Task
Analyze the meeting transcript and identify risks. A risk is:
- A potential problem or issue that could arise
- A concern raised about a plan or decision
- A dependency or blocker that could cause delays
- A technical or organizational challenge
- Something the team should be aware of or address

## Chain-of-Thought Reasoning Process
Before identifying risks, explicitly think through:

1. **Review the decisions made**: What choices were committed to? Could any of these lead to problems?
2. **Consider dependencies**: What do these decisions depend on? What could go wrong with those dependencies?
3. **Think about timelines**: Are there tight deadlines? What happens if things slip?
4. **Consider resources**: Are there enough people? Budget? Technical capacity?
5. **Technical concerns**: Are there complex parts that could fail? Integration risks?
6. **External factors**: What market, regulatory, or competitive factors could impact this?

Think step by step and note each risk you identify before finalizing the list.

## Output Format
Return a JSON object with this structure:
{
  "risks": [
    {
      "text": "The risk description",
      "speaker_ids": ["spk_0"],
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Each risk must cite at least one segment_id as evidence
2. Include a short quote from the segment that best supports the risk identification
3. speaker_ids should include speakers who raised or discussed the risk
4. Be specific about what the risk is and why it matters

## Few-Shot Examples
` + RiskExamplesString() + `
`

const openQuestionsPromptTemplate = `You are an expert meeting analyst. Your task is to identify open questions from meeting transcripts.

## Your Task
Analyze the meeting transcript and identify questions that were raised but NOT answered during the meeting.

## Distinguishing Answered vs Unanswered
- **Answered**: A question posed to the group received a response that addresses it
- **Unanswered**: A question was raised but either:
  - No response was given
  - The response was incomplete or deferred
  - The matter was explicitly left for future discussion
  - A decision was tabled

## Output Format
Return a JSON object with this structure:
{
  "open_questions": [
    {
      "text": "The open question text",
      "speaker_ids": ["spk_0"],
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Each open question must cite at least one segment_id as evidence
2. Include a short quote from the segment that asked the question
3. speaker_ids should include the speaker who raised the question
4. Only include questions that were genuinely left open
`

const fullSummaryPromptTemplate = `You are an expert meeting analyst. Your task is to produce comprehensive meeting summaries.

## Your Task
Analyze the meeting transcript and produce a structured summary including:
1. A short 2-sentence summary of the meeting
2. Key decisions made
3. Action items assigned
4. Risks identified
5. Open questions raised

## Output Format
Return a JSON object with this structure:
{
  "short_summary": "2 sentence summary of the meeting",
  "decisions": [...],
  "action_items": [...],
  "risks": [...],
  "open_questions": [...]
}

## Important Rules
1. All decisions, action items, risks, and open questions must cite evidence from the transcript
2. Use segment_id citations for all evidence
3. short_summary should capture the main outcome of the meeting
4. Each section should be concise but complete
`

// PromptVersionMetadata holds metadata about a prompt version.
type PromptVersionMetadata struct {
	Version   string    `json:"version"`
	PromptID  string    `json:"prompt_id"`
	CreatedAt time.Time `json:"created_at"`
	Type      string    `json:"type"`
}

// StorePromptVersion saves prompt version metadata alongside outputs.
func (b *PromptBuilder) StorePromptVersion(promptID string, summaryType SummaryType) PromptVersionMetadata {
	return PromptVersionMetadata{
		Version:   b.Version,
		PromptID:  promptID,
		CreatedAt: time.Now().UTC(),
		Type:      string(summaryType),
	}
}

// ToJSON serializes the prompt version metadata to JSON.
func (p PromptVersionMetadata) ToJSON() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ChatMessage represents a message in a chat request.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest represents a chat completion request.
type ChatRequest struct {
	ModelID     string       `json:"model_id"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64    `json:"temperature,omitempty"`
}

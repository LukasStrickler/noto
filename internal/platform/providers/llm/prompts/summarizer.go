package prompts

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
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
	IncludeRisks         bool
	IncludeOpenQuestions bool
	SummaryType          SummaryType
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

	// This transcript is anonymized: real names live locally and are never sent
	// to the model. Each speaker has a FIXED token (@S1, @S2, …) assigned by
	// order, and the model must refer to speakers ONLY by that token. A delimited
	// token is detectable verbatim, so the UI can reliably swap it back for the
	// real person + identity color at render time (internal/ui/tui renderPeople)
	// — unlike a bare label, which a model paraphrases into "the first speaker"
	// or "speakers A" and we then can't match.
	tokenByID := make(map[string]string, len(transcript.Speakers))
	for i, sp := range transcript.Speakers {
		tokenByID[sp.ID] = fmt.Sprintf("@S%d", i+1)
	}

	sb.WriteString("### Speaker references — IMPORTANT\n")
	sb.WriteString("Refer to every speaker ONLY by their fixed token below (e.g. \"@S1 owns the rollout\").\n")
	sb.WriteString("Use the token verbatim in EVERY field — short_summary, decisions, action_items,\n")
	sb.WriteString("risks, open_questions, owner, and speaker_ids. Never invent or guess a name,\n")
	sb.WriteString("never paraphrase a token (not \"the first speaker\", not \"Speaker One\"), and never\n")
	sb.WriteString("change its case or spacing. If you don't know who a speaker is, just use the token.\n\n")

	sb.WriteString("### Speakers\n")
	for i := range transcript.Speakers {
		sb.WriteString(fmt.Sprintf("- @S%d\n", i+1))
	}
	sb.WriteString("\n")

	// Write segments, attributed by the speaker's token.
	sb.WriteString("### Transcript Segments\n")
	for _, seg := range transcript.Segments {
		token := tokenByID[seg.SpeakerID]
		if token == "" {
			token = seg.SpeakerID
		}
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", seg.ID, token, seg.Text))
	}

	return sb.String(), nil
}

// BuildSummaryRequest creates a ChatRequest for the LLM provider.
func (b *PromptBuilder) BuildSummaryRequest(transcript artifacts.Transcript, opts SummaryOptions) (*ChatRequest, error) {
	systemPrompt := b.SystemPrompt(opts.SummaryType)
	userPrompt, err := b.Build(systemPrompt, transcript)
	if err != nil {
		return nil, err
	}

	return &ChatRequest{
		ModelID: "", // Will be filled by provider
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}, nil
}

// SystemPrompt returns the appropriate system prompt for the summary type.
// Exported so LLM adapters can use the versioned, few-shot/chain-of-thought
// templates as the single source of truth for the system message.
func (b *PromptBuilder) SystemPrompt(summaryType SummaryType) string {
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

// RefineSystemPrompt returns the system prompt for the second (gap-analysis +
// correction) pass.
func (b *PromptBuilder) RefineSystemPrompt() string {
	return refineSummaryPromptTemplate
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
      "text": "The decision text, using @S1-style speaker tokens for any people",
      "speaker_ids": ["@S1"],
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Each decision must cite at least one segment_id from the transcript as evidence
2. Include a short quote from the segment that best supports the decision
3. speaker_ids should list the speaker tokens (e.g. @S1) of everyone who contributed
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
      "text": "The action item text, using @S1-style speaker tokens for any people",
      "owner": "the assignee's speaker token (e.g. @S1) or null if not specified",
      "due_at": "ISO date string or null if not specified",
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Set owner to the assignee's speaker token (e.g. @S1) when a task is assigned to someone in the meeting; null otherwise
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

var risksPromptTemplate = `You are an expert meeting analyst. Your task is to identify potential risks from meeting transcripts.

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
      "text": "The risk description, using @S1-style speaker tokens for any people",
      "speaker_ids": ["@S1"],
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Each risk must cite at least one segment_id as evidence
2. Include a short quote from the segment that best supports the risk identification
3. speaker_ids should list the speaker tokens (e.g. @S1) of those who raised or discussed the risk
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
      "text": "The open question text, using @S1-style speaker tokens for any people",
      "speaker_ids": ["@S1"],
      "evidence": [{"segment_id": "seg_000001", "quote": "relevant quote from transcript"}]
    }
  ]
}

## Important Rules
1. Each open question must cite at least one segment_id as evidence
2. Include a short quote from the segment that asked the question
3. speaker_ids should list the speaker token (e.g. @S1) of whoever raised the question
4. Only include questions that were genuinely left open
`

var fullSummaryPromptTemplate = `You are an expert meeting analyst. Produce a structured, evidence-grounded summary of a meeting transcript.

## Output — STRICT
Return ONLY a single JSON object. No markdown, no code fences, no text before or after it. Shape:
{
  "short_summary": "2-3 sentence summary of the meeting's purpose and outcome",
  "decisions":      [{"text": "...", "speaker_ids": ["@S1"], "evidence": [{"segment_id": "seg_000001", "quote": "verbatim quote"}]}],
  "action_items":   [{"text": "...", "owner": "@S1 or null", "due_at": "ISO date or null", "evidence": [{"segment_id": "seg_000001", "quote": "verbatim quote"}]}],
  "risks":          [{"text": "...", "speaker_ids": ["@S2"], "evidence": [{"segment_id": "seg_000001", "quote": "verbatim quote"}]}],
  "open_questions": [{"text": "...", "speaker_ids": ["@S1"], "evidence": [{"segment_id": "seg_000001", "quote": "verbatim quote"}]}]
}
Every array may be empty, but never omit a key.

## Speaker references — IMPORTANT
Refer to every person ONLY by their fixed token (@S1, @S2, …) exactly as written in the transcript. Never invent or guess a real name, never paraphrase a token (not "the first speaker", not "Speaker One"), never change its case or spacing. Use the token verbatim in text, owner, and speaker_ids.

## Grounding rules — IMPORTANT
1. Every item MUST cite at least one segment_id that exists in the transcript.
2. Each "quote" MUST be copied VERBATIM from that segment — never paraphrase or fabricate a quote. If you cannot find a supporting verbatim quote, do NOT emit the item.
3. Extract only what the transcript supports. Do not invent decisions, owners, or deadlines that were not stated.
4. Definitions: a decision = a committed choice or conclusion; an action item = a task someone will do after the meeting; a risk = a stated concern, blocker, or dependency; an open question = a question raised but left unanswered.

## Reasoning
Think step by step internally — first the decisions, then who owns each action and any deadlines, then risks, then unanswered questions — mapping each to its supporting segment. Output ONLY the final JSON, never your reasoning.

## Few-Shot Examples
` + DecisionExamplesString() + RiskExamplesString() + `
`

// refineSummaryPromptTemplate drives the second pass: a reviewer that does gap
// analysis (adds supported items the first pass missed), repairs ungrounded
// quotes, and prunes hallucinated/duplicate items. This is the single biggest
// quality lever beyond the first extraction, and it is cheap.
const refineSummaryPromptTemplate = `You are a meticulous meeting-analysis reviewer. You are given a transcript and a DRAFT summary from a first pass. Return an improved summary in the SAME strict JSON shape.

Do three things:
1. GAP ANALYSIS — find decisions, action items, risks, or open questions the draft MISSED but the transcript clearly supports, and add them with verbatim-quote evidence.
2. CORRECTION — some draft items are flagged UNVERIFIED because their quote was not found verbatim in the cited segment. For each, either replace it with the correct verbatim quote + segment_id, or remove the item if nothing in the transcript supports it.
3. PRUNING — remove duplicates and any item lacking a verbatim supporting quote.

Keep every rule from the first pass: @S1 tokens only, verbatim quotes, every JSON key present, and output ONLY the JSON object — no code fences, no prose.`

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
	ModelID     string        `json:"model_id"`
	Messages    []ChatMessage `json:"messages"`
	Temperature *float64      `json:"temperature,omitempty"`
}

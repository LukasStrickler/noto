package artifacts

import "github.com/lukasstrickler/noto/internal/notoerr"

type Summary struct {
	SchemaVersion string        `json:"schema_version"`
	MeetingID     string        `json:"meeting_id"`
	ShortSummary  string        `json:"short_summary"`
	Decisions     []SummaryItem `json:"decisions"`
	ActionItems   []ActionItem  `json:"action_items"`
	OpenQuestions []SummaryItem `json:"open_questions"`
	Risks         []SummaryItem `json:"risks"`
	Model         SummaryModel  `json:"model"`
}

type SummaryItem struct {
	Text       string     `json:"text"`
	SpeakerIDs []string   `json:"speaker_ids,omitempty"`
	Evidence   []Evidence `json:"evidence"`
}

type ActionItem struct {
	Text     string     `json:"text"`
	Owner    string     `json:"owner"`
	DueAt    string     `json:"due_at"`
	Evidence []Evidence `json:"evidence"`
}

type Evidence struct {
	SegmentID string `json:"segment_id"`
	Quote     string `json:"quote"`
}

type SummaryModel struct {
	Provider      string `json:"provider"`
	ModelID       string `json:"model_id"`
	PromptVersion string `json:"prompt_version"`
}

func ValidateSummary(summary Summary, transcript Transcript) error {
	if summary.SchemaVersion != "summary.v1" {
		return notoerr.New("schema_validation_failed", "Summary schema_version must be summary.v1.", map[string]any{"schema_version": summary.SchemaVersion})
	}
	if summary.MeetingID == "" || summary.MeetingID != transcript.MeetingID {
		return notoerr.New("schema_validation_failed", "Summary meeting_id must match transcript meeting_id.", map[string]any{"summary_meeting_id": summary.MeetingID, "transcript_meeting_id": transcript.MeetingID})
	}
	if summary.Model.Provider != "openrouter" && summary.Model.Provider != "fake-llm" {
		return notoerr.New("invalid_provider_route", "Summary provider must be OpenRouter for real LLM work.", map[string]any{"provider": summary.Model.Provider})
	}
	segments := map[string]bool{}
	for _, segment := range transcript.Segments {
		segments[segment.ID] = true
	}
	for _, evidence := range allEvidence(summary) {
		if evidence.SegmentID == "" || !segments[evidence.SegmentID] {
			return notoerr.New("schema_validation_failed", "Summary evidence references an unknown transcript segment.", map[string]any{"segment_id": evidence.SegmentID})
		}
	}
	return nil
}

func allEvidence(summary Summary) []Evidence {
	var out []Evidence
	for _, item := range summary.Decisions {
		out = append(out, item.Evidence...)
	}
	for _, item := range summary.ActionItems {
		out = append(out, item.Evidence...)
	}
	for _, item := range summary.OpenQuestions {
		out = append(out, item.Evidence...)
	}
	for _, item := range summary.Risks {
		out = append(out, item.Evidence...)
	}
	return out
}

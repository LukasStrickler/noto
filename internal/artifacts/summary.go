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

func (s *Summary) Kind() ArtifactKind {
	return KindSummary
}

func (s *Summary) Validate() *notoerr.Error {
	if s.SchemaVersion != "summary.v1" {
		return notoerr.New(ErrCodeValidationFailed, "schema_version must be summary.v1", map[string]any{"schema_version": s.SchemaVersion})
	}
	if s.MeetingID == "" {
		return NewMissingFieldError("meeting_id")
	}
	return nil
}

func ValidateSummary(summary Summary, transcript Transcript) *notoerr.Error {
	if summary.SchemaVersion != "summary.v1" {
		return notoerr.New(ErrCodeValidationFailed, "schema_version must be summary.v1", map[string]any{"schema_version": summary.SchemaVersion})
	}
	if summary.MeetingID == "" {
		return NewMissingFieldError("meeting_id")
	}
	if transcript.MeetingID != "" && summary.MeetingID != transcript.MeetingID {
		return notoerr.New(ErrCodeValidationFailed, "meeting_id must match transcript meeting_id", map[string]any{"summary_meeting_id": summary.MeetingID, "transcript_meeting_id": transcript.MeetingID})
	}
	segments := map[string]bool{}
	for _, segment := range transcript.Segments {
		segments[segment.ID] = true
	}
	for _, evidence := range allEvidence(summary) {
		if evidence.SegmentID == "" {
			return NewMissingFieldError("evidence.segment_id")
		}
		if !segments[evidence.SegmentID] {
			return notoerr.New(ErrCodeValidationFailed, "evidence references unknown transcript segment", map[string]any{"segment_id": evidence.SegmentID})
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

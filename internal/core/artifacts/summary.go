package artifacts

import (
	"strings"
	"unicode"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

type Summary struct {
	SchemaVersion string           `json:"schema_version"`
	MeetingID     string           `json:"meeting_id"`
	ShortSummary  string           `json:"short_summary"`
	Decisions     []SummaryItem    `json:"decisions"`
	ActionItems   []ActionItem     `json:"action_items"`
	OpenQuestions []SummaryItem    `json:"open_questions"`
	Risks         []SummaryItem    `json:"risks"`
	Model         SummaryModel     `json:"model"`
	Coverage      *SummaryCoverage `json:"coverage,omitempty"`
}

type SummaryItem struct {
	Text       string     `json:"text"`
	SpeakerIDs []string   `json:"speaker_ids,omitempty"`
	Evidence   []Evidence `json:"evidence"`
	// Confidence is the share of this item's evidence quotes that were found
	// verbatim in their cited transcript segment, weighted by segment
	// confidence (0..1). Set by VerifyAndScore, not the model. A 0 means the
	// item is ungrounded — its quotes don't appear in the transcript.
	Confidence float64 `json:"confidence,omitempty"`
}

type ActionItem struct {
	Text       string     `json:"text"`
	Owner      string     `json:"owner"`
	DueAt      string     `json:"due_at"`
	Evidence   []Evidence `json:"evidence"`
	Confidence float64    `json:"confidence,omitempty"`
}

// SummaryCoverage is a "correctness insight": how much of the generated
// summary is actually grounded in the transcript. Computed by VerifyAndScore.
type SummaryCoverage struct {
	ItemsTotal     int     `json:"items_total"`
	ItemsGrounded  int     `json:"items_grounded"`
	GroundingScore float64 `json:"grounding_score"`
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

func (s *Summary) Version() string {
	return s.SchemaVersion
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

// VerifyAndScore grounds a summary against its transcript: for every evidence
// quote it checks whether the quote actually appears in the cited segment, sets
// each item's Confidence accordingly, and records an overall coverage score.
// This is the deterministic "correctness" pass — it catches hallucinated quotes
// that ValidateSummary (which only checks the segment_id exists) cannot. Pure
// and side-effect free apart from mutating the passed summary.
func VerifyAndScore(summary *Summary, transcript Transcript) {
	if summary == nil {
		return
	}
	segText := make(map[string]string, len(transcript.Segments))
	segConf := make(map[string]float64, len(transcript.Segments))
	for _, seg := range transcript.Segments {
		segText[seg.ID] = normalizeForMatch(seg.Text)
		if seg.Confidence != nil {
			segConf[seg.ID] = *seg.Confidence
		} else {
			segConf[seg.ID] = 1
		}
	}

	score := func(ev []Evidence) float64 {
		if len(ev) == 0 {
			return 0
		}
		matched := 0
		var confSum float64
		for _, e := range ev {
			seg, ok := segText[e.SegmentID]
			q := normalizeForMatch(e.Quote)
			if ok && q != "" && strings.Contains(seg, q) {
				matched++
				confSum += segConf[e.SegmentID]
			}
		}
		if matched == 0 {
			return 0
		}
		frac := float64(matched) / float64(len(ev))
		avgConf := confSum / float64(matched)
		return clamp01(frac * avgConf)
	}

	total, grounded := 0, 0
	tally := func(c float64) {
		total++
		if c > 0 {
			grounded++
		}
	}
	for i := range summary.Decisions {
		summary.Decisions[i].Confidence = score(summary.Decisions[i].Evidence)
		tally(summary.Decisions[i].Confidence)
	}
	for i := range summary.ActionItems {
		summary.ActionItems[i].Confidence = score(summary.ActionItems[i].Evidence)
		tally(summary.ActionItems[i].Confidence)
	}
	for i := range summary.Risks {
		summary.Risks[i].Confidence = score(summary.Risks[i].Evidence)
		tally(summary.Risks[i].Confidence)
	}
	for i := range summary.OpenQuestions {
		summary.OpenQuestions[i].Confidence = score(summary.OpenQuestions[i].Evidence)
		tally(summary.OpenQuestions[i].Confidence)
	}

	gs := 1.0
	if total > 0 {
		gs = float64(grounded) / float64(total)
	}
	summary.Coverage = &SummaryCoverage{ItemsTotal: total, ItemsGrounded: grounded, GroundingScore: gs}
}

// normalizeForMatch lowercases text and collapses every run of non-alphanumeric
// characters to a single space, so quote matching tolerates punctuation,
// capitalization, and whitespace differences between a model's quote and the
// transcript ("Ship v1!" matches "we will ship v1").
func normalizeForMatch(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := true // trim leading space
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
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

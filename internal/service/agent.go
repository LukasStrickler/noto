package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/repo"
)

// AgentListMeetings returns a cursor-paginated meeting list for agents.
// Each entry includes a pre-populated short summary so agents can decide
// which meetings to fetch in full without extra round-trips.
func (s *Service) AgentListMeetings(ctx context.Context, opts notoapi.AgentListOpts) (notoapi.AgentListResult, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	stored, err := s.repo.ListMeetings(ctx)
	if err != nil {
		return notoapi.AgentListResult{}, err
	}

	var filtered []notoapi.AgentMeetingSummary
	for _, sm := range stored {
		if !opts.Before.IsZero() && !sm.CreatedAt.Before(opts.Before) {
			continue
		}
		if !opts.After.IsZero() && !sm.CreatedAt.After(opts.After) {
			continue
		}
		filtered = append(filtered, agentSummaryFromStored(sm))
	}

	total := len(filtered)
	var nextCursor string
	if limit < len(filtered) {
		filtered = filtered[:limit]
		nextCursor = filtered[len(filtered)-1].CreatedAt.UTC().Format(time.RFC3339Nano)
	}

	return notoapi.AgentListResult{
		Meetings:   filtered,
		Total:      total,
		NextCursor: nextCursor,
	}, nil
}

// AgentGetMeeting returns the full meeting context for a single meeting:
// metadata, structured summary with source citations, and the full
// transcript with speaker display names pre-resolved. One call gives
// everything an agent needs to read and cite a meeting.
func (s *Service) AgentGetMeeting(ctx context.Context, id string) (notoapi.AgentMeetingContext, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.AgentMeetingContext{},
			notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}

	sm, err := s.repo.GetMeeting(ctx, mid)
	if err != nil {
		return notoapi.AgentMeetingContext{}, mapRepoErr(err, id)
	}

	out := notoapi.AgentMeetingContext{
		ID:              sm.ID.String(),
		Title:           sm.Title,
		CreatedAt:       sm.CreatedAt,
		DurationSeconds: sm.DurationSeconds,
		Status:          statusFromStored(sm),
	}

	// Summary — silently omitted when not yet available.
	if _, raw, err := s.repo.LoadSummary(ctx, mid); err == nil && raw != nil {
		out.Summary = buildAgentSummary(raw)
	}

	// Transcript — silently omitted when not yet available.
	if t, err := s.repo.LoadTranscript(ctx, mid); err == nil && t != nil {
		out.Transcript = buildAgentTranscript(t)
	}

	return out, nil
}

func agentSummaryFromStored(sm *repo.StoredMeeting) notoapi.AgentMeetingSummary {
	return notoapi.AgentMeetingSummary{
		ID:              sm.ID.String(),
		Title:           sm.Title,
		CreatedAt:       sm.CreatedAt,
		DurationSeconds: sm.DurationSeconds,
		Status:          statusFromStored(sm),
		Speakers:        sm.SpeakerCount,
		ShortSummary:    sm.ShortSummary,
		DecisionCount:   sm.DecisionCount,
		ActionCount:     sm.ActionCount,
		RiskCount:       sm.RiskCount,
		QuestionCount:   sm.QuestionCount,
	}
}

func buildAgentSummary(raw *artifacts.Summary) *notoapi.AgentSummaryBlock {
	block := &notoapi.AgentSummaryBlock{ShortSummary: raw.ShortSummary}
	for _, d := range raw.Decisions {
		block.Decisions = append(block.Decisions, notoapi.AgentCitation{
			Text:        d.Text,
			SpeakerIDs:  d.SpeakerIDs,
			SegmentRefs: refsFromEvidence(d.Evidence),
		})
	}
	for _, a := range raw.ActionItems {
		c := notoapi.AgentCitation{
			Text:        a.Text,
			SegmentRefs: refsFromEvidence(a.Evidence),
		}
		if a.Owner != "" {
			c.SpeakerIDs = []string{a.Owner}
		}
		block.ActionItems = append(block.ActionItems, c)
	}
	for _, r := range raw.Risks {
		block.Risks = append(block.Risks, notoapi.AgentCitation{
			Text:        r.Text,
			SegmentRefs: refsFromEvidence(r.Evidence),
		})
	}
	for _, q := range raw.OpenQuestions {
		block.OpenQuestions = append(block.OpenQuestions, notoapi.AgentCitation{
			Text:        q.Text,
			SegmentRefs: refsFromEvidence(q.Evidence),
		})
	}
	return block
}

func buildAgentTranscript(t *artifacts.Transcript) *notoapi.AgentTranscriptBlock {
	nameIdx := make(map[string]string, len(t.Speakers))
	var speakers []notoapi.Speaker
	for _, sp := range t.Speakers {
		name := sp.DisplayName
		if name == "" {
			name = sp.Label
		}
		nameIdx[sp.ID] = name
		speakers = append(speakers, notoapi.Speaker{
			ID:          sp.ID,
			DisplayName: sp.DisplayName,
			Role:        sp.Origin,
		})
	}
	var segs []notoapi.AgentSegment
	for _, seg := range t.Segments {
		name := nameIdx[seg.SpeakerID]
		if name == "" {
			name = seg.SpeakerID
		}
		segs = append(segs, notoapi.AgentSegment{
			ID:           seg.ID,
			Speaker:      name,
			TimestampSec: seg.StartSeconds,
			EndSec:       seg.EndSeconds,
			Text:         seg.Text,
		})
	}
	return &notoapi.AgentTranscriptBlock{Speakers: speakers, Segments: segs}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// ListMeetings returns a paged list of meetings, most-recently-created first.
func (s *Service) ListMeetings(ctx context.Context, opts notoapi.ListMeetingsOpts) (notoapi.ListMeetingsResult, error) {
	stored, err := s.repo.ListMeetings(ctx)
	if err != nil {
		return notoapi.ListMeetingsResult{}, err
	}

	q := strings.ToLower(strings.TrimSpace(opts.Query))
	out := make([]notoapi.Meeting, 0, len(stored))
	for _, sm := range stored {
		if q != "" && !strings.Contains(strings.ToLower(sm.Title), q) {
			continue
		}
		out = append(out, meetingFromStored(sm))
	}
	total := len(out)
	if opts.Limit > 0 && opts.Limit < len(out) {
		out = out[:opts.Limit]
	}
	s.attachIdentity(ctx, out)
	return notoapi.ListMeetingsResult{Meetings: out, Total: total}, nil
}

// attachIdentity fills each meeting's speaker-identity rollup from the mapping
// store with one grouped query. Best-effort: a read error (or no mappings) just
// leaves Identity nil rather than failing the whole listing — it's decorative.
func (s *Service) attachIdentity(ctx context.Context, meetings []notoapi.Meeting) {
	if len(meetings) == 0 || s.meetingMappings == nil {
		return
	}
	counts, err := s.meetingMappings.StatusCountsByMeeting(ctx)
	if err != nil || len(counts) == 0 {
		return
	}
	for i := range meetings {
		if c, ok := counts[meetings[i].ID]; ok {
			meetings[i].Identity = identitySummary(c)
		}
	}
}

// identitySummary folds per-status mapping counts into the dashboard's four
// buckets. Returns nil when there's nothing to summarize.
func identitySummary(byStatus map[string]int) *notoapi.SpeakerIdentitySummary {
	sum := &notoapi.SpeakerIdentitySummary{}
	any := false
	for status, n := range byStatus {
		if n == 0 {
			continue
		}
		any = true
		switch status {
		case "auto", "manual":
			sum.Resolved += n
		case "pending":
			sum.Likely += n
		case "new":
			sum.New += n
		default: // "unmatched", ""
			sum.Unset += n
		}
	}
	if !any {
		return nil
	}
	return sum
}

// GetMeeting returns a single meeting.
func (s *Service) GetMeeting(ctx context.Context, id string) (notoapi.Meeting, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.Meeting{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", map[string]any{"id": id})
	}
	sm, err := s.repo.GetMeeting(ctx, mid)
	if err != nil {
		return notoapi.Meeting{}, mapRepoErr(err, id)
	}
	m := meetingFromStored(sm)
	return m, nil
}

// GetTranscript returns the normalized transcript for the meeting.
func (s *Service) GetTranscript(ctx context.Context, id string) (notoapi.Transcript, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.Transcript{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	t, err := s.repo.LoadTranscript(ctx, mid)
	if err != nil {
		return notoapi.Transcript{}, mapRepoErr(err, id)
	}
	// Resolve speaker names through the identity mappings (not just the transcript's
	// own DisplayName), the same shared join the agent handoff and search index use —
	// so an AUTO-identified speaker reads as the person, not "spk_0", consistently
	// across every read site. The raw anonymous Label stays available in its field.
	nameByID := s.meetingSpeakerNames(ctx, id, t)
	out := notoapi.Transcript{MeetingID: id}
	for _, seg := range t.Segments {
		name := nameByID[seg.SpeakerID]
		if name == "" {
			name = seg.SpeakerID
		}
		out.Segments = append(out.Segments, notoapi.TranscriptSegment{
			ID:         seg.ID,
			SpeakerID:  seg.SpeakerID,
			Speaker:    name,
			Role:       seg.SourceRole,
			StartSec:   seg.StartSeconds,
			EndSec:     seg.EndSeconds,
			Text:       seg.Text,
			Confidence: derefConf(seg.Confidence),
		})
	}
	for _, sp := range t.Speakers {
		out.Speakers = append(out.Speakers, notoapi.Speaker{
			ID:          sp.ID,
			DisplayName: nameByID[sp.ID],
			Role:        sp.Origin,
			Label:       sp.Label,
		})
	}
	return out, nil
}

func derefConf(c *float64) float64 {
	if c == nil {
		return 0
	}
	return *c
}

// GetSummary returns the structured summary plus its rendered markdown.
func (s *Service) GetSummary(ctx context.Context, id string) (notoapi.Summary, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.Summary{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	md, raw, err := s.repo.LoadSummary(ctx, mid)
	if err != nil {
		return notoapi.Summary{}, mapRepoErr(err, id)
	}
	out := notoapi.Summary{MeetingID: id, Markdown: md}
	if raw != nil {
		out.ShortSummary = raw.ShortSummary
		for _, d := range raw.Decisions {
			out.Decisions = append(out.Decisions, notoapi.SummaryItem{
				Text:        d.Text,
				SpeakerIDs:  d.SpeakerIDs,
				SegmentRefs: refsFromEvidence(d.Evidence),
			})
		}
		for _, a := range raw.ActionItems {
			out.ActionItems = append(out.ActionItems, notoapi.ActionItem{
				Text:        a.Text,
				Owner:       a.Owner,
				SegmentRefs: refsFromEvidence(a.Evidence),
			})
		}
		for _, r := range raw.Risks {
			out.Risks = append(out.Risks, notoapi.SummaryItem{
				Text:        r.Text,
				SegmentRefs: refsFromEvidence(r.Evidence),
			})
		}
		for _, oq := range raw.OpenQuestions {
			out.OpenQuestions = append(out.OpenQuestions, notoapi.SummaryItem{
				Text:        oq.Text,
				SegmentRefs: refsFromEvidence(oq.Evidence),
			})
		}
	}
	if out.ShortSummary == "" && out.Markdown != "" {
		out.ShortSummary = firstSummaryLine(out.Markdown)
	}
	return out, nil
}

// firstSummaryLine returns the first non-blank, non-heading line of rendered
// summary markdown — the summary prose. renderSummaryMD opens with a "# {title}"
// heading (and "## Section" headings follow), so taking the first non-blank line
// would surface the meeting TITLE as the summary; skip lines that start with '#'.
func firstSummaryLine(md string) string {
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		return t
	}
	return ""
}

// GetMeetingFiles returns the on-disk paths of artifacts for the meeting.
func (s *Service) GetMeetingFiles(_ context.Context, id string) (notoapi.MeetingFiles, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.MeetingFiles{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	fp := s.repo.FilePaths(mid)
	if fp == nil {
		return notoapi.MeetingFiles{MeetingID: id}, nil
	}
	return notoapi.MeetingFiles{
		MeetingID:   id,
		MeetingDir:  fp.MeetingDir,
		Manifest:    fp.Manifest,
		Transcript:  fp.Transcript,
		SummaryMD:   fp.SummaryMD,
		SummaryJSON: fp.SummaryJSON,
		Audio:       fp.Audio,
	}, nil
}

// GetAgentHandoff returns file paths plus CLI commands an agent can run to
// access the meeting data as JSON.
func (s *Service) GetAgentHandoff(ctx context.Context, id string) (notoapi.AgentHandoff, error) {
	files, err := s.GetMeetingFiles(ctx, id)
	if err != nil {
		return notoapi.AgentHandoff{}, err
	}
	m, err := s.GetMeeting(ctx, id)
	if err != nil {
		return notoapi.AgentHandoff{}, err
	}
	cmds := []string{
		fmt.Sprintf("noto transcript --json %s", id),
		fmt.Sprintf("noto summary --json %s", id),
		fmt.Sprintf("noto files --json %s", id),
	}
	return notoapi.AgentHandoff{
		MeetingID: id,
		VersionID: m.CurrentVersionID,
		Files:     files,
		Commands:  cmds,
	}, nil
}

// DeleteMeeting removes the meeting and its search index entries.
func (s *Service) DeleteMeeting(ctx context.Context, id string) error {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	if err := s.repo.DeleteMeeting(ctx, mid); err != nil {
		return mapRepoErr(err, id)
	}
	if s.search != nil {
		_ = s.search.DeleteFromIndex(id)
	}
	// Drop this meeting's speaker mappings too — they live in a SEPARATE store the
	// artifact repo can't reach, so without this they orphan: a deleted meeting's
	// unresolved speakers keep inflating the "to identify" badge forever and skew
	// the cross-meeting identity priors (co-attendance / per-person meeting counts).
	// The seed/reset path already does this; the user-facing delete must match.
	if s.meetingMappings != nil {
		_ = s.meetingMappings.DeleteByMeeting(ctx, id)
	}
	s.emitStatusBar() // a deleted meeting can change the "to identify" count
	return nil
}

// UpdateSpeakerName rewrites a speaker's DisplayName in the transcript artifact.
func (s *Service) UpdateSpeakerName(ctx context.Context, meetingID, speakerID, displayName string) error {
	if speakerID == "" {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "speaker id is required", nil)
	}
	mid, err := uuid.Parse(meetingID)
	if err != nil {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	t, err := s.repo.LoadTranscript(ctx, mid)
	if err != nil {
		return mapRepoErr(err, meetingID)
	}
	found := false
	for i := range t.Speakers {
		if t.Speakers[i].ID == speakerID {
			t.Speakers[i].DisplayName = displayName
			found = true
			break
		}
	}
	if !found {
		return notoapi.NewError(notoapi.CodeNotFound, "speaker not found in meeting", map[string]any{"speaker_id": speakerID})
	}
	if err := s.repo.SaveTranscript(ctx, mid, t); err != nil {
		return err
	}
	// Refresh the search index so the renamed speaker is immediately searchable
	// by name (the FTS speaker column is built from the transcript's display
	// names). Best-effort: the rename already succeeded; a stale index self-heals
	// on the next reindex. Passing "" lets indexOneMeeting resolve the title.
	_ = s.indexOneMeeting(ctx, mid, "")
	return nil
}

// VerifyMeeting kicks an async verify job for one meeting and returns it.
func (s *Service) VerifyMeeting(ctx context.Context, id string) (notoapi.Job, error) {
	return s.CreateJob(ctx, notoapi.CreateJobOpts{
		Kind:      notoapi.JobVerify,
		MeetingID: id,
	})
}

// --- helpers ---

func meetingFromStored(sm *repo.StoredMeeting) notoapi.Meeting {
	m := notoapi.Meeting{
		ID:               sm.ID.String(),
		Title:            sm.Title,
		CreatedAt:        sm.CreatedAt,
		CurrentVersionID: sm.CurrentVersionID,
		ShortSummary:     sm.ShortSummary,
		Status:           statusFromStored(sm),
		DurationSeconds:  sm.DurationSeconds,
		Speakers:         sm.SpeakerCount,
		DecisionCount:    sm.DecisionCount,
		ActionCount:      sm.ActionCount,
		RiskCount:        sm.RiskCount,
		QuestionCount:    sm.QuestionCount,
	}
	for _, v := range sm.Versions {
		m.Versions = append(m.Versions, notoapi.Version{
			ID:        v.ID,
			CreatedAt: v.CreatedAt,
			Reason:    v.Reason,
			Checksum:  v.Checksum,
		})
	}
	return m
}

func statusFromStored(sm *repo.StoredMeeting) notoapi.MeetingStatus {
	switch {
	case sm.HasSummary:
		return notoapi.StatusSummarized
	case sm.HasTranscript:
		return notoapi.StatusTranscribed
	default:
		return notoapi.StatusRecorded
	}
}

func mapRepoErr(err error, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, repo.ErrNotFound) {
		return notoapi.NewError(notoapi.CodeNotFound, "meeting not found", map[string]any{"id": id})
	}
	return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
}

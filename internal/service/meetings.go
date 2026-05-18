package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/storage"
)

// ListMeetings reads the meetings directory and returns a paged list,
// most-recently-created first. Empty or missing dir is not an error.
func (s *Service) ListMeetings(_ context.Context, opts notoapi.ListMeetingsOpts) (notoapi.ListMeetingsResult, error) {
	refs, err := storage.ListMeetings(s.recordingsDir)
	if err != nil {
		return notoapi.ListMeetingsResult{}, err
	}

	q := strings.ToLower(strings.TrimSpace(opts.Query))
	out := make([]notoapi.Meeting, 0, len(refs))
	for _, ref := range refs {
		m := s.meetingFromRef(ref)
		if q != "" && !strings.Contains(strings.ToLower(m.Title), q) {
			continue
		}
		out = append(out, m)
	}
	total := len(out)
	if opts.Limit > 0 && opts.Limit < len(out) {
		out = out[:opts.Limit]
	}
	return notoapi.ListMeetingsResult{Meetings: out, Total: total}, nil
}

// GetMeeting returns a single meeting plus its derived counts.
func (s *Service) GetMeeting(_ context.Context, id string) (notoapi.Meeting, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.Meeting{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", map[string]any{"id": id})
	}
	m, err := storage.GetMeeting(s.recordingsDir, mid)
	if err != nil {
		return notoapi.Meeting{}, mapStorageErr(err, id)
	}

	out := notoapi.Meeting{
		ID:               m.MeetingID.String(),
		Title:            m.Title,
		CurrentVersionID: m.CurrentVersionID,
		ShortSummary:     m.ShortSummary,
		Status:           statusForMeetingID(s.recordingsDir, mid),
	}
	for _, v := range m.Versions {
		out.Versions = append(out.Versions, notoapi.Version{
			ID:        v.VersionID,
			CreatedAt: v.CreatedAt,
			Reason:    v.Reason,
			Checksum:  v.Checksum,
		})
	}
	if len(out.Versions) > 0 {
		out.CreatedAt = out.Versions[0].CreatedAt
	}
	// Enrich with transcript+summary derived counts where available.
	s.enrich(&out)
	return out, nil
}

func statusForMeetingID(recordingsDir string, mid uuid.UUID) notoapi.MeetingStatus {
	layout, err := storage.LayoutFor(recordingsDir, mid)
	if err != nil {
		return notoapi.StatusRecorded
	}
	if _, err := os.Stat(filepath.Join(layout.MeetingDir, "summary.json")); err == nil {
		return notoapi.StatusSummarized
	}
	if _, err := os.Stat(layout.TranscriptPath); err == nil {
		return notoapi.StatusTranscribed
	}
	return notoapi.StatusRecorded
}

// GetTranscript returns the normalized transcript for the meeting's
// current version.
func (s *Service) GetTranscript(_ context.Context, id string) (notoapi.Transcript, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.Transcript{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return notoapi.Transcript{}, err
	}
	t, err := storage.ReadTranscript(layout)
	if err != nil {
		return notoapi.Transcript{}, mapStorageErr(err, id)
	}
	out := notoapi.Transcript{MeetingID: id}
	for _, seg := range t.Segments {
		out.Segments = append(out.Segments, notoapi.TranscriptSegment{
			ID:         seg.ID,
			SpeakerID:  seg.SpeakerID,
			Speaker:    seg.SpeakerID,
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
			DisplayName: sp.DisplayName,
			Role:        sp.Origin,
		})
	}
	// Replace display names where available.
	if len(out.Speakers) > 0 {
		idx := map[string]string{}
		for _, sp := range out.Speakers {
			if sp.DisplayName != "" {
				idx[sp.ID] = sp.DisplayName
			}
		}
		for i := range out.Segments {
			if name, ok := idx[out.Segments[i].SpeakerID]; ok && name != "" {
				out.Segments[i].Speaker = name
			}
		}
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
func (s *Service) GetSummary(_ context.Context, id string) (notoapi.Summary, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.Summary{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return notoapi.Summary{}, err
	}
	mdText, _ := storage.ReadSummary(layout)
	jsonPath := filepath.Join(layout.MeetingDir, "summary.json")
	out := notoapi.Summary{MeetingID: id, Markdown: mdText}
	if data, err := os.ReadFile(jsonPath); err == nil {
		var raw artifacts.Summary
		if jsonErr := json.Unmarshal(data, &raw); jsonErr == nil {
			out.ShortSummary = raw.ShortSummary
			for _, d := range raw.Decisions {
				out.Decisions = append(out.Decisions, notoapi.SummaryItem{
					Text:        d.Text,
					SpeakerIDs:  d.SpeakerIDs,
					SegmentRefs: segmentRefsFromEvidence(d.Evidence),
				})
			}
			for _, a := range raw.ActionItems {
				out.ActionItems = append(out.ActionItems, notoapi.ActionItem{
					Text:        a.Text,
					Owner:       a.Owner,
					SegmentRefs: segmentRefsFromEvidence(a.Evidence),
				})
			}
			for _, r := range raw.Risks {
				out.Risks = append(out.Risks, notoapi.SummaryItem{
					Text:        r.Text,
					SegmentRefs: segmentRefsFromEvidence(r.Evidence),
				})
			}
			for _, oq := range raw.OpenQuestions {
				out.OpenQuestions = append(out.OpenQuestions, notoapi.SummaryItem{
					Text:        oq.Text,
					SegmentRefs: segmentRefsFromEvidence(oq.Evidence),
				})
			}
		}
	}
	if out.ShortSummary == "" && out.Markdown != "" {
		// Use the first non-empty line of the markdown as a short summary.
		for _, line := range strings.Split(out.Markdown, "\n") {
			if t := strings.TrimSpace(line); t != "" {
				out.ShortSummary = t
				break
			}
		}
	}
	return out, nil
}

// GetMeetingFiles returns the on-disk paths of artifacts for the meeting.
func (s *Service) GetMeetingFiles(_ context.Context, id string) (notoapi.MeetingFiles, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.MeetingFiles{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return notoapi.MeetingFiles{}, err
	}
	out := notoapi.MeetingFiles{
		MeetingID:  id,
		MeetingDir: layout.MeetingDir,
		Manifest:   pathIfExists(layout.ManifestPath),
		Transcript: pathIfExists(layout.TranscriptPath),
		SummaryMD:  pathIfExists(layout.SummaryPath),
		Audio:      pathIfExists(layout.AudioPath),
	}
	if p := filepath.Join(layout.MeetingDir, "summary.json"); pathIfExists(p) != "" {
		out.SummaryJSON = p
	}
	return out, nil
}

// GetAgentHandoff returns paths plus the verbatim CLI commands an agent
// can run to retrieve the same data via JSON.
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

// DeleteMeeting removes the on-disk meeting directory and any indexed
// entries.
func (s *Service) DeleteMeeting(ctx context.Context, id string) error {
	mid, err := uuid.Parse(id)
	if err != nil {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return err
	}
	if err := storage.DeleteMeeting(layout); err != nil {
		return mapStorageErr(err, id)
	}
	if s.search != nil {
		_ = s.search.DeleteFromIndex(id)
	}
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

func (s *Service) meetingFromRef(ref storage.MeetingRef) notoapi.Meeting {
	m := notoapi.Meeting{
		ID:               ref.MeetingID.String(),
		Title:            ref.Title,
		CreatedAt:        ref.CreatedAt,
		CurrentVersionID: ref.CurrentVersionID,
		Status:           statusForRef(s.recordingsDir, ref),
	}
	if m.Title == "" {
		m.Title = "Untitled meeting"
	}
	s.enrich(&m)
	return m
}

func (s *Service) enrich(m *notoapi.Meeting) {
	if m.ID == "" {
		return
	}
	mid, err := uuid.Parse(m.ID)
	if err != nil {
		return
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return
	}
	// Counts from summary if present.
	if data, err := os.ReadFile(filepath.Join(layout.MeetingDir, "summary.json")); err == nil {
		var raw artifacts.Summary
		if jerr := json.Unmarshal(data, &raw); jerr == nil {
			m.DecisionCount = len(raw.Decisions)
			m.ActionCount = len(raw.ActionItems)
			m.RiskCount = len(raw.Risks)
			if m.ShortSummary == "" {
				m.ShortSummary = raw.ShortSummary
			}
		}
	}
	// Duration + speakers from transcript if present.
	if data, err := os.ReadFile(layout.TranscriptPath); err == nil {
		var t artifacts.Transcript
		if jerr := json.Unmarshal(data, &t); jerr == nil {
			m.Speakers = len(t.Speakers)
			if len(t.Segments) > 0 {
				last := t.Segments[len(t.Segments)-1]
				m.DurationSeconds = int(last.EndSeconds)
			}
		}
	}
}

func statusForRef(recordingsDir string, ref storage.MeetingRef) notoapi.MeetingStatus {
	layout, err := storage.LayoutFor(recordingsDir, ref.MeetingID)
	if err != nil {
		return notoapi.StatusRecorded
	}
	if _, err := os.Stat(filepath.Join(layout.MeetingDir, "summary.json")); err == nil {
		return notoapi.StatusSummarized
	}
	if _, err := os.Stat(layout.TranscriptPath); err == nil {
		return notoapi.StatusTranscribed
	}
	if _, err := os.Stat(layout.AudioPath); err == nil {
		return notoapi.StatusRecorded
	}
	return notoapi.StatusRecorded
}

func pathIfExists(path string) string {
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func mapStorageErr(err error, id string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
		return notoapi.NewError(notoapi.CodeNotFound, "meeting not found", map[string]any{"id": id})
	}
	return notoapi.NewError(notoapi.CodeInternal, err.Error(), nil)
}

// Used by jobs/index workers, exported within package.
func (s *Service) layoutFor(id string) (storage.DirectoryLayout, uuid.UUID, error) {
	mid, err := uuid.Parse(id)
	if err != nil {
		return storage.DirectoryLayout{}, uuid.UUID{}, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil)
	}
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	return layout, mid, err
}

// segmentRefsFromEvidence flattens artifacts.Evidence into a plain
// list of segment ids — what callers need for citation rendering.
func segmentRefsFromEvidence(ev []artifacts.Evidence) []string {
	if len(ev) == 0 {
		return nil
	}
	out := make([]string, 0, len(ev))
	for _, e := range ev {
		if e.SegmentID != "" {
			out = append(out, e.SegmentID)
		}
	}
	return out
}

// touch keeps the unused-import linter happy across helpers we may add
// to this file later. No-op.
var _ = time.Now

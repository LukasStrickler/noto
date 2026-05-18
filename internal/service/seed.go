package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/storage"
)

// SeedDev populates the local store with three fully-formed fake
// meetings (manifest, transcript, summary markdown + JSON) and indexes
// them in the FTS5 search index. UUIDs are fixed so re-running upserts
// the same dirs instead of accumulating duplicates.
//
// Intended for `noto seed` / `make seed` only; never wired into the
// public notoapi.Client surface.
func (s *Service) SeedDev(ctx context.Context) ([]SeededMeeting, error) {
	_ = ctx
	out := make([]SeededMeeting, 0, len(seedFixtures))
	for _, fx := range seedFixtures {
		m, err := s.writeSeedMeeting(fx)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	return out, nil
}

// SeededMeeting is what SeedDev returns per meeting so CLI callers can
// print a useful summary.
type SeededMeeting struct {
	ID    string
	Title string
	Dir   string
}

func (s *Service) writeSeedMeeting(fx seedFixture) (SeededMeeting, error) {
	mid := uuid.MustParse(fx.ID)
	layout, err := storage.LayoutFor(s.recordingsDir, mid)
	if err != nil {
		return SeededMeeting{}, err
	}
	// Wipe any existing files so the seed is idempotent and we don't
	// leave behind a checksum that no longer matches the rewritten
	// manifest.
	_ = os.RemoveAll(layout.MeetingDir)
	if err := storage.EnsureDirs(layout); err != nil {
		return SeededMeeting{}, err
	}

	versionID := fmt.Sprintf("ver_seed_%s", fx.ID[:8])
	manifest := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        mid.String(),
		CurrentVersionID: versionID,
		Versions: []artifacts.ManifestVersion{
			{
				VersionID: versionID,
				CreatedAt: fx.CreatedAt,
				Reason:    "seed",
			},
		},
		Metadata: artifacts.ManifestMetadata{Title: fx.Title},
	}
	if err := storage.WriteManifest(layout, manifest); err != nil {
		return SeededMeeting{}, err
	}

	transcript := buildSeedTranscript(mid.String(), fx)
	if err := storage.WriteTranscript(layout, transcript); err != nil {
		return SeededMeeting{}, err
	}

	summaryJSON := buildSeedSummary(mid.String(), fx)
	if data, err := json.MarshalIndent(summaryJSON, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(layout.MeetingDir, "summary.json"), data, 0644)
	}
	if err := storage.WriteSummary(layout, renderSeedSummaryMD(fx)); err != nil {
		return SeededMeeting{}, err
	}

	if s.search != nil {
		_ = s.search.IndexMeetingFromInput(seedSearchInput(mid.String(), fx, transcript, summaryJSON))
	}

	return SeededMeeting{ID: mid.String(), Title: fx.Title, Dir: layout.MeetingDir}, nil
}

func buildSeedTranscript(meetingID string, fx seedFixture) *artifacts.Transcript {
	speakers := make([]artifacts.Speaker, 0, len(fx.Speakers))
	for _, sp := range fx.Speakers {
		speakers = append(speakers, artifacts.Speaker{
			ID:          sp.ID,
			Label:       sp.DisplayName,
			Origin:      "manual",
			DisplayName: sp.DisplayName,
		})
	}
	conf := 0.92
	segments := make([]artifacts.Segment, 0, len(fx.Segments))
	for i, seg := range fx.Segments {
		segments = append(segments, artifacts.Segment{
			ID:           fmt.Sprintf("seg_%03d", i+1),
			SpeakerID:    seg.SpeakerID,
			StartSeconds: seg.Start,
			EndSeconds:   seg.End,
			SourceID:     "seed",
			SourceRole:   "speaker",
			Text:         seg.Text,
			Confidence:   &conf,
		})
	}
	duration := 0.0
	if len(segments) > 0 {
		duration = segments[len(segments)-1].EndSeconds
	}
	return &artifacts.Transcript{
		SchemaVersion:   "transcript.v1",
		MeetingID:       meetingID,
		Language:        "en",
		DurationSeconds: duration,
		Provider: artifacts.TranscriptProvider{
			ID:    "seed",
			JobID: "seed-job",
		},
		Speakers: speakers,
		Segments: segments,
		Capabilities: artifacts.TranscriptCapabilities{
			SpeakerDiarization: true,
			SourceRoles:        true,
		},
	}
}

func buildSeedSummary(meetingID string, fx seedFixture) *artifacts.Summary {
	decisions := make([]artifacts.SummaryItem, 0, len(fx.Decisions))
	for _, d := range fx.Decisions {
		decisions = append(decisions, artifacts.SummaryItem{
			Text:     d.Text,
			Evidence: evidenceFromRefs(d.SegmentRefs),
		})
	}
	actions := make([]artifacts.ActionItem, 0, len(fx.Actions))
	for _, a := range fx.Actions {
		actions = append(actions, artifacts.ActionItem{
			Text:     a.Text,
			Owner:    a.Owner,
			Evidence: evidenceFromRefs(a.SegmentRefs),
		})
	}
	risks := make([]artifacts.SummaryItem, 0, len(fx.Risks))
	for _, r := range fx.Risks {
		risks = append(risks, artifacts.SummaryItem{
			Text:     r.Text,
			Evidence: evidenceFromRefs(r.SegmentRefs),
		})
	}
	return &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		ShortSummary:  fx.ShortSummary,
		Decisions:     decisions,
		ActionItems:   actions,
		Risks:         risks,
		Model: artifacts.SummaryModel{
			Provider:      "seed",
			ModelID:       "fixture",
			PromptVersion: "1",
		},
	}
}

func evidenceFromRefs(refs []int) []artifacts.Evidence {
	out := make([]artifacts.Evidence, 0, len(refs))
	for _, r := range refs {
		out = append(out, artifacts.Evidence{SegmentID: fmt.Sprintf("seg_%03d", r)})
	}
	return out
}

func renderSeedSummaryMD(fx seedFixture) string {
	out := fx.ShortSummary + "\n\n"
	if len(fx.Decisions) > 0 {
		out += "## Decisions\n"
		for _, d := range fx.Decisions {
			out += "- " + d.Text + "\n"
		}
		out += "\n"
	}
	if len(fx.Actions) > 0 {
		out += "## Action Items\n"
		for _, a := range fx.Actions {
			line := "- " + a.Text
			if a.Owner != "" {
				line += " — _" + a.Owner + "_"
			}
			out += line + "\n"
		}
		out += "\n"
	}
	if len(fx.Risks) > 0 {
		out += "## Risks\n"
		for _, r := range fx.Risks {
			out += "- " + r.Text + "\n"
		}
		out += "\n"
	}
	return out
}

func seedSearchInput(meetingID string, fx seedFixture, t *artifacts.Transcript, sum *artifacts.Summary) *search.IndexMeetingInput {
	segs := make([]search.TranscriptSegment, 0, len(t.Segments))
	for _, seg := range t.Segments {
		segs = append(segs, search.TranscriptSegment{
			SegmentID: seg.ID,
			Text:      seg.Text,
			Speaker:   seg.SpeakerID,
			Timestamp: seg.StartSeconds,
		})
	}
	decs := make([]search.SummaryItem, 0, len(sum.Decisions))
	for _, d := range sum.Decisions {
		decs = append(decs, search.SummaryItem{Text: d.Text})
	}
	acts := make([]search.ActionItem, 0, len(sum.ActionItems))
	for _, a := range sum.ActionItems {
		acts = append(acts, search.ActionItem{Text: a.Text, Owner: a.Owner})
	}
	risks := make([]search.SummaryItem, 0, len(sum.Risks))
	for _, r := range sum.Risks {
		risks = append(risks, search.SummaryItem{Text: r.Text})
	}
	return &search.IndexMeetingInput{
		MeetingID:          meetingID,
		Title:              fx.Title,
		CreatedAt:          fx.CreatedAt,
		TranscriptSegments: segs,
		Decisions:          decs,
		ActionItems:        acts,
		Risks:              risks,
	}
}

// --- fixtures ------------------------------------------------------

type seedSpeaker struct {
	ID          string
	DisplayName string
}

type seedSegment struct {
	SpeakerID string
	Start     float64
	End       float64
	Text      string
}

type seedDecision struct {
	Text        string
	SegmentRefs []int
}

type seedAction struct {
	Text        string
	Owner       string
	SegmentRefs []int
}

type seedRisk struct {
	Text        string
	SegmentRefs []int
}

type seedFixture struct {
	ID            string
	Title         string
	CreatedAt     time.Time
	ShortSummary  string
	Speakers      []seedSpeaker
	Segments      []seedSegment
	Decisions     []seedDecision
	Actions       []seedAction
	Risks         []seedRisk
}

// Use a deterministic time anchor so the seed is reproducible. Each
// fixture is offset back from this anchor by a few days so the
// list-by-recency ordering is stable and obvious.
var seedAnchor = time.Date(2025, 5, 12, 14, 30, 0, 0, time.UTC)

var seedFixtures = []seedFixture{
	{
		ID:           "11111111-1111-4111-8111-111111111111",
		Title:        "Q2 Roadmap Sync",
		CreatedAt:    seedAnchor,
		ShortSummary: "Aligned on the Q2 roadmap: ship search v2, defer the mobile beta to Q3, and run a billing-bug postmortem next week.",
		Speakers: []seedSpeaker{
			{ID: "spk_alice", DisplayName: "Alice"},
			{ID: "spk_bob", DisplayName: "Bob"},
			{ID: "spk_carol", DisplayName: "Carol"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_alice", Start: 0, End: 8, Text: "Welcome back everyone, let's lock in the Q2 roadmap today."},
			{SpeakerID: "spk_bob", Start: 8, End: 22, Text: "The search rewrite is the biggest item — we're targeting end of May for search v2."},
			{SpeakerID: "spk_carol", Start: 22, End: 36, Text: "Mobile beta is still blocked on the new auth flow, so I'd push it to Q3."},
			{SpeakerID: "spk_alice", Start: 36, End: 48, Text: "Agreed, mobile slips to Q3. Bob, you own search v2 ship."},
			{SpeakerID: "spk_bob", Start: 48, End: 62, Text: "Got it. I'll also schedule the billing-bug postmortem for next Tuesday."},
			{SpeakerID: "spk_carol", Start: 62, End: 74, Text: "One risk: the new search index migration is going to need a maintenance window."},
		},
		Decisions: []seedDecision{
			{Text: "Ship search v2 by end of May", SegmentRefs: []int{2, 4}},
			{Text: "Defer mobile beta to Q3", SegmentRefs: []int{3, 4}},
		},
		Actions: []seedAction{
			{Text: "Own search v2 release", Owner: "Bob", SegmentRefs: []int{4}},
			{Text: "Schedule billing-bug postmortem", Owner: "Bob", SegmentRefs: []int{5}},
		},
		Risks: []seedRisk{
			{Text: "Search index migration needs a maintenance window", SegmentRefs: []int{6}},
		},
	},
	{
		ID:           "22222222-2222-4222-8222-222222222222",
		Title:        "Customer Discovery: Acme Corp",
		CreatedAt:    seedAnchor.Add(-72 * time.Hour),
		ShortSummary: "Acme is evaluating noto for their distributed standups; biggest blockers are SSO and on-prem export.",
		Speakers: []seedSpeaker{
			{ID: "spk_dana", DisplayName: "Dana (Acme)"},
			{ID: "spk_evan", DisplayName: "Evan"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_evan", Start: 0, End: 10, Text: "Thanks for joining, Dana. Walk me through how your team runs standups today."},
			{SpeakerID: "spk_dana", Start: 10, End: 28, Text: "We're fully distributed across three timezones, so async voice notes feed into a daily digest."},
			{SpeakerID: "spk_dana", Start: 28, End: 44, Text: "For us to adopt noto we'd need SSO via Okta and the ability to export transcripts to our own S3 bucket."},
			{SpeakerID: "spk_evan", Start: 44, End: 58, Text: "Both are on the roadmap. SSO is targeted for Q2, on-prem export for Q3."},
			{SpeakerID: "spk_dana", Start: 58, End: 70, Text: "If those land we'd be ready to pilot with the platform team — about 40 seats."},
		},
		Decisions: []seedDecision{
			{Text: "Acme is a viable Q3 pilot if SSO + export land", SegmentRefs: []int{3, 4, 5}},
		},
		Actions: []seedAction{
			{Text: "Send Acme the SSO roadmap doc", Owner: "Evan", SegmentRefs: []int{4}},
			{Text: "Loop Acme into the on-prem export beta", Owner: "Evan", SegmentRefs: []int{4, 5}},
		},
		Risks: []seedRisk{
			{Text: "Pilot is contingent on Okta SSO landing in Q2", SegmentRefs: []int{3, 4}},
		},
	},
	{
		ID:           "33333333-3333-4333-8333-333333333333",
		Title:        "Postmortem: Billing 500s",
		CreatedAt:    seedAnchor.Add(-7 * 24 * time.Hour),
		ShortSummary: "Stripe webhook retries hammered the billing service for ~22 minutes; root cause was a missing idempotency key on subscription updates.",
		Speakers: []seedSpeaker{
			{ID: "spk_fiona", DisplayName: "Fiona"},
			{ID: "spk_greg", DisplayName: "Greg"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_fiona", Start: 0, End: 12, Text: "Incident started at 14:03 UTC when billing-service began returning 500s on /subscriptions/update."},
			{SpeakerID: "spk_greg", Start: 12, End: 28, Text: "Stripe was retrying the same webhook every 30 seconds because we didn't ACK fast enough."},
			{SpeakerID: "spk_fiona", Start: 28, End: 44, Text: "Each retry spawned a duplicate transaction — no idempotency key on the upstream call."},
			{SpeakerID: "spk_greg", Start: 44, End: 60, Text: "Mitigation was to drop the malformed webhook batch and add the idempotency key in a hotfix."},
			{SpeakerID: "spk_fiona", Start: 60, End: 76, Text: "We need contract tests for webhook idempotency so this can't regress."},
		},
		Decisions: []seedDecision{
			{Text: "Add idempotency keys to every Stripe write path", SegmentRefs: []int{3, 4}},
		},
		Actions: []seedAction{
			{Text: "Write contract tests for webhook idempotency", Owner: "Fiona", SegmentRefs: []int{5}},
			{Text: "Backfill idempotency keys for historical retry windows", Owner: "Greg", SegmentRefs: []int{4}},
		},
		Risks: []seedRisk{
			{Text: "Other Stripe write paths likely have the same gap", SegmentRefs: []int{3}},
		},
	},
}

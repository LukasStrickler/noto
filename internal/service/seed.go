package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/repo"
	"github.com/lukasstrickler/noto/internal/search"
)

// SeedDev populates the local store with fully-formed fake meetings
// (manifest, transcript, summary) and indexes them in the FTS5 search
// index. UUIDs are fixed so re-running upserts instead of duplicating.
//
// Intended for `noto seed` / `make seed` only; never exposed via
// notoapi.Client.
func (s *Service) SeedDev(ctx context.Context) ([]SeededMeeting, error) {
	out := make([]SeededMeeting, 0, len(seedFixtures))
	for _, fx := range seedFixtures {
		m, err := s.writeSeedMeeting(ctx, fx)
		if err != nil {
			return out, err
		}
		out = append(out, m)
	}
	// Optimize the FTS index once after seeding all fixtures.
	if s.search != nil {
		_ = s.search.Optimize()
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

func (s *Service) writeSeedMeeting(ctx context.Context, fx seedFixture) (SeededMeeting, error) {
	mid := uuid.MustParse(fx.ID)

	// Wipe any existing data so the seed is idempotent.
	_ = s.repo.DeleteMeeting(ctx, mid)

	if err := s.repo.CreateMeeting(ctx, mid, repo.CreateMeetingOpts{
		Title:     fx.Title,
		Reason:    "seed",
		CreatedAt: fx.CreatedAt,
	}); err != nil {
		return SeededMeeting{}, err
	}

	transcript := buildSeedTranscript(mid.String(), fx)
	if err := s.repo.SaveTranscript(ctx, mid, transcript); err != nil {
		return SeededMeeting{}, err
	}

	summaryJSON := buildSeedSummary(mid.String(), fx)
	if err := s.repo.SaveSummary(ctx, mid, renderSeedSummaryMD(fx), summaryJSON); err != nil {
		return SeededMeeting{}, err
	}

	if s.search != nil {
		_ = s.search.IndexMeetingFromInput(seedSearchInput(mid.String(), fx, transcript, summaryJSON))
	}

	fp := s.repo.FilePaths(mid)
	dir := ""
	if fp != nil {
		dir = fp.MeetingDir
	}
	return SeededMeeting{ID: mid.String(), Title: fx.Title, Dir: dir}, nil
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
	questions := make([]artifacts.SummaryItem, 0, len(fx.Questions))
	for _, q := range fx.Questions {
		questions = append(questions, artifacts.SummaryItem{
			Text:     q.Text,
			Evidence: evidenceFromRefs(q.SegmentRefs),
		})
	}
	return &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		ShortSummary:  fx.ShortSummary,
		Decisions:     decisions,
		ActionItems:   actions,
		Risks:         risks,
		OpenQuestions: questions,
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
	if len(fx.Questions) > 0 {
		out += "## Open Questions\n"
		for _, q := range fx.Questions {
			out += "- " + q.Text + "\n"
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
	questions := make([]search.SummaryItem, 0, len(sum.OpenQuestions))
	for _, q := range sum.OpenQuestions {
		questions = append(questions, search.SummaryItem{Text: q.Text})
	}
	return &search.IndexMeetingInput{
		MeetingID:          meetingID,
		Title:              fx.Title,
		ShortSummary:       fx.ShortSummary,
		CreatedAt:          fx.CreatedAt,
		TranscriptSegments: segs,
		Decisions:          decs,
		ActionItems:        acts,
		Risks:              risks,
		OpenQuestions:      questions,
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

type seedQuestion struct {
	Text        string
	SegmentRefs []int
}

type seedFixture struct {
	ID           string
	Title        string
	CreatedAt    time.Time
	ShortSummary string
	Speakers     []seedSpeaker
	Segments     []seedSegment
	Decisions    []seedDecision
	Actions      []seedAction
	Risks        []seedRisk
	Questions    []seedQuestion
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
		Questions: []seedQuestion{
			{Text: "Do we have headcount to staff the search v2 oncall rotation?", SegmentRefs: []int{2}},
			{Text: "Should the mobile beta wait on the auth flow rewrite or run in parallel?", SegmentRefs: []int{3, 4}},
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
		Questions: []seedQuestion{
			{Text: "Would Acme accept SAML as a stopgap if Okta SSO slips?", SegmentRefs: []int{3, 4}},
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
		Questions: []seedQuestion{
			{Text: "Do other vendors (Adyen, Braintree) need the same idempotency audit?", SegmentRefs: []int{3, 5}},
			{Text: "Should we add synthetic monitoring on the webhook ACK latency?", SegmentRefs: []int{2}},
		},
	},
	{
		ID:           "44444444-4444-4444-8444-444444444444",
		Title:        "Mobile beta kickoff",
		CreatedAt:    seedAnchor.Add(-2 * 24 * time.Hour),
		ShortSummary: "Kicked off the mobile beta program: targeting 25 design partners on iOS first, Android two weeks behind. Crash reporting via Sentry is mandatory before invite.",
		Speakers: []seedSpeaker{
			{ID: "spk_priya", DisplayName: "Priya"},
			{ID: "spk_marc", DisplayName: "Marc"},
			{ID: "spk_lin", DisplayName: "Lin"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_priya", Start: 0, End: 14, Text: "Welcome to the mobile beta kickoff — we have 25 design partners lined up across iOS and Android."},
			{SpeakerID: "spk_marc", Start: 14, End: 30, Text: "I'd ship iOS first and let Android catch up two weeks later; our TestFlight pipeline is rock solid."},
			{SpeakerID: "spk_lin", Start: 30, End: 48, Text: "Sentry crash reporting has to be wired before we send invites — last beta we flew blind for three days."},
			{SpeakerID: "spk_priya", Start: 48, End: 64, Text: "Agreed. Marc, you own the iOS rollout. Lin, you own the Sentry integration."},
			{SpeakerID: "spk_marc", Start: 64, End: 78, Text: "One concern: the offline-first sync code hasn't been load-tested on slow networks."},
			{SpeakerID: "spk_lin", Start: 78, End: 92, Text: "I can run a Lighthouse-style throttled-network test on staging before the invite blast."},
		},
		Decisions: []seedDecision{
			{Text: "Ship iOS beta first, Android two weeks later", SegmentRefs: []int{2, 4}},
			{Text: "Sentry crash reporting is a prerequisite for invites", SegmentRefs: []int{3, 4}},
		},
		Actions: []seedAction{
			{Text: "Own iOS beta rollout", Owner: "Marc", SegmentRefs: []int{4}},
			{Text: "Wire Sentry crash reporting", Owner: "Lin", SegmentRefs: []int{3, 4}},
			{Text: "Throttled-network test the offline sync path", Owner: "Lin", SegmentRefs: []int{5, 6}},
		},
		Risks: []seedRisk{
			{Text: "Offline-first sync hasn't been load-tested on slow networks", SegmentRefs: []int{5}},
		},
		Questions: []seedQuestion{
			{Text: "Do we have a kill switch if the iOS beta blows up?", SegmentRefs: []int{2}},
			{Text: "What's the invite cadence — all 25 at once or phased?", SegmentRefs: []int{1}},
		},
	},
	{
		ID:           "55555555-5555-4555-8555-555555555555",
		Title:        "Security incident: webhook secret leak",
		CreatedAt:    seedAnchor.Add(-10 * 24 * time.Hour),
		ShortSummary: "An intern accidentally committed a webhook signing secret to a public fork. We rotated the secret within 9 minutes; no observed abuse, but we're tightening pre-commit scanning.",
		Speakers: []seedSpeaker{
			{ID: "spk_omar", DisplayName: "Omar"},
			{ID: "spk_rita", DisplayName: "Rita"},
			{ID: "spk_sam", DisplayName: "Sam"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_omar", Start: 0, End: 14, Text: "At 09:14 UTC GitHub's secret scanner flagged a webhook signing key in a public fork."},
			{SpeakerID: "spk_rita", Start: 14, End: 30, Text: "We rotated within 9 minutes and force-pushed over the commit, but the diff was indexed by archive.org."},
			{SpeakerID: "spk_sam", Start: 30, End: 48, Text: "I checked the Stripe and Slack audit logs — no anomalous webhook traffic during the exposure window."},
			{SpeakerID: "spk_omar", Start: 48, End: 64, Text: "We need a pre-commit hook that scans for our specific secret prefixes, not just generic patterns."},
			{SpeakerID: "spk_rita", Start: 64, End: 78, Text: "Long term we should move webhook secrets to short-lived signed JWTs."},
		},
		Decisions: []seedDecision{
			{Text: "Add pre-commit scanning for project-specific secret prefixes", SegmentRefs: []int{4}},
			{Text: "Schedule a session on rotating to short-lived signed JWTs", SegmentRefs: []int{5}},
		},
		Actions: []seedAction{
			{Text: "Write pre-commit hook scanning for noto secret prefixes", Owner: "Omar", SegmentRefs: []int{4}},
			{Text: "Spike short-lived JWT webhook secrets", Owner: "Rita", SegmentRefs: []int{5}},
		},
		Risks: []seedRisk{
			{Text: "Leaked secret is mirrored on archive.org despite force-push", SegmentRefs: []int{2}},
			{Text: "Generic secret scanners miss our custom prefixes", SegmentRefs: []int{4}},
		},
		Questions: []seedQuestion{
			{Text: "Should we onboard interns to a sandbox org with no production secrets?", SegmentRefs: []int{1}},
		},
	},
	{
		ID:           "66666666-6666-4666-8666-666666666666",
		Title:        "Hiring loop: senior backend",
		CreatedAt:    seedAnchor.Add(-14 * 24 * time.Hour),
		ShortSummary: "Debrief for senior backend candidate Jasmin. Strong systems design and ownership signals; mixed coding-round feedback. Leaning toward extending an offer with onboarding plan focused on Go.",
		Speakers: []seedSpeaker{
			{ID: "spk_nick", DisplayName: "Nick (hiring manager)"},
			{ID: "spk_yuri", DisplayName: "Yuri"},
			{ID: "spk_paula", DisplayName: "Paula"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_nick", Start: 0, End: 12, Text: "Debrief for Jasmin — systems design round and coding round both ran this morning."},
			{SpeakerID: "spk_yuri", Start: 12, End: 28, Text: "Systems design was a strong yes. She designed a sharded queue with thoughtful backpressure handling."},
			{SpeakerID: "spk_paula", Start: 28, End: 46, Text: "Coding round was uneven — fluent in Python but rusty on Go syntax. The logic was correct though."},
			{SpeakerID: "spk_nick", Start: 46, End: 60, Text: "Ownership signals are excellent. She led an incident review at her last role that we'd consider exemplary."},
			{SpeakerID: "spk_yuri", Start: 60, End: 76, Text: "If we hire her I'd pair her with a senior on the routing rewrite for the first six weeks."},
			{SpeakerID: "spk_paula", Start: 76, End: 90, Text: "Agreed on offer, with a Go-focused onboarding plan."},
		},
		Decisions: []seedDecision{
			{Text: "Extend an offer to Jasmin with a Go-focused onboarding plan", SegmentRefs: []int{4, 6}},
		},
		Actions: []seedAction{
			{Text: "Draft offer letter at L5 band", Owner: "Nick", SegmentRefs: []int{4, 6}},
			{Text: "Pair Jasmin with senior on routing rewrite for first six weeks", Owner: "Yuri", SegmentRefs: []int{5}},
		},
		Risks: []seedRisk{
			{Text: "Go ramp-up may slow her first quarter's velocity", SegmentRefs: []int{3, 6}},
		},
		Questions: []seedQuestion{
			{Text: "Is there a competing offer from her current company we should account for?", SegmentRefs: []int{1}},
			{Text: "Do we have bandwidth on the senior side for the pairing plan?", SegmentRefs: []int{5}},
		},
	},
	{
		ID:           "77777777-7777-4777-8777-777777777777",
		Title:        "Vendor renewal: Datadog",
		CreatedAt:    seedAnchor.Add(-17 * 24 * time.Hour),
		ShortSummary: "Datadog renewal comes up in 60 days. Usage has grown 38% YoY; sales is pitching a 22% renewal uplift. We have leverage via the Grafana migration POC.",
		Speakers: []seedSpeaker{
			{ID: "spk_eve", DisplayName: "Eve (finance)"},
			{ID: "spk_dan", DisplayName: "Dan (infra lead)"},
			{ID: "spk_riya", DisplayName: "Riya"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_eve", Start: 0, End: 12, Text: "Datadog renewal is 60 days out; their sales rep opened with a 22% uplift quote."},
			{SpeakerID: "spk_dan", Start: 12, End: 28, Text: "Usage genuinely grew 38% YoY, mostly from log volume on the new ingestion pipeline."},
			{SpeakerID: "spk_riya", Start: 28, End: 46, Text: "The Grafana POC is far enough along that we can credibly walk away — that's our leverage."},
			{SpeakerID: "spk_dan", Start: 46, End: 62, Text: "I want to keep APM on Datadog regardless; the dashboards are too embedded in team workflows."},
			{SpeakerID: "spk_eve", Start: 62, End: 78, Text: "Counter-proposal: hold pricing flat, in exchange for a two-year commit on APM only."},
		},
		Decisions: []seedDecision{
			{Text: "Counter Datadog with a two-year flat-pricing commit on APM only", SegmentRefs: []int{5}},
		},
		Actions: []seedAction{
			{Text: "Send Datadog the two-year APM-only counter", Owner: "Eve", SegmentRefs: []int{5}},
			{Text: "Finalize Grafana POC migration estimate as walk-away leverage", Owner: "Riya", SegmentRefs: []int{3}},
		},
		Risks: []seedRisk{
			{Text: "Walking away from Datadog logs breaks team workflows mid-quarter", SegmentRefs: []int{4}},
		},
		Questions: []seedQuestion{
			{Text: "Can finance commit to a two-year contract given the runway picture?", SegmentRefs: []int{5}},
			{Text: "What's the realistic Grafana migration cost if Datadog walks?", SegmentRefs: []int{3}},
			{Text: "Does the APM dashboard set survive a Grafana port?", SegmentRefs: []int{4}},
		},
	},
	{
		ID:           "88888888-8888-4888-8888-888888888888",
		Title:        "Design review: Search v2",
		CreatedAt:    seedAnchor.Add(-21 * 24 * time.Hour),
		ShortSummary: "Reviewed Bob's design for search v2: FTS5 with custom BM25 weights, dedicated summary-body column, and prefix matching for partial queries. Approved with two follow-ups on snippet rendering and re-index strategy.",
		Speakers: []seedSpeaker{
			{ID: "spk_bob", DisplayName: "Bob"},
			{ID: "spk_alice", DisplayName: "Alice"},
			{ID: "spk_carol", DisplayName: "Carol"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_bob", Start: 0, End: 14, Text: "Search v2 is FTS5 with custom BM25 weights — titles ×5, summary body ×3, summary bullets ×2, transcript ×1."},
			{SpeakerID: "spk_alice", Start: 14, End: 30, Text: "Prefix matching is the headline UX win — `mob` finds `mobile` without forcing the user to type the whole word."},
			{SpeakerID: "spk_carol", Start: 30, End: 48, Text: "I'd add a dedicated summary-body column so a hit anywhere in the summary outranks a generic transcript hit."},
			{SpeakerID: "spk_bob", Start: 48, End: 62, Text: "Already in the design — that's where the ×3 weight comes from."},
			{SpeakerID: "spk_alice", Start: 62, End: 78, Text: "Two follow-ups: snippet rendering shouldn't break on multi-byte runes, and we need a migration story for the new column set."},
			{SpeakerID: "spk_carol", Start: 78, End: 92, Text: "Schema version bump plus a drop-and-rebuild on mismatch is the safe move."},
		},
		Decisions: []seedDecision{
			{Text: "Approve search v2 design with column weights as proposed", SegmentRefs: []int{1, 4}},
			{Text: "Bump FTS5 schema version and rebuild on mismatch", SegmentRefs: []int{6}},
		},
		Actions: []seedAction{
			{Text: "Wire prefix matching in sanitizeFTS5Query", Owner: "Bob", SegmentRefs: []int{2}},
			{Text: "Add summary_body FTS column with ×3 BM25 weight", Owner: "Bob", SegmentRefs: []int{3, 4}},
			{Text: "Audit snippet rendering for multi-byte rune safety", Owner: "Alice", SegmentRefs: []int{5}},
		},
		Risks: []seedRisk{
			{Text: "Rebuild-on-mismatch wipes the index until reindex jobs finish", SegmentRefs: []int{6}},
		},
		Questions: []seedQuestion{
			{Text: "Should we expose the column weights in config for advanced users?", SegmentRefs: []int{1}},
			{Text: "Does FTS5 prefix matching play well with unicode61 on non-Latin scripts?", SegmentRefs: []int{2}},
		},
	},
}

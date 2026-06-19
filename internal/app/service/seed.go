package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/platform/search"
)

// SeedResult is what SeedDev returns so callers can summarize what landed.
type SeedResult struct {
	Meetings []SeededMeeting
	People   int // speaker profiles seeded into the People directory
}

// SeedDev populates the local store with fully-formed fake meetings
// (manifest, transcript, summary), indexes them in the FTS5 search index, and
// layers a People directory + per-meeting speaker→person mappings on top so the
// People screen and the Speakers-tab assignment flow have real data. UUIDs are
// fixed so re-running upserts instead of duplicating.
//
// Intended for `noto seed` / `make seed` only; never exposed via
// notoapi.Client.
func (s *Service) SeedDev(ctx context.Context) (SeedResult, error) {
	res := SeedResult{Meetings: make([]SeededMeeting, 0, len(seedFixtures))}
	for _, fx := range seedFixtures {
		m, err := s.writeSeedMeeting(ctx, fx)
		if err != nil {
			return res, err
		}
		res.Meetings = append(res.Meetings, m)
	}
	// Optimize the FTS index once after seeding all fixtures.
	if s.search != nil {
		_ = s.search.Optimize()
	}
	people, err := s.seedSpeakerIdentity(ctx)
	if err != nil {
		return res, err
	}
	res.People = people
	return res, nil
}

// SeededMeeting is what SeedDev returns per meeting so CLI callers can
// print a useful summary.
type SeededMeeting struct {
	ID    string
	Title string
	Dir   string
}

// PurgeResult reports how much PurgeAll removed.
type PurgeResult struct {
	Meetings int
	People   int
}

// PurgeAll wipes the local store back to empty: every meeting (+ its search
// index entries and speaker mappings) and every speaker profile. Unlike
// SeedDev — which only touches its own fixtures — this removes ALL local data,
// so the CLI gates it behind an explicit confirmation. Host-only; never exposed
// via notoapi.Client.
func (s *Service) PurgeAll(ctx context.Context) (PurgeResult, error) {
	var res PurgeResult
	stored, err := s.repo.ListMeetings(ctx)
	if err != nil {
		return res, err
	}
	for _, sm := range stored {
		if err := s.repo.DeleteMeeting(ctx, sm.ID); err != nil {
			return res, err
		}
		if s.search != nil {
			_ = s.search.DeleteFromIndex(sm.ID.String())
		}
		if s.meetingMappings != nil {
			_ = s.meetingMappings.DeleteByMeeting(ctx, sm.ID.String())
		}
		res.Meetings++
	}
	if s.speakerProfiles != nil {
		profiles, err := s.speakerProfiles.List(ctx)
		if err != nil {
			return res, err
		}
		for _, p := range profiles {
			if err := s.speakerProfiles.Delete(ctx, p.ID); err != nil {
				return res, err
			}
			res.People++
		}
	}
	return res, nil
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

// seedSpeakerLabel is the anonymous diarization label a speaker carries in the
// transcript ("Speaker A", "Speaker B", …), assigned by their position in the
// meeting. Real diarization never knows names — identity comes only from a
// linked profile (rendered via ProfileName at view time), so leaving these
// anonymous keeps the seed honest: an unidentified speaker reads as
// "Speaker C — unknown — name them", not as a name that contradicts "unknown".
func seedSpeakerLabel(i int) string {
	if i < 26 {
		return "Speaker " + string(rune('A'+i))
	}
	return fmt.Sprintf("Speaker %d", i+1)
}

// seedSpeakerTokens maps each fixture speaker's id to its anonymous per-meeting
// token ("Speaker A"…). Fixture prose references a participant by their id in a
// `{spk_bob}` placeholder so the build step can render it to the token — exactly
// the anonymous handle a real token-based LLM would emit. The UI then resolves
// that token back to the person's name + identity color locally (renderPeople),
// so a confirmed speaker reads as their name, a likely one as a marked guess,
// and an unidentified one stays "Speaker A" — never a bare, unstructured first
// name. (No real names live in the stored artifacts, mirroring production.)
func seedSpeakerTokens(fx seedFixture) map[string]string {
	m := make(map[string]string, len(fx.Speakers))
	for i, sp := range fx.Speakers {
		m[sp.ID] = fmt.Sprintf("@S%d", i+1)
	}
	return m
}

// applySpeakerTokens replaces every `{spk_xxx}` placeholder in fixture text with
// the meeting's per-speaker token.
func applySpeakerTokens(text string, tokens map[string]string) string {
	if !strings.Contains(text, "{spk_") {
		return text
	}
	for id, tok := range tokens {
		text = strings.ReplaceAll(text, "{"+id+"}", tok)
	}
	return text
}

// seedOwnerToken renders an action owner: a `spk_*` speaker id becomes that
// meeting's token (so it resolves to a person), anything else passes through.
func seedOwnerToken(owner string, tokens map[string]string) string {
	if tok, ok := tokens[owner]; ok {
		return tok
	}
	return owner
}

func buildSeedTranscript(meetingID string, fx seedFixture) *artifacts.Transcript {
	speakers := make([]artifacts.Speaker, 0, len(fx.Speakers))
	for i, sp := range fx.Speakers {
		label := seedSpeakerLabel(i)
		speakers = append(speakers, artifacts.Speaker{
			ID:          sp.ID,
			Label:       label,
			Origin:      "diarization",
			DisplayName: label,
		})
	}
	tokens := seedSpeakerTokens(fx)
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
			Text:         applySpeakerTokens(seg.Text, tokens),
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
	tokens := seedSpeakerTokens(fx)
	decisions := make([]artifacts.SummaryItem, 0, len(fx.Decisions))
	for _, d := range fx.Decisions {
		decisions = append(decisions, artifacts.SummaryItem{
			Text:     applySpeakerTokens(d.Text, tokens),
			Evidence: evidenceFromRefs(d.SegmentRefs),
		})
	}
	actions := make([]artifacts.ActionItem, 0, len(fx.Actions))
	for _, a := range fx.Actions {
		actions = append(actions, artifacts.ActionItem{
			Text:     applySpeakerTokens(a.Text, tokens),
			Owner:    seedOwnerToken(a.Owner, tokens),
			Evidence: evidenceFromRefs(a.SegmentRefs),
		})
	}
	risks := make([]artifacts.SummaryItem, 0, len(fx.Risks))
	for _, r := range fx.Risks {
		risks = append(risks, artifacts.SummaryItem{
			Text:     applySpeakerTokens(r.Text, tokens),
			Evidence: evidenceFromRefs(r.SegmentRefs),
		})
	}
	questions := make([]artifacts.SummaryItem, 0, len(fx.Questions))
	for _, q := range fx.Questions {
		questions = append(questions, artifacts.SummaryItem{
			Text:     applySpeakerTokens(q.Text, tokens),
			Evidence: evidenceFromRefs(q.SegmentRefs),
		})
	}
	return &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID,
		ShortSummary:  applySpeakerTokens(fx.ShortSummary, tokens),
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
	tokens := seedSpeakerTokens(fx)
	tok := func(s string) string { return applySpeakerTokens(s, tokens) }
	out := tok(fx.ShortSummary) + "\n\n"
	if len(fx.Decisions) > 0 {
		out += "## Decisions\n"
		for _, d := range fx.Decisions {
			out += "- " + tok(d.Text) + "\n"
		}
		out += "\n"
	}
	if len(fx.Actions) > 0 {
		out += "## Action Items\n"
		for _, a := range fx.Actions {
			line := "- " + tok(a.Text)
			if a.Owner != "" {
				line += " — " + seedOwnerToken(a.Owner, tokens)
			}
			out += line + "\n"
		}
		out += "\n"
	}
	if len(fx.Risks) > 0 {
		out += "## Risks\n"
		for _, r := range fx.Risks {
			out += "- " + tok(r.Text) + "\n"
		}
		out += "\n"
	}
	if len(fx.Questions) > 0 {
		out += "## Open Questions\n"
		for _, q := range fx.Questions {
			out += "- " + tok(q.Text) + "\n"
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
		ShortSummary:       sum.ShortSummary,
		CreatedAt:          fx.CreatedAt,
		TranscriptSegments: segs,
		Decisions:          decs,
		ActionItems:        acts,
		Risks:              risks,
		OpenQuestions:      questions,
	}
}

// --- fixtures ------------------------------------------------------

// seedSpeaker is one diarized voice in a fixture meeting. It carries no name:
// the transcript label is anonymous (seedSpeakerLabel, by position) and the
// real identity, when known, comes from the linked profile in seedPeople. The
// ID (e.g. "spk_sam") both joins to that profile and documents who it is.
type seedSpeaker struct {
	ID string
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
			{ID: "spk_alice"},
			{ID: "spk_bob"},
			{ID: "spk_carol"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_alice", Start: 0, End: 8, Text: "Welcome back everyone, let's lock in the Q2 roadmap today."},
			{SpeakerID: "spk_bob", Start: 8, End: 22, Text: "The search rewrite is the biggest item — we're targeting end of May for search v2."},
			{SpeakerID: "spk_carol", Start: 22, End: 36, Text: "Mobile beta is still blocked on the new auth flow, so I'd push it to Q3."},
			{SpeakerID: "spk_alice", Start: 36, End: 48, Text: "Agreed, mobile slips to Q3. {spk_bob}, you own search v2 ship."},
			{SpeakerID: "spk_bob", Start: 48, End: 62, Text: "Got it. I'll also schedule the billing-bug postmortem for next Tuesday."},
			{SpeakerID: "spk_carol", Start: 62, End: 74, Text: "One risk: the new search index migration is going to need a maintenance window."},
		},
		Decisions: []seedDecision{
			{Text: "Ship search v2 by end of May", SegmentRefs: []int{2, 4}},
			{Text: "Defer mobile beta to Q3", SegmentRefs: []int{3, 4}},
		},
		Actions: []seedAction{
			{Text: "Own search v2 release", Owner: "spk_bob", SegmentRefs: []int{4}},
			{Text: "Schedule billing-bug postmortem", Owner: "spk_bob", SegmentRefs: []int{5}},
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
			{ID: "spk_dana"},
			{ID: "spk_evan"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_evan", Start: 0, End: 10, Text: "Thanks for joining, {spk_dana}. Walk me through how your team runs standups today."},
			{SpeakerID: "spk_dana", Start: 10, End: 28, Text: "We're fully distributed across three timezones, so async voice notes feed into a daily digest."},
			{SpeakerID: "spk_dana", Start: 28, End: 44, Text: "For us to adopt noto we'd need SSO via Okta and the ability to export transcripts to our own S3 bucket."},
			{SpeakerID: "spk_evan", Start: 44, End: 58, Text: "Both are on the roadmap. SSO is targeted for Q2, on-prem export for Q3."},
			{SpeakerID: "spk_dana", Start: 58, End: 70, Text: "If those land we'd be ready to pilot with the platform team — about 40 seats."},
		},
		Decisions: []seedDecision{
			{Text: "Acme is a viable Q3 pilot if SSO + export land", SegmentRefs: []int{3, 4, 5}},
		},
		Actions: []seedAction{
			{Text: "Send Acme the SSO roadmap doc", Owner: "spk_evan", SegmentRefs: []int{4}},
			{Text: "Loop Acme into the on-prem export beta", Owner: "spk_evan", SegmentRefs: []int{4, 5}},
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
			{ID: "spk_fiona"},
			{ID: "spk_greg"},
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
			{Text: "Write contract tests for webhook idempotency", Owner: "spk_fiona", SegmentRefs: []int{5}},
			{Text: "Backfill idempotency keys for historical retry windows", Owner: "spk_greg", SegmentRefs: []int{4}},
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
			{ID: "spk_priya"},
			{ID: "spk_marc"},
			{ID: "spk_lin"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_priya", Start: 0, End: 14, Text: "Welcome to the mobile beta kickoff — we have 25 design partners lined up across iOS and Android."},
			{SpeakerID: "spk_marc", Start: 14, End: 30, Text: "I'd ship iOS first and let Android catch up two weeks later; our TestFlight pipeline is rock solid."},
			{SpeakerID: "spk_lin", Start: 30, End: 48, Text: "Sentry crash reporting has to be wired before we send invites — last beta we flew blind for three days."},
			{SpeakerID: "spk_priya", Start: 48, End: 64, Text: "Agreed. {spk_marc}, you own the iOS rollout. {spk_lin}, you own the Sentry integration."},
			{SpeakerID: "spk_marc", Start: 64, End: 78, Text: "One concern: the offline-first sync code hasn't been load-tested on slow networks."},
			{SpeakerID: "spk_lin", Start: 78, End: 92, Text: "I can run a Lighthouse-style throttled-network test on staging before the invite blast."},
		},
		Decisions: []seedDecision{
			{Text: "Ship iOS beta first, Android two weeks later", SegmentRefs: []int{2, 4}},
			{Text: "Sentry crash reporting is a prerequisite for invites", SegmentRefs: []int{3, 4}},
		},
		Actions: []seedAction{
			{Text: "Own iOS beta rollout", Owner: "spk_marc", SegmentRefs: []int{4}},
			{Text: "Wire Sentry crash reporting", Owner: "spk_lin", SegmentRefs: []int{3, 4}},
			{Text: "Throttled-network test the offline sync path", Owner: "spk_lin", SegmentRefs: []int{5, 6}},
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
			{ID: "spk_omar"},
			{ID: "spk_rita"},
			{ID: "spk_sam"},
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
			{Text: "Write pre-commit hook scanning for noto secret prefixes", Owner: "spk_omar", SegmentRefs: []int{4}},
			{Text: "Spike short-lived JWT webhook secrets", Owner: "spk_rita", SegmentRefs: []int{5}},
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
			{ID: "spk_nick"},
			{ID: "spk_yuri"},
			{ID: "spk_paula"},
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
			{Text: "Draft offer letter at L5 band", Owner: "spk_nick", SegmentRefs: []int{4, 6}},
			{Text: "Pair Jasmin with senior on routing rewrite for first six weeks", Owner: "spk_yuri", SegmentRefs: []int{5}},
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
			{ID: "spk_eve"},
			{ID: "spk_dan"},
			{ID: "spk_riya"},
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
			{Text: "Send Datadog the two-year APM-only counter", Owner: "spk_eve", SegmentRefs: []int{5}},
			{Text: "Finalize Grafana POC migration estimate as walk-away leverage", Owner: "spk_riya", SegmentRefs: []int{3}},
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
		ShortSummary: "Reviewed {spk_bob}'s design for search v2: FTS5 with custom BM25 weights, dedicated summary-body column, and prefix matching for partial queries. Approved with two follow-ups on snippet rendering and re-index strategy.",
		Speakers: []seedSpeaker{
			{ID: "spk_bob"},
			{ID: "spk_alice"},
			{ID: "spk_carol"},
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
			{Text: "Wire prefix matching in sanitizeFTS5Query", Owner: "spk_bob", SegmentRefs: []int{2}},
			{Text: "Add a dedicated summary-body search column ranked above transcript hits", Owner: "spk_bob", SegmentRefs: []int{3, 4}},
			{Text: "Audit snippet rendering for multi-byte rune safety", Owner: "spk_alice", SegmentRefs: []int{5}},
		},
		Risks: []seedRisk{
			{Text: "Rebuild-on-mismatch wipes the index until reindex jobs finish", SegmentRefs: []int{6}},
		},
		Questions: []seedQuestion{
			{Text: "Should we expose the column weights in config for advanced users?", SegmentRefs: []int{1}},
			{Text: "Does FTS5 prefix matching play well with unicode61 on non-Latin scripts?", SegmentRefs: []int{2}},
		},
	},
	{
		// A deliberately CROWDED meeting (six named attendees) so the detail
		// pane's people roster has to wrap to multiple rows and the full-width
		// timeline shows many speakers. Newest CreatedAt so it previews by default.
		// Everyone is a known person (no identity triage) — the point of this
		// fixture is the layout, not the assignment flow.
		ID:           "99999999-9999-4999-8999-999999999999",
		Title:        "All-hands: Q3 kickoff",
		CreatedAt:    seedAnchor.Add(24 * time.Hour),
		ShortSummary: "Q3 kickoff across eng, mobile, infra and platform: ship search v2, start the on-prem export beta, harden the billing webhooks, and freeze the mobile feature set before the public beta.",
		Speakers: []seedSpeaker{
			{ID: "spk_alice"},
			{ID: "spk_carol"},
			{ID: "spk_dana"},
			{ID: "spk_marc"},
			{ID: "spk_yuri"},
			{ID: "spk_riya"},
		},
		Segments: []seedSegment{
			{SpeakerID: "spk_alice", Start: 0, End: 12, Text: "Welcome to the Q3 kickoff — let's set the cross-team priorities for the quarter."},
			{SpeakerID: "spk_carol", Start: 12, End: 28, Text: "Search v2 lands first; the index migration is staged behind a maintenance window."},
			{SpeakerID: "spk_dana", Start: 28, End: 42, Text: "From the customer side, Acme wants the on-prem export beta the moment SSO ships."},
			{SpeakerID: "spk_marc", Start: 42, End: 56, Text: "Mobile is freezing the feature set two weeks before the public beta — no new scope after that."},
			{SpeakerID: "spk_yuri", Start: 56, End: 72, Text: "Routing rewrite is the long pole; I'd pair {spk_riya} on the migration so we don't bus-factor it."},
			{SpeakerID: "spk_riya", Start: 72, End: 86, Text: "Happy to. I'll also keep the Grafana POC warm as leverage on the Datadog renewal."},
			{SpeakerID: "spk_alice", Start: 86, End: 100, Text: "Good. {spk_carol}, you own search v2 ship; {spk_marc}, you own the mobile freeze."},
			{SpeakerID: "spk_dana", Start: 100, End: 116, Text: "I'll line up Acme as the first on-prem export design partner once the beta opens."},
			{SpeakerID: "spk_marc", Start: 116, End: 130, Text: "One risk: the offline-sync path still isn't load-tested on slow networks."},
			{SpeakerID: "spk_carol", Start: 130, End: 146, Text: "And the search rebuild wipes the index until reindex jobs finish — we need a comms plan."},
			{SpeakerID: "spk_yuri", Start: 146, End: 160, Text: "I'll draft the routing migration plan and circulate it before the next sync."},
			{SpeakerID: "spk_riya", Start: 160, End: 174, Text: "I'll size the Grafana migration so finance has a real walk-away number for Datadog."},
		},
		Decisions: []seedDecision{
			{Text: "Ship search v2 first, behind a staged index-migration window", SegmentRefs: []int{2, 7}},
			{Text: "Freeze the mobile feature set two weeks before public beta", SegmentRefs: []int{4, 7}},
			{Text: "Open the on-prem export beta with Acme as first design partner", SegmentRefs: []int{3, 8}},
		},
		Actions: []seedAction{
			{Text: "Own search v2 ship", Owner: "spk_carol", SegmentRefs: []int{7}},
			{Text: "Own the mobile feature freeze", Owner: "spk_marc", SegmentRefs: []int{7}},
			{Text: "Draft the routing migration plan", Owner: "spk_yuri", SegmentRefs: []int{11}},
			{Text: "Size the Grafana migration as Datadog walk-away leverage", Owner: "spk_riya", SegmentRefs: []int{12}},
			{Text: "Line up Acme for the on-prem export beta", Owner: "spk_dana", SegmentRefs: []int{8}},
		},
		Risks: []seedRisk{
			{Text: "Offline-sync path is not load-tested on slow networks", SegmentRefs: []int{9}},
			{Text: "Search rebuild wipes the index until reindex jobs finish", SegmentRefs: []int{10}},
		},
		Questions: []seedQuestion{
			{Text: "Who owns the user comms while the search index rebuilds?", SegmentRefs: []int{10}},
			{Text: "Can the routing rewrite and search v2 migration share a maintenance window?", SegmentRefs: []int{2, 5}},
		},
	},
}

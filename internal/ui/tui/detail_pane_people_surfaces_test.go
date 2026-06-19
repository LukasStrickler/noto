package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Person rendering must be wired into EVERY surface that names a speaker, not
// just the ones tested in isolation. This locks the contract end to end: every
// place a person can appear resolves the @S<n> token to a name (or a clearly
// marked ~guess?, or the anonymous token) — and no surface ever leaks a raw
// "@S1" the model was fed. If a new person-bearing surface is added without
// routing through renderPeople/resolveSpeaker, the leak check here trips.
func TestPersonRenderingAcrossAllSurfaces(t *testing.T) {
	d := peopleFixturePane() // A confident, B likely, C unknown
	d.id_ = "m1"
	// Tokenized prose in every field, so each surface has a token to resolve.
	d.transcript.Segments = []notoapi.TranscriptSegment{
		{ID: "s1", SpeakerID: "A", StartSec: 0, EndSec: 10, Text: "@S2 will follow up"},
		{ID: "s2", SpeakerID: "B", StartSec: 10, EndSec: 18, Text: "thanks @S1"},
		{ID: "s3", SpeakerID: "C", StartSec: 18, EndSec: 24, Text: "noted"},
	}
	d.speakers = computeSpeakerStats(d.transcript) // recompute: segment times changed
	d.summary = notoapi.Summary{
		MeetingID:     "m1",
		Markdown:      "@S1 and @S2 met while @S3 listened.",
		Decisions:     []notoapi.SummaryItem{{Text: "@S1 ships v2", SegmentRefs: []string{"s1"}}},
		Risks:         []notoapi.SummaryItem{{Text: "@S2 may slip", SegmentRefs: []string{"s2"}}},
		OpenQuestions: []notoapi.SummaryItem{{Text: "who pages @S3?", SegmentRefs: []string{"s3"}}},
		ActionItems:   []notoapi.ActionItem{{Text: "draft the RFC", Owner: "@S1", SegmentRefs: []string{"s1"}}},
	}

	ctx := testScreenCtx()
	s := ctx.styles
	const w = 200

	surfaces := map[string]string{
		"summary":        d.renderSummary(ctx, w, 40),
		"actions":        d.renderItemsList(ctx, "Action items", actionItemsToSummary(d.summary.ActionItems), w, 40, s.Action),
		"decisions":      d.renderItemsList(ctx, "Decisions", d.summary.Decisions, w, 40, s.Decision),
		"risks":          d.renderItemsList(ctx, "Risks", d.summary.Risks, w, 40, s.Risk),
		"questions":      d.renderItemsList(ctx, "Open questions", d.summary.OpenQuestions, w, 40, s.Question),
		"transcript":     d.renderTranscript(ctx, w, 40),
		"header roster":  d.renderSpeakerOverviewRows(s, w),
		"speakers list":  d.renderSpeakerList(ctx, w),
		"speaker detail": d.renderSpeakerDetail(s, w),
	}

	// No surface may leak the raw token the model was handed.
	for label, out := range surfaces {
		if got := ansi.Strip(out); strings.Contains(got, "@S") {
			t.Errorf("%s leaked a raw @S token:\n%s", label, got)
		}
	}

	// The three tiers must all be visible where a person is named: a confident
	// person by name, a likely one as ~name?, an unknown one as the token.
	for _, surface := range []string{"summary", "speakers list", "header roster"} {
		got := ansi.Strip(surfaces[surface])
		for _, want := range []string{"Alice Nguyen", "~Carol Reyes?", "Speaker C"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %q:\n%s", surface, want, got)
			}
		}
	}

	// Per-field token resolution: each items tab and the action owner resolve too.
	checks := []struct{ surface, want string }{
		{"decisions", "Alice Nguyen ships v2"},
		{"risks", "~Carol Reyes? may slip"},
		{"questions", "who pages Speaker C?"},
		{"actions", "(owner: Alice Nguyen)"},
	}
	for _, c := range checks {
		if got := ansi.Strip(surfaces[c.surface]); !strings.Contains(got, c.want) {
			t.Errorf("%s missing %q:\n%s", c.surface, c.want, got)
		}
	}

	// The transcript attributes segments to resolved people, not raw labels.
	tr := ansi.Strip(surfaces["transcript"])
	for _, want := range []string{"Alice Nguyen", "~Carol Reyes?", "Speaker C"} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript missing speaker %q:\n%s", want, tr)
		}
	}
}

// Even before talk-time stats exist (speakers declared, no segments yet) the
// header roster resolves through the same path: a confirmed person shows by
// name, never the bare diarizer id or an unresolved label.
func TestAttendeeStripResolvesBeforeStats(t *testing.T) {
	d := newDetailPane()
	d.id_ = "m1"
	d.transcript = notoapi.Transcript{
		Speakers: []notoapi.Speaker{
			{ID: "A", Label: "Speaker A"},
			{ID: "B", Label: "Speaker B"},
		},
		// No segments → computeSpeakerStats yields nothing → fallback path.
	}
	carol := "p2"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "manual", ProfileName: "Alice Nguyen"},
		"B": {MeetingSpeakerID: "B", MatchStatus: "pending", ProfileID: &carol, ProfileName: "Carol Reyes"},
	}

	got := ansi.Strip(d.renderAttendeeStrip(testScreenCtx().styles, 120))
	for _, want := range []string{"Alice Nguyen", "~Carol Reyes?"} {
		if !strings.Contains(got, want) {
			t.Errorf("loading-state roster missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "@S") {
		t.Errorf("loading-state roster leaked a token:\n%s", got)
	}
}

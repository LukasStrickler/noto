package tui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
	"github.com/lukasstrickler/noto/internal/ui/tui/theme"
)

// peopleFixturePane: speaker A confirmed (manual), B a likely guess (pending),
// C unknown (unmatched) — the three confidence tiers.
func peopleFixturePane() *detailPane {
	d := newDetailPane()
	d.transcript = notoapi.Transcript{
		Speakers: []notoapi.Speaker{
			{ID: "A", Label: "Speaker A"},
			{ID: "B", Label: "Speaker B"},
			{ID: "C", Label: "Speaker C"},
		},
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", SpeakerID: "A", Speaker: "Speaker A", StartSec: 0, EndSec: 10, Text: "a"},
			{ID: "s2", SpeakerID: "B", Speaker: "Speaker B", StartSec: 10, EndSec: 18, Text: "b"},
			{ID: "s3", SpeakerID: "C", Speaker: "Speaker C", StartSec: 18, EndSec: 24, Text: "c"},
		},
	}
	d.speakers = computeSpeakerStats(d.transcript)
	carol := "p3"
	d.mappings = map[string]notoapi.MeetingSpeakerMapping{
		"A": {MeetingSpeakerID: "A", MatchStatus: "manual", ProfileName: "Alice Nguyen"},
		"B": {MeetingSpeakerID: "B", MatchStatus: "pending", ProfileID: &carol, ProfileName: "Carol Reyes"},
		"C": {MeetingSpeakerID: "C", MatchStatus: "unmatched"},
	}
	return d
}

// renderPeople is the single place model prose becomes named, colored people: a
// confirmed speaker resolves to their name, a likely one to a clearly-marked
// ~guess?, an unknown one stays the anonymous token — and a token is never left
// unresolved. The PRIMARY handle is the fixed @S<n> token (reliable, can't be
// paraphrased); the bare label is a fallback for legacy output.
func TestRenderPeopleResolvesTokens(t *testing.T) {
	d := peopleFixturePane()
	s := theme.NewStyles()
	want := "Alice Nguyen and ~Carol Reyes? met; Speaker C took notes."
	cases := map[string]string{
		"@S tokens":     "@S1 and @S2 met; @S3 took notes.",
		"legacy labels": "Speaker A and Speaker B met; Speaker C took notes.",
	}
	for name, in := range cases {
		if got := ansi.Strip(d.renderPeople(s, in)); got != want {
			t.Errorf("%s: renderPeople =\n  %q\nwant\n  %q", name, got, want)
		}
	}
	// A possessive @S token must still resolve cleanly.
	if got := ansi.Strip(d.renderPeople(s, "Reviewed @S1's design.")); got != "Reviewed Alice Nguyen's design." {
		t.Errorf("possessive token = %q, want %q", got, "Reviewed Alice Nguyen's design.")
	}
}

func TestResolveSpeakerTiers(t *testing.T) {
	d := peopleFixturePane()
	cases := []struct {
		id   string
		name string
		tier speakerTier
	}{
		{"A", "Alice Nguyen", tierConfident},
		{"B", "Carol Reyes", tierLikely},
		{"C", "Speaker C", tierUnknown},
	}
	for _, c := range cases {
		name, tier := d.resolveSpeaker(c.id)
		if name != c.name || tier != c.tier {
			t.Errorf("resolveSpeaker(%q) = (%q, %v), want (%q, %v)", c.id, name, tier, c.name, c.tier)
		}
	}
}

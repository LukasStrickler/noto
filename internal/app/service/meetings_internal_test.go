package service

import (
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/speakerstore"
)

// TestResolveAgentSpeakerNames pins the agent-handoff name resolution: an
// AUTO-identified speaker (name only in its mapping→profile, not the transcript)
// must resolve to the profile name, since the system treats "auto" as resolved;
// an explicit transcript DisplayName still wins; an unresolved/pending mapping is
// NOT promoted; and a speaker with nothing falls back to label then id.
func TestResolveAgentSpeakerNames(t *testing.T) {
	pid := func(s string) *string { return &s }
	tr := &artifacts.Transcript{Speakers: []artifacts.Speaker{
		{ID: "spk_0", DisplayName: "Bob"}, // explicitly named
		{ID: "spk_1", Label: "Speaker 2"}, // auto-identified below
		{ID: "spk_2", Label: "Speaker 3"}, // pending guess — must NOT resolve
		{ID: "spk_3"},                     // nothing at all
	}}
	mappings := []speakerstore.MeetingSpeakerMapping{
		{MeetingSpeakerID: "spk_1", ProfileID: pid("p-maya"), MatchStatus: "auto"},
		{MeetingSpeakerID: "spk_2", ProfileID: pid("p-guess"), MatchStatus: "pending"},
	}
	profileName := map[string]string{"p-maya": "Maya", "p-guess": "Probably Sam"}

	got := resolveAgentSpeakerNames(tr, mappings, profileName)
	want := map[string]string{
		"spk_0": "Bob",       // explicit name wins
		"spk_1": "Maya",      // auto match resolves from the profile
		"spk_2": "Speaker 3", // pending is not promoted → falls back to label
		"spk_3": "spk_3",     // nothing → id
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("speaker %s resolved to %q, want %q", id, got[id], w)
		}
	}
}

// TestMergeProfileFields pins the in-memory merge: target metadata wins and source
// fills gaps; voiceprints combine count-weighted; the survivor takes the LATER
// LastSeenAt (was stale before — the merge didn't touch it); and an empty-voiceprint
// target adopts source's vector AND its embedding model together.
func TestMergeProfileFields(t *testing.T) {
	older := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("later last-seen and adopted model on empty-voiceprint target", func(t *testing.T) {
		target := speakerstore.SpeakerProfile{ID: "t", DisplayName: "Alice", LastSeenAt: &older}
		source := speakerstore.SpeakerProfile{
			ID: "s", DisplayName: "Bob", Pronouns: "they/them",
			EmbeddingVector: []float64{1, 0, 0}, EmbeddingDim: 3, EmbeddingCount: 4,
			EmbeddingModel: "ecapa", LastSeenAt: &newer,
		}
		got := mergeProfileFields(target, source)
		if got.DisplayName != "Alice" {
			t.Errorf("target name must win: got %q", got.DisplayName)
		}
		if got.Pronouns != "they/them" {
			t.Errorf("source must fill the pronouns gap: got %q", got.Pronouns)
		}
		if got.LastSeenAt == nil || !got.LastSeenAt.Equal(newer) {
			t.Errorf("survivor must take the later last-seen %v, got %v", newer, got.LastSeenAt)
		}
		if got.EmbeddingModel != "ecapa" || len(got.EmbeddingVector) != 3 || got.EmbeddingCount != 4 {
			t.Errorf("empty-voiceprint target must adopt source's vector+model+count, got %+v", got)
		}
	})

	t.Run("count-weighted voiceprint and later-seen keeps target's newer stamp", func(t *testing.T) {
		target := speakerstore.SpeakerProfile{
			ID: "t", EmbeddingVector: []float64{1, 0, 0}, EmbeddingDim: 3, EmbeddingCount: 20,
			EmbeddingModel: "ecapa", LastSeenAt: &newer,
		}
		source := speakerstore.SpeakerProfile{
			ID: "s", EmbeddingVector: []float64{0, 1, 0}, EmbeddingDim: 3, EmbeddingCount: 1,
			EmbeddingModel: "ecapa", LastSeenAt: &older,
		}
		got := mergeProfileFields(target, source)
		if got.EmbeddingCount != 21 {
			t.Errorf("counts must add: got %d want 21", got.EmbeddingCount)
		}
		// A 20:1 weight barely moves the centroid off target's axis.
		if got.EmbeddingVector[0] < 0.9 {
			t.Errorf("20:1 merge should stay near target's axis, got %v", got.EmbeddingVector)
		}
		if got.LastSeenAt == nil || !got.LastSeenAt.Equal(newer) {
			t.Errorf("survivor must keep the newer last-seen %v, got %v", newer, got.LastSeenAt)
		}
	})
}

// TestFirstSummaryLine pins that the GetSummary markdown fallback skips the
// "# {title}" / "## Section" headings renderSummaryMD emits and returns the first
// PROSE line — so a meeting whose structured short-summary is missing shows the
// summary text, not its own title heading.
func TestFirstSummaryLine(t *testing.T) {
	cases := []struct{ name, md, want string }{
		{
			name: "skips title heading",
			md:   "# Weekly Sync\n\nWe agreed to ship v1 by Friday.\n\n## Decisions\n\n1. Ship v1.",
			want: "We agreed to ship v1 by Friday.",
		},
		{
			name: "leading blank lines then heading then prose",
			md:   "\n\n#   Spaced Title\n\nThe real summary.\n",
			want: "The real summary.",
		},
		{
			name: "no prose, only headings and list → first non-heading line",
			md:   "# Title\n\n## Decisions\n\n1. Do the thing.",
			want: "1. Do the thing.",
		},
		{
			name: "prose first (no heading) is returned verbatim",
			md:   "Just a plain summary.",
			want: "Just a plain summary.",
		},
		{
			name: "only headings → empty",
			md:   "# Title\n\n## Decisions\n## Risks",
			want: "",
		},
		{name: "empty", md: "", want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstSummaryLine(c.md); got != c.want {
				t.Errorf("firstSummaryLine(%q) = %q, want %q", c.md, got, c.want)
			}
		})
	}
}

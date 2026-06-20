package merge_test

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/speakers"
	"github.com/lukasstrickler/noto/internal/platform/providers/merge"
)

// TestOverlapRefinementFlow pins the ADR-0007 contract between the two pieces the
// refinement pass composes: speakers.AssignStreams (which separated stream is which
// speaker, acoustically) → merge.MergeOverlapStreams (splice both into the
// transcript). This is the exact data flow the gated orchestrator wires once it has
// the separated audio: separate → embed → AssignStreams → MergeOverlapStreams.
func TestOverlapRefinementFlow(t *testing.T) {
	// The meeting's two speakers, by their reference embeddings (centroids).
	candidates := []speakers.SpeakerCandidate{
		{ID: "spk_A", Embedding: speakers.Embedding{1, 0}},
		{ID: "spk_B", Embedding: speakers.Embedding{0, 1}},
	}
	// Separation produced two streams for an overlap region; their ECAPA embeddings
	// (stream 0 acoustically A, stream 1 acoustically B) + the words decoded from each.
	streamEmb := []speakers.Embedding{{0.95, 0.05}, {0.05, 0.95}}
	streamWords := [][]artifacts.Word{
		{{ID: "s0w0", Text: "hello", StartSeconds: 1.1, EndSeconds: 1.4}},
		{{ID: "s1w0", Text: "world", StartSeconds: 1.2, EndSeconds: 1.5}},
	}

	// Step 1 — assign each separated stream to a speaker by acoustic identity.
	assigned := speakers.AssignStreams(streamEmb, candidates)
	if assigned[0] != "spk_A" || assigned[1] != "spk_B" {
		t.Fatalf("AssignStreams = %v; want [spk_A spk_B]", assigned)
	}

	// Step 2 — build the overlap region from the assignment + decoded words.
	region := merge.OverlapRegion{StartSeconds: 1.0, EndSeconds: 2.0}
	for i, spk := range assigned {
		region.Streams = append(region.Streams, merge.OverlapStream{SpeakerID: spk, Words: streamWords[i]})
	}

	// The base single-channel transcript garbled the overlap into one speaker.
	base := &artifacts.Transcript{
		MeetingID: "m1",
		Words: []artifacts.Word{
			{ID: "b0", Text: "intro", StartSeconds: 0.0, EndSeconds: 0.5, SpeakerID: "spk_A"},
			{ID: "b1", Text: "garble", StartSeconds: 1.0, EndSeconds: 1.8, SpeakerID: "spk_A"},
		},
	}

	// Step 3 — merge both speakers back in.
	v2 := merge.MergeOverlapStreams(base, []merge.OverlapRegion{region})

	got := map[string]string{}
	for _, w := range v2.Words {
		got[w.Text] = w.SpeakerID
	}
	if _, gone := got["garble"]; gone {
		t.Error("garbled mixed word should be replaced inside the overlap region")
	}
	if got["hello"] != "spk_A" || got["world"] != "spk_B" {
		t.Errorf("both speakers must be recovered, attributed: hello=%q world=%q", got["hello"], got["world"])
	}
	if got["intro"] != "spk_A" {
		t.Error("words outside the region untouched")
	}
	spk := map[string]bool{}
	for _, s := range v2.Speakers {
		spk[s.ID] = true
	}
	if !spk["spk_A"] || !spk["spk_B"] {
		t.Errorf("v2 speakers = %v; want both", v2.Speakers)
	}
}

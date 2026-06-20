package merge

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

func wordsText(ws []artifacts.Word) map[string]string { // text -> speaker
	m := map[string]string{}
	for _, w := range ws {
		m[w.Text] = w.SpeakerID
	}
	return m
}

// TestMergeOverlapStreams: the mixed (garbled) word inside an overlap region is
// replaced by BOTH separated speakers' words, attributed; words outside the region
// are untouched; both speakers appear.
func TestMergeOverlapStreams(t *testing.T) {
	base := &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     "m1",
		Words: []artifacts.Word{
			{ID: "b0", Text: "hello", StartSeconds: 0.0, EndSeconds: 0.5, SpeakerID: "A"},
			{ID: "b1", Text: "garble", StartSeconds: 1.0, EndSeconds: 1.8, SpeakerID: "A"}, // inside overlap
			{ID: "b2", Text: "bye", StartSeconds: 3.0, EndSeconds: 3.5, SpeakerID: "A"},
		},
	}
	regions := []OverlapRegion{{
		StartSeconds: 1.0, EndSeconds: 2.0,
		Streams: []OverlapStream{
			{SpeakerID: "A", Words: []artifacts.Word{{ID: "a0", Text: "world", StartSeconds: 1.1, EndSeconds: 1.5}}},
			{SpeakerID: "B", Words: []artifacts.Word{{ID: "sb0", Text: "yeah", StartSeconds: 1.3, EndSeconds: 1.7}}},
		},
	}}

	got := MergeOverlapStreams(base, regions)
	tx := wordsText(got.Words)

	if _, ok := tx["garble"]; ok {
		t.Error("garbled mixed word should be dropped inside the overlap region")
	}
	if tx["world"] != "A" {
		t.Errorf("separated stream A word 'world' = speaker %q; want A", tx["world"])
	}
	if tx["yeah"] != "B" {
		t.Errorf("separated stream B word 'yeah' = speaker %q; want B — the 2nd speaker must be recovered", tx["yeah"])
	}
	if tx["hello"] != "A" || tx["bye"] != "A" {
		t.Error("words outside the overlap region must be untouched")
	}

	// both speakers present
	spk := map[string]bool{}
	for _, s := range got.Speakers {
		spk[s.ID] = true
	}
	if !spk["A"] || !spk["B"] {
		t.Errorf("speakers = %v; want both A and B", got.Speakers)
	}

	// words are time-ordered (resegment relies on it)
	for i := 1; i < len(got.Words); i++ {
		if got.Words[i].StartSeconds < got.Words[i-1].StartSeconds {
			t.Fatalf("words not time-ordered at %d", i)
		}
	}

	// base is not mutated
	if base.Words[1].Text != "garble" {
		t.Error("base transcript was mutated")
	}
}

// TestMergeOverlapStreamsNoRegions is a no-op passthrough (re-segmented) when there
// are no overlap regions.
func TestMergeOverlapStreamsNoRegions(t *testing.T) {
	base := &artifacts.Transcript{
		Words: []artifacts.Word{
			{ID: "b0", Text: "hello", StartSeconds: 0.0, EndSeconds: 0.5, SpeakerID: "A"},
			{ID: "b1", Text: "there", StartSeconds: 0.6, EndSeconds: 1.0, SpeakerID: "A"},
		},
	}
	got := MergeOverlapStreams(base, nil)
	if len(got.Words) != 2 || got.Words[0].Text != "hello" || got.Words[1].Text != "there" {
		t.Errorf("no-region merge should preserve words, got %d", len(got.Words))
	}
}

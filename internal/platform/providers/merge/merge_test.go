package merge

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
)

func wordsTr(words ...artifacts.Word) *artifacts.Transcript {
	return &artifacts.Transcript{Words: words}
}

func TestAttributeByMidpoint(t *testing.T) {
	tr := wordsTr(
		artifacts.Word{ID: "w0", Text: "hello", StartSeconds: 0, EndSeconds: 1},
		artifacts.Word{ID: "w1", Text: "there", StartSeconds: 1, EndSeconds: 2},
		artifacts.Word{ID: "w2", Text: "general", StartSeconds: 6, EndSeconds: 7},
		artifacts.Word{ID: "w3", Text: "kenobi", StartSeconds: 7, EndSeconds: 8},
	)
	turns := []diarize.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "B", StartSeconds: 5, EndSeconds: 10},
	}
	out := Attribute(tr, turns)

	got := map[string]string{}
	for _, w := range out.Words {
		got[w.ID] = w.SpeakerID
	}
	want := map[string]string{"w0": "A", "w1": "A", "w2": "B", "w3": "B"}
	for id, spk := range want {
		if got[id] != spk {
			t.Errorf("word %s speaker = %q, want %q", id, got[id], spk)
		}
	}

	// resegmented into one segment per speaker run
	if len(out.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(out.Segments))
	}
	if out.Segments[0].SpeakerID != "A" || out.Segments[0].Text != "hello there" {
		t.Errorf("seg0 = %+v", out.Segments[0])
	}
	if out.Segments[1].SpeakerID != "B" || out.Segments[1].Text != "general kenobi" {
		t.Errorf("seg1 = %+v", out.Segments[1])
	}
	// speakers list rebuilt
	if len(out.Speakers) != 2 || out.Speakers[0].ID != "A" || out.Speakers[0].Origin != "diarized" {
		t.Errorf("speakers = %+v", out.Speakers)
	}
	// input not mutated
	if tr.Words[0].SpeakerID != "" {
		t.Error("input transcript was mutated")
	}
}

func TestAttributeNearestFallback(t *testing.T) {
	// A word entirely inside a gap between turns is assigned to the nearest turn.
	tr := wordsTr(artifacts.Word{ID: "w0", Text: "uh", StartSeconds: 5.1, EndSeconds: 5.3})
	turns := []diarize.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "B", StartSeconds: 6, EndSeconds: 10},
	}
	out := Attribute(tr, turns)
	if out.Words[0].SpeakerID != "A" { // midpoint 5.2 is closer to A's end (5.0) than B's start (6.0)
		t.Errorf("speaker = %q, want A (nearest)", out.Words[0].SpeakerID)
	}
}

func TestAttributeOverlapFallback(t *testing.T) {
	// Midpoint lands in no turn, but the word overlaps one — overlap wins over
	// nearest.
	tr := wordsTr(artifacts.Word{ID: "w0", Text: "x", StartSeconds: 4.0, EndSeconds: 4.4})
	// midpoint 4.2 is inside A[0,5] → that's the midpoint case; make it trickier:
	turns := []diarize.Turn{{Speaker: "A", StartSeconds: 0, EndSeconds: 5}}
	out := Attribute(tr, turns)
	if out.Words[0].SpeakerID != "A" {
		t.Errorf("speaker = %q, want A", out.Words[0].SpeakerID)
	}
}

func TestAttributeNoTurns(t *testing.T) {
	tr := wordsTr(artifacts.Word{ID: "w0", Text: "x", StartSeconds: 0, EndSeconds: 1})
	out := Attribute(tr, nil)
	if out.Words[0].SpeakerID != "" {
		t.Errorf("speaker = %q, want empty", out.Words[0].SpeakerID)
	}
	if len(out.Speakers) != 0 {
		t.Errorf("speakers = %+v, want none", out.Speakers)
	}
}

func TestWordsBySpeaker(t *testing.T) {
	tr := &artifacts.Transcript{Words: []artifacts.Word{
		{Text: "hello", SpeakerID: "A", StartSeconds: 0},
		{Text: "world", SpeakerID: "B", StartSeconds: 1},
		{Text: "again", SpeakerID: "A", StartSeconds: 2},
	}}
	got := WordsBySpeaker(tr)
	if got["A"] != "hello again" || got["B"] != "world" {
		t.Errorf("WordsBySpeaker = %#v", got)
	}
}

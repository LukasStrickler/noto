package speakers

import "testing"

func TestAssignStreamsDistinct(t *testing.T) {
	a := SpeakerCandidate{ID: "A", Embedding: Embedding{1, 0}}
	b := SpeakerCandidate{ID: "B", Embedding: Embedding{0, 1}}

	// s0 clearly A-like, s1 clearly B-like → s0→A, s1→B.
	got := AssignStreams([]Embedding{{0.9, 0.1}, {0.1, 0.9}}, []SpeakerCandidate{a, b})
	if got[0] != "A" || got[1] != "B" {
		t.Fatalf("got %v; want [A B]", got)
	}
}

func TestAssignStreamsForcesOneToOne(t *testing.T) {
	a := SpeakerCandidate{ID: "A", Embedding: Embedding{1, 0}}
	b := SpeakerCandidate{ID: "B", Embedding: Embedding{0, 1}}

	// Both streams lean A, but the assignment must give them DISTINCT speakers
	// (the more-A-like one keeps A, the other is pushed to B) — not both → A.
	got := AssignStreams([]Embedding{{0.99, 0.10}, {0.80, 0.30}}, []SpeakerCandidate{a, b})
	if got[0] == got[1] {
		t.Fatalf("streams assigned to the SAME speaker %v; one-to-one expected", got)
	}
	if got[0] != "A" {
		t.Errorf("the more-A-like stream should keep A; got %v", got)
	}
}

func TestAssignStreamsGreedyOnUnequalCounts(t *testing.T) {
	a := SpeakerCandidate{ID: "A", Embedding: Embedding{1, 0}}
	b := SpeakerCandidate{ID: "B", Embedding: Embedding{0, 1}}
	c := SpeakerCandidate{ID: "C", Embedding: Embedding{1, 1}}

	// 2 streams, 3 candidates → greedy best per stream (no one-to-one constraint).
	got := AssignStreams([]Embedding{{1, 0}, {0, 1}}, []SpeakerCandidate{a, b, c})
	if got[0] != "A" || got[1] != "B" {
		t.Errorf("got %v; want [A B]", got)
	}
}

func TestAssignStreamsNoCandidatesOrEmpty(t *testing.T) {
	if got := AssignStreams([]Embedding{{1, 0}}, nil); got[0] != "" {
		t.Errorf("no candidates should yield empty id, got %q", got[0])
	}
	if got := AssignStreams(nil, []SpeakerCandidate{{ID: "A", Embedding: Embedding{1, 0}}}); len(got) != 0 {
		t.Errorf("no streams should yield empty result, got %v", got)
	}
}

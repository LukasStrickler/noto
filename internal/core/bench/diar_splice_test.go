package bench

import "testing"

func TestSpliceTurns_ReplacesDiarizationWithinSpan(t *testing.T) {
	// One speaker A holds [0,10); a re-diarization of [4,6) finds two speakers there.
	base := []SpeakerSpan{{Speaker: "A", Start: 0, End: 10}}
	repl := []SpeakerSpan{
		{Speaker: "A", Start: 4, End: 5},
		{Speaker: "B", Start: 5, End: 6},
	}
	out := SpliceTurns(base, 4, 6, repl)

	// A's turn splits into a head [0,4) and a tail [6,10); the replacement fills [4,6).
	want := []SpeakerSpan{
		{Speaker: "A", Start: 0, End: 4},
		{Speaker: "A", Start: 4, End: 5},
		{Speaker: "B", Start: 5, End: 6},
		{Speaker: "A", Start: 6, End: 10},
	}
	if len(out) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(out), len(want), out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("turn %d = %+v, want %+v", i, out[i], want[i])
		}
	}
}

func TestSpliceTurns_DropsTurnsEntirelyInsideSpan(t *testing.T) {
	// A turn entirely inside the span is removed (the re-diarization replaces it);
	// turns entirely outside survive unchanged.
	base := []SpeakerSpan{
		{Speaker: "A", Start: 0, End: 2},  // before — survives
		{Speaker: "B", Start: 4, End: 6},  // inside [3,7) — dropped
		{Speaker: "C", Start: 8, End: 10}, // after — survives
	}
	repl := []SpeakerSpan{{Speaker: "D", Start: 3, End: 7}}
	out := SpliceTurns(base, 3, 7, repl)

	want := []SpeakerSpan{
		{Speaker: "A", Start: 0, End: 2},
		{Speaker: "D", Start: 3, End: 7},
		{Speaker: "C", Start: 8, End: 10},
	}
	if len(out) != len(want) {
		t.Fatalf("got %d turns, want %d: %+v", len(out), len(want), out)
	}
	for i := range want {
		if out[i] != want[i] {
			t.Errorf("turn %d = %+v, want %+v", i, out[i], want[i])
		}
	}
}

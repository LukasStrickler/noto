package bench_test

import (
	"testing"

	bench "github.com/lukasstrickler/noto/internal/core/bench"
)

func TestSpliceSpan_ReplacesByMidpointAndSorts(t *testing.T) {
	base := []bench.TimedWord{
		{Text: "the", Start: 0, End: 1},
		{Text: "kwik", Start: 1, End: 2},  // wrong, in span
		{Text: "braun", Start: 2, End: 3}, // wrong, in span
		{Text: "fox", Start: 3, End: 4},
	}
	// Re-decode of [1,3) → two corrected words.
	repl := []bench.TimedWord{
		{Text: "quick", Start: 1, End: 2},
		{Text: "brown", Start: 2, End: 3},
	}
	out := bench.SpliceSpan(base, 1, 3, repl)
	got := make([]string, len(out))
	for i, w := range out {
		got[i] = w.Text
	}
	want := []string{"the", "quick", "brown", "fox"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("word[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestSpliceSpan_BoundaryWordByMidpointNotRemoved(t *testing.T) {
	// A word at [2.5,3.5] has midpoint 3.0, which is NOT < 3 → stays. A naive overlap
	// test would wrongly remove it.
	base := []bench.TimedWord{
		{Text: "in", Start: 1.0, End: 2.0},   // mid 1.5 in [1,3) → removed
		{Text: "edge", Start: 2.5, End: 3.5}, // mid 3.0, not in [1,3) → kept
	}
	out := bench.SpliceSpan(base, 1, 3, []bench.TimedWord{{Text: "X", Start: 1.2, End: 1.8}})
	texts := []string{}
	for _, w := range out {
		texts = append(texts, w.Text)
	}
	if len(out) != 2 || texts[0] != "X" || texts[1] != "edge" {
		t.Errorf("boundary word should survive: got %v, want [X edge]", texts)
	}
}

func TestWordsInSpan(t *testing.T) {
	words := []bench.TimedWord{
		{Text: "a", Start: 0, End: 1},
		{Text: "b", Start: 1, End: 2},
		{Text: "c", Start: 5, End: 6},
	}
	in := bench.WordsInSpan(words, 0.6, 3) // "a" mid 0.5 excluded; "b" mid 1.5 included; "c" mid 5.5 excluded
	if len(in) != 1 || in[0].Text != "b" {
		t.Errorf("WordsInSpan = %v, want just [b]", in)
	}
}

func TestOutcomeFromDeltas_AcceptWashRegress(t *testing.T) {
	span := bench.RepairSpan{StartSec: 0, EndSec: 2}
	// Clear improvement on WER, entity flat → accepted.
	a := bench.OutcomeFromDeltas(span, bench.MethodAlternateDecode, 0.001, -0.05, 0)
	if !a.Accepted || a.Negative {
		t.Errorf("net improvement must be accepted: %+v", a)
	}
	// Clear regression → negative.
	n := bench.OutcomeFromDeltas(span, bench.MethodAlternateDecode, 0.001, +0.03, 0)
	if n.Accepted || !n.Negative {
		t.Errorf("regression must be negative: %+v", n)
	}
	// Below the threshold on both axes → a wash, neither accepted nor negative.
	w := bench.OutcomeFromDeltas(span, bench.MethodAlternateDecode, 0.001, -0.0001, 0.0001)
	if w.Accepted || w.Negative {
		t.Errorf("sub-threshold change must be a wash: %+v", w)
	}
	// Mixed: WER better but entity worse → wash (improves AND regresses) — not counted.
	m := bench.OutcomeFromDeltas(span, bench.MethodAlternateDecode, 0.001, -0.05, +0.05)
	if m.Accepted || m.Negative {
		t.Errorf("mixed result must be a wash, not an accepted/negative: %+v", m)
	}
}

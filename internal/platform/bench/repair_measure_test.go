package bench

import (
	"math"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// ref: speaker A says "the quick brown fox" over 0..4s.
func spliceRef() dataset.Meeting {
	return dataset.Meeting{
		ID: "M",
		Words: []dataset.Word{
			{Speaker: "A", StartSeconds: 0, EndSeconds: 1, Text: "the"},
			{Speaker: "A", StartSeconds: 1, EndSeconds: 2, Text: "quick"},
			{Speaker: "A", StartSeconds: 2, EndSeconds: 3, Text: "brown"},
			{Speaker: "A", StartSeconds: 3, EndSeconds: 4, Text: "fox"},
		},
		Turns: []dataset.Turn{{Speaker: "A", StartSeconds: 0, EndSeconds: 4}},
	}
}

func TestMeasureSplice_FixingErrorsImprovesWER(t *testing.T) {
	ref := spliceRef()
	// Baseline mis-hears the two middle words.
	baseline := []HypWord{
		{Text: "the", Start: 0, End: 1, Speaker: "A"},
		{Text: "kwik", Start: 1, End: 2, Speaker: "A"},
		{Text: "braun", Start: 2, End: 3, Speaker: "A"},
		{Text: "fox", Start: 3, End: 4, Speaker: "A"},
	}
	// A re-decode of [1,3) gets them right.
	repl := []HypWord{
		{Text: "quick", Start: 1, End: 2, Speaker: "A"},
		{Text: "brown", Start: 2, End: 3, Speaker: "A"},
	}
	m := MeasureSplice(ref, baseline, 1, 3, repl)

	if m.WERBefore <= 0 {
		t.Fatalf("baseline should have WER > 0, got %v", m.WERBefore)
	}
	if m.WERAfter != 0 {
		t.Errorf("fixing both errors should drive WER to 0, got %v", m.WERAfter)
	}
	if m.WERDelta >= 0 {
		t.Errorf("a real fix must have negative WER delta, got %v", m.WERDelta)
	}
	// The accept/reject rule should adopt it (improves, regresses nothing).
	out := m.Outcome(corebench.RepairSpan{StartSec: 1, EndSec: 3}, corebench.MethodAlternateDecode, 0.001)
	if !out.Accepted || out.Negative {
		t.Errorf("a benchmark-improving repair must be accepted: %+v", out)
	}
}

func TestMeasureSplice_BreakingCorrectWordsRegresses(t *testing.T) {
	ref := spliceRef()
	// Baseline is perfect.
	baseline := []HypWord{
		{Text: "the", Start: 0, End: 1, Speaker: "A"},
		{Text: "quick", Start: 1, End: 2, Speaker: "A"},
		{Text: "brown", Start: 2, End: 3, Speaker: "A"},
		{Text: "fox", Start: 3, End: 4, Speaker: "A"},
	}
	// A bad re-decode corrupts the two middle words.
	repl := []HypWord{
		{Text: "slow", Start: 1, End: 2, Speaker: "A"},
		{Text: "green", Start: 2, End: 3, Speaker: "A"},
	}
	m := MeasureSplice(ref, baseline, 1, 3, repl)
	if m.WERBefore != 0 {
		t.Fatalf("perfect baseline should have WER 0, got %v", m.WERBefore)
	}
	if m.WERDelta <= 0 {
		t.Errorf("a corrupting repair must have positive WER delta, got %v", m.WERDelta)
	}
	out := m.Outcome(corebench.RepairSpan{StartSec: 1, EndSec: 3}, corebench.MethodAlternateDecode, 0.001)
	if out.Accepted || !out.Negative {
		t.Errorf("a regressing repair must be flagged negative, not accepted: %+v", out)
	}
}

// The local measure registers a single-word fix as a strong delta where the
// whole-transcript measure of the SAME fix in a long meeting rounds to ~0 — the
// sensitivity gap that made the first real run accept nothing.
func TestMeasureSpliceLocal_IsSensitiveWhereWholeTranscriptRoundsToZero(t *testing.T) {
	// A 200-word reference: one wrong baseline word at [10,11).
	ref := dataset.Meeting{ID: "M"}
	baseline := make([]HypWord, 0, 200)
	for i := 0; i < 200; i++ {
		start := float64(i)
		text := "w"
		ref.Words = append(ref.Words, dataset.Word{Speaker: "A", StartSeconds: start, EndSeconds: start + 1, Text: text})
		hypText := text
		if i == 10 {
			hypText = "WRONG"
		}
		baseline = append(baseline, HypWord{Text: hypText, Start: start, End: start + 1, Speaker: "A"})
	}
	repl := []HypWord{{Text: "w", Start: 10, End: 11, Speaker: "A"}}

	local := MeasureSpliceLocal(ref, baseline, 10, 11, repl)
	whole := MeasureSplice(ref, baseline, 10, 11, repl)

	if local.WERDelta != -1 {
		t.Errorf("local: fixing the only word in a 1-word span should be WER delta -1, got %v", local.WERDelta)
	}
	// Whole-transcript: 1 word out of 200 → |delta| = 0.005, 200× weaker than the
	// local signal — and on a 3000-word meeting it rounds below the accept threshold
	// entirely. The local measure is what makes per-span accept work.
	if math.Abs(whole.WERDelta) >= math.Abs(local.WERDelta) {
		t.Errorf("whole-transcript delta (%v) must be far weaker than local (%v)", whole.WERDelta, local.WERDelta)
	}
	if math.Abs(whole.WERDelta) > 0.01 {
		t.Errorf("whole-transcript signal should be tiny (~0.005), got %v", whole.WERDelta)
	}
}

func TestMeasureSplice_NoOpReplacementIsWash(t *testing.T) {
	ref := spliceRef()
	baseline := []HypWord{
		{Text: "the", Start: 0, End: 1, Speaker: "A"},
		{Text: "quick", Start: 1, End: 2, Speaker: "A"},
		{Text: "brown", Start: 2, End: 3, Speaker: "A"},
		{Text: "fox", Start: 3, End: 4, Speaker: "A"},
	}
	// Replacing the span with the identical words changes nothing.
	repl := []HypWord{
		{Text: "quick", Start: 1, End: 2, Speaker: "A"},
		{Text: "brown", Start: 2, End: 3, Speaker: "A"},
	}
	m := MeasureSplice(ref, baseline, 1, 3, repl)
	if m.WERDelta != 0 || m.CpWERDelta != 0 {
		t.Errorf("a no-op splice must have zero deltas, got wer=%v cpwer=%v", m.WERDelta, m.CpWERDelta)
	}
}

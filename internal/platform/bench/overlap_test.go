package bench

import (
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
)

// ref: A speaks 0..20, B speaks 8..12 → a single overlap region [8,12].
func overlapRefTurns() []dataset.Turn {
	return []dataset.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 20},
		{Speaker: "B", StartSeconds: 8, EndSeconds: 12},
	}
}

func TestScoreOverlapMeeting_ErrorConcentratedInOverlap(t *testing.T) {
	// Hyp gets A perfectly but MISSES B entirely — the classic missed-overlapping-
	// speaker failure. All the diarization error should live in the overlap region.
	hyp := MeetingHyp{MeetingID: "M", Turns: []HypTurn{{Speaker: "A", Start: 0, End: 20}}}
	m := scoreOverlapMeeting("M", hyp, overlapRefTurns())

	if m.OverlapRegions != 1 || m.PeakSpeakers != 2 {
		t.Fatalf("overlap detect: regions=%d peak=%d, want 1/2", m.OverlapRegions, m.PeakSpeakers)
	}
	if m.OverlapSec < 3.9 || m.OverlapSec > 4.1 {
		t.Errorf("overlap sec = %v, want ~4 ([8,12])", m.OverlapSec)
	}
	if m.DERErrorSec <= 0 {
		t.Fatalf("expected diarization error from the missed speaker, got %v", m.DERErrorSec)
	}
	if m.OverlapErrorSec <= 0 {
		t.Errorf("the missed speaker is in overlap → overlap error must be > 0, got %v", m.OverlapErrorSec)
	}
	// Outside the overlap, A is matched perfectly, so essentially ALL error is
	// addressable by separating the overlap region.
	if m.AddressableFraction < 0.9 {
		t.Errorf("addressable fraction = %v, want ~1.0 (all error is in overlap)", m.AddressableFraction)
	}
}

func TestScoreOverlapMeeting_PerfectHypHasNoError(t *testing.T) {
	// Hyp matches both speakers exactly → no diarization error, nothing addressable.
	hyp := MeetingHyp{MeetingID: "M", Turns: []HypTurn{
		{Speaker: "A", Start: 0, End: 20},
		{Speaker: "B", Start: 8, End: 12},
	}}
	m := scoreOverlapMeeting("M", hyp, overlapRefTurns())

	if m.OverlapRegions != 1 {
		t.Fatalf("overlap region should still be detected from the reference, got %d", m.OverlapRegions)
	}
	if m.DERErrorSec > 0.01 {
		t.Errorf("a perfect hyp should have ~0 DER error, got %v", m.DERErrorSec)
	}
	if m.OverlapErrorSec > 0.01 || m.AddressableFraction != 0 {
		t.Errorf("no error → nothing addressable, got errSec=%v frac=%v", m.OverlapErrorSec, m.AddressableFraction)
	}
}

func TestScoreOverlapMeeting_NoOverlapReference(t *testing.T) {
	// Sequential speakers, no overlap → no regions and nothing to repair.
	ref := []dataset.Turn{
		{Speaker: "A", StartSeconds: 0, EndSeconds: 5},
		{Speaker: "B", StartSeconds: 5, EndSeconds: 10},
	}
	hyp := MeetingHyp{MeetingID: "M", Turns: []HypTurn{{Speaker: "A", Start: 0, End: 5}, {Speaker: "B", Start: 5, End: 10}}}
	m := scoreOverlapMeeting("M", hyp, ref)
	if m.OverlapRegions != 0 || m.OverlapSec != 0 {
		t.Errorf("no overlap expected, got regions=%d sec=%v", m.OverlapRegions, m.OverlapSec)
	}
}

package bench_test

import (
	"math"
	"testing"

	bench "github.com/lukasstrickler/noto/internal/core/bench"
)

func TestOverlapRegions_DetectsDistinctSpeakerOverlap(t *testing.T) {
	// A speaks 0..10, B speaks 4..8 → overlap is exactly [4,8].
	spans := []bench.SpeakerSpan{
		{Speaker: "A", Start: 0, End: 10},
		{Speaker: "B", Start: 4, End: 8},
	}
	regions := bench.OverlapRegions(spans, 2)
	if len(regions) != 1 {
		t.Fatalf("expected 1 overlap region, got %d: %+v", len(regions), regions)
	}
	r := regions[0]
	if r.Start != 4 || r.End != 8 {
		t.Errorf("region = [%v,%v], want [4,8]", r.Start, r.End)
	}
	if r.MaxSpeakers != 2 {
		t.Errorf("max speakers = %d, want 2", r.MaxSpeakers)
	}
	if got := bench.OverlapSeconds(spans); got != 4 {
		t.Errorf("overlap seconds = %v, want 4", got)
	}
}

func TestOverlapRegions_SameSpeakerNeverCountsAsOverlap(t *testing.T) {
	// One speaker with two segments that touch/overlap themselves is NOT overlap —
	// overlap means two DISTINCT speakers (the cost driver). This guards against a
	// messy diarizer's adjacent same-speaker segments triggering needless repair.
	spans := []bench.SpeakerSpan{
		{Speaker: "A", Start: 0, End: 6},
		{Speaker: "A", Start: 5, End: 10},
	}
	if regions := bench.OverlapRegions(spans, 2); len(regions) != 0 {
		t.Errorf("same-speaker self-overlap must not be a region, got %+v", regions)
	}
	if got := bench.OverlapSeconds(spans); got != 0 {
		t.Errorf("same-speaker overlap seconds = %v, want 0", got)
	}
}

func TestOverlapRegions_MergesAdjacentAndTracksPeak(t *testing.T) {
	// A 0..10, B 2..5, C 4..7 → from 4..5 three speakers overlap. The overlap
	// region spans 2..7 (B enters at 2, C leaves at 7) with peak 3.
	spans := []bench.SpeakerSpan{
		{Speaker: "A", Start: 0, End: 10},
		{Speaker: "B", Start: 2, End: 5},
		{Speaker: "C", Start: 4, End: 7},
	}
	regions := bench.OverlapRegions(spans, 2)
	if len(regions) != 1 {
		t.Fatalf("expected 1 merged region, got %d: %+v", len(regions), regions)
	}
	if regions[0].Start != 2 || regions[0].End != 7 {
		t.Errorf("merged region = [%v,%v], want [2,7]", regions[0].Start, regions[0].End)
	}
	if regions[0].MaxSpeakers != 3 {
		t.Errorf("peak speakers = %d, want 3", regions[0].MaxSpeakers)
	}
}

func TestSpeechSeconds_UnionCountsOverlapOnce(t *testing.T) {
	spans := []bench.SpeakerSpan{
		{Speaker: "A", Start: 0, End: 10},
		{Speaker: "B", Start: 4, End: 12}, // union is 0..12 = 12s
	}
	if got := bench.SpeechSeconds(spans); got != 12 {
		t.Errorf("speech seconds = %v, want 12 (union, overlap once)", got)
	}
}

func TestPlanOverlapRepair_TargetedFarCheaperThanBlanket(t *testing.T) {
	// 3600s speech, 10% (360s) hard overlap, base $0.0194/audio-hr, separation pass
	// = 3× base per-second. Targeted repairs 360s; blanket would repair all 3600s.
	b := bench.OverlapRepairBudget{
		TotalSpeechSec: 3600,
		OverlapSec:     360,
		BaseCostPerSec: 0.0194 / 3600,
		SepCostFactor:  3,
	}
	c := bench.PlanOverlapRepair(b)
	if c.OverlapFraction != 0.1 {
		t.Errorf("overlap fraction = %v, want 0.1", c.OverlapFraction)
	}
	// Targeted is 10% of speech; blanket is 100% → blanket is 10× the spend.
	if math.Abs(c.SavingsFactor-10) > 0.001 {
		t.Errorf("savings factor = %v, want 10 (targeted touches 1/10 of speech)", c.SavingsFactor)
	}
	if c.TargetedExtraPct >= c.BlanketExtraPct {
		t.Errorf("targeted (%v%%) must cost less than blanket (%v%%)", c.TargetedExtraPct, c.BlanketExtraPct)
	}
	// Blanket adds SepCostFactor× the base cost (3×) → +300%.
	if math.Abs(c.BlanketExtraPct-300) > 0.5 {
		t.Errorf("blanket extra = %v%%, want ~300", c.BlanketExtraPct)
	}
}

func TestPlanOverlapRepair_CapBoundsWorstCaseAndReportsSkipped(t *testing.T) {
	// A pathological 60%-overlap meeting capped at 20% of speech: repair 20%, and
	// the other 40% is reported (never silently dropped).
	b := bench.OverlapRepairBudget{
		TotalSpeechSec: 1000,
		OverlapSec:     600,
		BaseCostPerSec: 0.00001,
		SepCostFactor:  3,
		CapFraction:    0.20,
	}
	c := bench.PlanOverlapRepair(b)
	if c.RepairedSec != 200 {
		t.Errorf("repaired sec = %v, want 200 (capped at 20%%)", c.RepairedSec)
	}
	if c.CappedSkippedSec != 400 {
		t.Errorf("capped-skipped sec = %v, want 400 (reported, not dropped)", c.CappedSkippedSec)
	}
}

func TestPlanOverlapRepair_ZeroOverlapIsFree(t *testing.T) {
	// A clean 1-on-1 (no within-system overlap) costs nothing extra — the common case.
	b := bench.OverlapRepairBudget{TotalSpeechSec: 3600, OverlapSec: 0, BaseCostPerSec: 0.0194 / 3600, SepCostFactor: 3}
	c := bench.PlanOverlapRepair(b)
	if c.TargetedExtraUSD != 0 || c.OverlapFraction != 0 {
		t.Errorf("zero overlap must add zero cost, got %+v", c)
	}
}

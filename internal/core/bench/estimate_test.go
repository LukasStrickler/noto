package bench_test

import (
	"testing"

	bench "github.com/lukasstrickler/noto/internal/core/bench"
)

func TestEstimate_AnchorProjectionAndHeadroom(t *testing.T) {
	// Full AMI anchor: 11.44 audio-hr at the audited $0.0194/hr.
	est := bench.Estimate(bench.EstimateInput{
		SuiteID:    "anchor_ami@2026-06-18",
		Tier:       "baseline_variance",
		AudioHours: 11.44,
		TierCapUSD: 15.0,
	})
	if est.SchemaVersion != bench.SchemaEstimateV1 {
		t.Fatalf("schema=%s", est.SchemaVersion)
	}
	// 0.0194 × 11.44 = 0.221936 → round to 0.2219.
	if est.ProjectedCostUSD < 0.2218 || est.ProjectedCostUSD > 0.2220 {
		t.Fatalf("projected=%v want ~0.2219", est.ProjectedCostUSD)
	}
	if !est.WithinBudget {
		t.Fatalf("expected within budget: %+v", est)
	}
	// Headroom = anchor - target = 0.0194 - 0.01 = 0.0094.
	if est.IdleHeadroomPerAudioHourUSD < 0.0093 || est.IdleHeadroomPerAudioHourUSD > 0.0095 {
		t.Fatalf("headroom=%v want 0.0094", est.IdleHeadroomPerAudioHourUSD)
	}
	// Savings if the run hit $0.01/hr: (0.0194-0.01) × 11.44 ≈ 0.1075.
	if est.ProjectedHeadroomSavingsUSD < 0.107 || est.ProjectedHeadroomSavingsUSD > 0.108 {
		t.Fatalf("savings=%v want ~0.1075", est.ProjectedHeadroomSavingsUSD)
	}
}

func TestEstimate_UnknownAudioHoursNotesAndCapOnly(t *testing.T) {
	est := bench.Estimate(bench.EstimateInput{
		SuiteID:    "smoke@2026-06-18",
		Tier:       "smoke",
		AudioHours: 0,
		TierCapUSD: 0.25,
	})
	if est.ProjectedCostUSD != 0 {
		t.Fatalf("projected should be 0 with unknown hours: %v", est.ProjectedCostUSD)
	}
	if len(est.Notes) == 0 {
		t.Fatal("expected a note about unknown audio hours")
	}
	if !est.WithinBudget {
		t.Fatal("zero projected cost is within any cap")
	}
}

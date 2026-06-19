package bench_test

import (
	"testing"

	bench "github.com/lukasstrickler/noto/internal/core/bench"
)

func TestScoreRun_WinnerScoresMidRange(t *testing.T) {
	// The frozen anchor winner: below the $0.0194 anchor, near (but under) the
	// quality guardrails, high GPU busy. Should pass guardrails and land a
	// sensible mid-range score, not 0 and not 100.
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.01633,
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		WER:                 0.214, DER: 0.103, CpWER: 0.288,
		BusyPct: 82.3,
	}, bench.DefaultKPIWeights())

	if !s.GuardrailsPass {
		t.Fatal("anchor winner should pass guardrails")
	}
	if s.Score <= 0 || s.Score >= 100 {
		t.Errorf("score %v out of expected mid-range", s.Score)
	}
	// cost is a third of the way from anchor to target.
	if c := s.Components["cost"]; c < 0.2 || c > 0.45 {
		t.Errorf("cost component %v not in expected band", c)
	}
	if s.Components["utilization"] < 0.8 {
		t.Errorf("utilization component %v should track busy%%", s.Components["utilization"])
	}
}

func TestScoreRun_GuardrailBreachGatesToZero(t *testing.T) {
	// Cheaper than target and GPU-saturated, but WER blows past the guardrail:
	// a cost win that regresses quality is a reject → score 0.
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.009, // beats the stretch target
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		WER:                 0.40, DER: 0.10, CpWER: 0.30, // WER over guardrail
		BusyPct: 95,
	}, bench.DefaultKPIWeights())

	if s.GuardrailsPass {
		t.Fatal("WER over guardrail should fail")
	}
	if s.Score != 0 {
		t.Errorf("guardrail breach must gate score to 0, got %v", s.Score)
	}
}

func TestScoreRun_BeatingTargetCapsCostAtOne(t *testing.T) {
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.005, // well under target
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		WER:                 0.10, DER: 0.05, CpWER: 0.15,
		BusyPct: 90,
	}, bench.DefaultKPIWeights())
	if s.Components["cost"] != 1 {
		t.Errorf("cost beating target should cap at 1, got %v", s.Components["cost"])
	}
}

func TestScoreRun_NoMetricsNotGated(t *testing.T) {
	// An integration/smoke run with no quality metrics: quality component 0, but
	// not gated (nothing was breached).
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.015,
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		BusyPct:             60,
	}, bench.DefaultKPIWeights())
	if !s.GuardrailsPass {
		t.Error("absent metrics must not be treated as a breach")
	}
	if s.Components["quality"] != 0 {
		t.Errorf("absent metrics → quality 0, got %v", s.Components["quality"])
	}
	if s.Score <= 0 {
		t.Error("a run with cost+util but no metrics should still score > 0")
	}
}

func TestScoreRun_IdleWasteNoted(t *testing.T) {
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.029, AnchorCostUSD: 0.0194, TargetCostUSD: 0.01,
		WER: 0.21, DER: 0.10, CpWER: 0.28, BusyPct: 53, IdleCostPerAudioHourUSD: 0.0116,
	}, bench.DefaultKPIWeights())
	found := false
	for _, n := range s.Notes {
		if len(n) > 0 && (n[:3] == "GPU") {
			found = true
		}
	}
	if !found {
		t.Errorf("idle waste should be surfaced as a note: %v", s.Notes)
	}
}

package bench_test

import (
	"math"
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

func TestScoreRun_AbsentQualityRenormalizedOut(t *testing.T) {
	// A smoke run with no quality metrics: quality's weight must be dropped from
	// the denominator, so the score is the cost/util weighted mean over only the
	// two measured dials — not dragged down by a phantom quality=0 with full weight.
	w := bench.DefaultKPIWeights()
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.015,
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		BusyPct:             60,
	}, w)
	want := 100 * (w.Cost*s.Components["cost"] + w.Utilization*s.Components["utilization"]) / (w.Cost + w.Utilization)
	if math.Abs(s.Score-want) > 1e-9 {
		t.Fatalf("absent quality not renormalized out: score=%v want=%v", s.Score, want)
	}
}

func TestScoreRun_AbsentCostRenormalizedOut(t *testing.T) {
	// No cost audit (CostPerAudioHourUSD==0): cost's heavy weight must be dropped
	// from the denominator, not contribute a hard 0 that halves the score.
	w := bench.DefaultKPIWeights()
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0,
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		WER:                 0.21, DER: 0.10, CpWER: 0.28,
		BusyPct: 80,
	}, w)
	if s.Components["cost"] != 0 {
		t.Fatalf("absent cost component should report 0, got %v", s.Components["cost"])
	}
	want := 100 * (w.Quality*s.Components["quality"] + w.Utilization*s.Components["utilization"]) / (w.Quality + w.Utilization)
	if math.Abs(s.Score-want) > 1e-9 {
		t.Fatalf("absent cost not renormalized out: score=%v want=%v", s.Score, want)
	}
}

func TestScoreRun_AbsentUtilizationRenormalizedOut(t *testing.T) {
	// A run with no GPU sample (BusyPct==0): a CPU/integration run, a run whose GPU
	// audit failed, or a remote decode the harness didn't profile. Utilization's
	// weight must be dropped from the denominator — NOT contribute a hard 0 that
	// tanks a strong cost+quality run for carrying no GPU reading. This mirrors the
	// cost/quality absence handling; utilization is the dial that used to be the
	// exception.
	w := bench.DefaultKPIWeights()
	s := bench.ScoreRun(bench.KPIInputs{
		CostPerAudioHourUSD: 0.013,
		AnchorCostUSD:       bench.AnchorCostPerProcessedAudioHourUSD,
		TargetCostUSD:       bench.StretchTargetCostPerAudioHourUSD,
		WER:                 0.21, DER: 0.10, CpWER: 0.28,
		BusyPct: 0, // no GPU sample
	}, w)
	if s.Components["utilization"] != 0 {
		t.Fatalf("absent utilization component should report 0, got %v", s.Components["utilization"])
	}
	want := 100 * (w.Cost*s.Components["cost"] + w.Quality*s.Components["quality"]) / (w.Cost + w.Quality)
	if math.Abs(s.Score-want) > 1e-9 {
		t.Fatalf("absent utilization not renormalized out: score=%v want=%v", s.Score, want)
	}
	// Sanity: had the 0 been folded in with full weight, the score would be strictly
	// lower — prove the renormalization actually moved the number.
	tanked := 100 * (w.Cost*s.Components["cost"] + w.Quality*s.Components["quality"]) / (w.Cost + w.Quality + w.Utilization)
	if want <= tanked {
		t.Fatalf("test is vacuous: renormalized %v not above folded-in %v", want, tanked)
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

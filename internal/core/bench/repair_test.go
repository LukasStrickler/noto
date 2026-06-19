package bench_test

import (
	"testing"

	bench "github.com/lukasstrickler/noto/internal/core/bench"
)

func TestRepairSpan_EligibleAndEV(t *testing.T) {
	// A high-confidence span is never eligible (don't repair what's right).
	hi := bench.RepairSpan{StartSec: 0, EndSec: 2, PError: 0.9, PSuccess: 0.8, ProductValue: 1, IsHighConf: true, ContextOK: true}
	if hi.Eligible() {
		t.Error("high-confidence span must be ineligible")
	}
	// A span with no context for the method is ineligible.
	noctx := bench.RepairSpan{StartSec: 0, EndSec: 2, PError: 0.9, PSuccess: 0.8, ProductValue: 1, ContextOK: false}
	if noctx.Eligible() {
		t.Error("span without context must be ineligible")
	}
	good := bench.RepairSpan{StartSec: 0, EndSec: 2, PError: 0.9, PSuccess: 0.8, ProductValue: 2, ContextOK: true}
	if !good.Eligible() {
		t.Fatal("a low-conf, high-value, context-ok span should be eligible")
	}
	// EV = 0.9*0.8*2 - (2s * $0.01/s * value-per-usd). With valuePerUSD small, EV>0.
	if ev := good.ExpectedValue(0.01, 1); ev <= 0 {
		t.Errorf("expected positive EV, got %v", ev)
	}
	// A punishingly expensive method drives EV negative.
	if ev := good.ExpectedValue(100, 1); ev >= 0 {
		t.Errorf("expensive method should make EV negative, got %v", ev)
	}
}

func TestRepairCeiling_HeadroomFromLowConfidenceErrors(t *testing.T) {
	// 10 words: the 2 lowest-confidence are errors, plus 1 high-confidence error.
	// Oracle-repairing the bottom 20% (2 words) fixes 2 of 3 errors.
	words := []bench.WordConfidence{
		{Confidence: 0.05, Correct: false}, // low-conf error (fixable)
		{Confidence: 0.10, Correct: false}, // low-conf error (fixable)
		{Confidence: 0.30, Correct: true},
		{Confidence: 0.40, Correct: true},
		{Confidence: 0.50, Correct: true},
		{Confidence: 0.60, Correct: true},
		{Confidence: 0.70, Correct: true},
		{Confidence: 0.80, Correct: true},
		{Confidence: 0.90, Correct: true},
		{Confidence: 0.95, Correct: false}, // high-conf error (NOT in bottom 20%)
	}
	c := bench.RepairCeiling(words, 0.20)
	if c.TotalErrors != 3 {
		t.Fatalf("total errors = %d, want 3", c.TotalErrors)
	}
	if c.WordsRepaired != 2 || c.FixableErrors != 2 {
		t.Errorf("bottom-20%% should repair 2 words / fix 2 errors, got %d/%d", c.WordsRepaired, c.FixableErrors)
	}
	if c.CurrentErrorRate != 0.30 {
		t.Errorf("current error rate = %v, want 0.30", c.CurrentErrorRate)
	}
	if c.CeilingErrorRate != 0.10 {
		t.Errorf("ceiling error rate = %v, want 0.10 (only the high-conf error remains)", c.CeilingErrorRate)
	}
}

func TestRepairCeiling_FlatWhenErrorsAreHighConfidence(t *testing.T) {
	// If errors hide in HIGH-confidence words, repairing low-confidence buys
	// nothing — the ceiling equals the current rate, so repair is not worth it.
	words := []bench.WordConfidence{
		{Confidence: 0.10, Correct: true},
		{Confidence: 0.20, Correct: true},
		{Confidence: 0.90, Correct: false},
		{Confidence: 0.95, Correct: false},
	}
	c := bench.RepairCeiling(words, 0.50)
	if c.CeilingErrorRate != c.CurrentErrorRate {
		t.Errorf("flat ceiling expected: current %v ceiling %v", c.CurrentErrorRate, c.CeilingErrorRate)
	}
}

func TestSpansFromWords_GroupsContiguousRiskyRuns(t *testing.T) {
	words := []bench.RepairWord{
		{StartSec: 0, EndSec: 1, Confidence: 0.95, HasConfidence: true},                      // high-conf → break
		{StartSec: 1, EndSec: 2, Confidence: 0.30, HasConfidence: true, ProductValue: 1},      // risky run A
		{StartSec: 2, EndSec: 3, Confidence: 0.20, HasConfidence: true, ProductValue: 3},      // risky run A (entity)
		{StartSec: 3, EndSec: 4, Confidence: 0.99, HasConfidence: true},                       // high-conf → break
		{StartSec: 4, EndSec: 5, Confidence: 0.10, HasConfidence: false},                      // no confidence → skip
		{StartSec: 5, EndSec: 6, Confidence: 0.40, HasConfidence: true, ProductValue: 1},      // risky run B
	}
	spans := bench.SpansFromWords(words, 0.5, 0.8, true)
	if len(spans) != 2 {
		t.Fatalf("expected 2 grouped spans, got %d: %+v", len(spans), spans)
	}
	// Run A spans 1..3, riskiest word conf 0.20 → PError 0.80, max ProductValue 3.
	a := spans[0]
	if a.StartSec != 1 || a.EndSec != 3 {
		t.Errorf("span A = [%v,%v], want [1,3]", a.StartSec, a.EndSec)
	}
	if a.PError < 0.79 || a.PError > 0.81 {
		t.Errorf("span A PError = %v, want ~0.80 (riskiest word)", a.PError)
	}
	if a.ProductValue != 3 {
		t.Errorf("span A ProductValue = %v, want 3 (max/entity)", a.ProductValue)
	}
	if a.PSuccess != 0.8 || !a.ContextOK {
		t.Errorf("span A should carry method pSuccess/contextOK: %+v", a)
	}
	if spans[1].StartSec != 5 {
		t.Errorf("span B should start at 5, got %v", spans[1].StartSec)
	}
}

func TestSpansFromWords_NoConfidenceYieldsNoSpans(t *testing.T) {
	words := []bench.RepairWord{
		{StartSec: 0, EndSec: 1, Confidence: 0.1, HasConfidence: false},
		{StartSec: 1, EndSec: 2, Confidence: 0.1, HasConfidence: false},
	}
	if spans := bench.SpansFromWords(words, 0.5, 0.8, true); len(spans) != 0 {
		t.Errorf("words with no confidence must yield no repair spans, got %d", len(spans))
	}
}

func TestRepairBudget_SecBudget(t *testing.T) {
	// 600s speech × 10% = 60s, under the 300s cap → 60s.
	b := bench.RepairBudget{SpeechSec: 600, Ratio: 0.10, CapSec: 300}
	if got := b.SecBudget(); got != 60 {
		t.Errorf("SecBudget = %v, want 60", got)
	}
	// Cap binds: 6000s × 10% = 600s, capped to 300.
	b2 := bench.RepairBudget{SpeechSec: 6000, Ratio: 0.10, CapSec: 300}
	if got := b2.SecBudget(); got != 300 {
		t.Errorf("capped SecBudget = %v, want 300", got)
	}
	// Dollar guard binds: $1 / $0.05/s = 20s < 60s.
	b3 := bench.RepairBudget{SpeechSec: 600, Ratio: 0.10, CapSec: 300, CostPerSecUSD: 0.05, BudgetUSD: 1}
	if got := b3.SecBudget(); got != 20 {
		t.Errorf("dollar-guarded SecBudget = %v, want 20", got)
	}
}

func TestPlanRepairs_RanksByEVAndRespectsBudget(t *testing.T) {
	spans := []bench.RepairSpan{
		{StartSec: 0, EndSec: 10, PError: 0.9, PSuccess: 0.9, ProductValue: 3, ContextOK: true}, // high EV, 10s
		{StartSec: 10, EndSec: 20, PError: 0.5, PSuccess: 0.5, ProductValue: 1, ContextOK: true}, // low EV, 10s
		{StartSec: 20, EndSec: 25, PError: 0.2, PSuccess: 0.1, ProductValue: 1, ContextOK: true}, // tiny EV
		{StartSec: 25, EndSec: 30, PError: 0.9, PSuccess: 0.9, ProductValue: 2, IsHighConf: true}, // ineligible
	}
	// Budget = 600s speech × 2% = 12s → only ~one 10s span fits.
	plan := bench.PlanRepairs(spans, bench.RepairBudget{SpeechSec: 600, Ratio: 0.02, CostPerSecUSD: 0.001}, 1)

	if plan.BudgetSec != 12 {
		t.Fatalf("BudgetSec = %v, want 12", plan.BudgetSec)
	}
	if len(plan.Attempt) != 1 {
		t.Fatalf("expected 1 attempted span within budget, got %d", len(plan.Attempt))
	}
	// The highest-EV span (the first) must be the one attempted.
	if plan.Attempt[0].StartSec != 0 {
		t.Errorf("highest-EV span should be attempted first, got start=%v", plan.Attempt[0].StartSec)
	}
	// The high-confidence span is ineligible; a positive-EV span that didn't fit is
	// recorded as budget-skipped (not silently dropped).
	if len(plan.SkippedBudget) < 1 {
		t.Errorf("expected at least one budget-skipped span, got %d", len(plan.SkippedBudget))
	}
	foundHiConf := false
	for _, s := range plan.Ineligible {
		if s.IsHighConf {
			foundHiConf = true
		}
	}
	if !foundHiConf {
		t.Error("high-confidence span should be in Ineligible")
	}
}

func TestRepairReport_GateAndAccounting(t *testing.T) {
	plan := bench.RepairPlan{
		PlannedSec:    10,
		SkippedBudget: []bench.RepairSpan{{StartSec: 0, EndSec: 5, ContextOK: true, PError: 0.5}},
	}
	outcomes := []bench.RepairOutcome{
		{Span: bench.RepairSpan{StartSec: 0, EndSec: 4}, CostUSD: 0.002, WERDelta: -0.05, EntityDelta: -0.02, Accepted: true},
		{Span: bench.RepairSpan{StartSec: 4, EndSec: 6}, CostUSD: 0.001, WERDelta: 0.01, Negative: true}, // made it worse
	}
	r := bench.BuildRepairReport(plan, outcomes)

	if r.AcceptedRepairs != 1 || r.NegativeRepairs != 1 {
		t.Fatalf("accounting: accepted=%d negative=%d", r.AcceptedRepairs, r.NegativeRepairs)
	}
	if r.SkippedHighValueSec != 5 {
		t.Errorf("skipped high-value sec = %v, want 5", r.SkippedHighValueSec)
	}
	if r.NetWERDelta != -0.05 {
		t.Errorf("net WER delta = %v, want -0.05 (only accepted counts)", r.NetWERDelta)
	}
	// 1 accepted / $0.003 ≈ 333/$ ; negative rate 1/1 = 100% → fails the gate.
	pass, reasons := r.PassesB7Gate(50, 0.005)
	if pass {
		t.Error("100% negative rate must fail the B7 gate")
	}
	if len(reasons) == 0 {
		t.Error("a failing gate must explain why")
	}
}

func TestRepairReport_PassesWhenItBeatsDoNothing(t *testing.T) {
	r := bench.RepairReport{
		AcceptedRepairs: 10,
		NegativeRepairs: 0,
		CostUSD:         0.05, // 10 / 0.05 = 200 accepted/$
		NetWERDelta:     -0.10,
		NetEntityDelta:  -0.04,
	}
	pass, reasons := r.PassesB7Gate(50, 0.005)
	if !pass {
		t.Errorf("a clean net-improving dry-run should pass: %v", reasons)
	}
}

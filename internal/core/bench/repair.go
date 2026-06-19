package bench

import "sort"

// repair.go is the PURE core of the B7 dry-run repair system (§10): decide which
// transcript spans are worth a second pass, by expected value, under budget — then
// account for the result so we can SEE how much accuracy a repair buys and at what
// cost. First shipped form is DRY-RUN ONLY (§10.4): score + rank + report
// candidates and their benchmark deltas; never rewrite production transcript text
// until the B7 gate passes. No I/O — the runner feeds it confidences + deltas.

// RepairMethod is a second-pass technique (§10.5). Cheapest-first; the dry-run
// records candidates for whichever method the profile allows.
type RepairMethod string

const (
	MethodAlternateDecode RepairMethod = "same_model_alternate_decode" // cheap first repair
	MethodContextBiased   RepairMethod = "context_biased_redecode"     // H8, repair-only first
	MethodForcedAlign     RepairMethod = "forced_alignment"            // candidate ranking
	MethodAlternateASR    RepairMethod = "alternate_asr"               // H9, independent pass
)

// RepairSpan is one candidate region: a low-confidence / high-value stretch with
// the signals §10.2/§10.4 feed into the expected-value decision.
type RepairSpan struct {
	StartSec     float64
	EndSec       float64
	PError       float64 // P(word(s) wrong) — from calibration / bottom-decile confidence
	PSuccess     float64 // P(repair fixes it | method) — repairability
	ProductValue float64 // entity/action weight (§10.4); 1.0 = ordinary words, >1 = high-value
	IsHighConf   bool    // already high-confidence → ineligible (don't repair what's right)
	ContextOK    bool    // required context (glossary/bias) exists for the method
}

// DurationSec is the span length; 0 for a degenerate span.
func (s RepairSpan) DurationSec() float64 {
	if s.EndSec > s.StartSec {
		return s.EndSec - s.StartSec
	}
	return 0
}

// Eligible is the §10.4 HARD gate (budget is applied across spans by PlanRepairs):
// not already high-confidence, has context, a real error probability, real duration.
func (s RepairSpan) Eligible() bool {
	return !s.IsHighConf && s.ContextOK && s.PError > 0 && s.DurationSec() > 0
}

// ExpectedValue (§10.4): P(error) × P(success) × product_value − expected compute
// cost. The cost (duration × $/sec) is converted into the same units as the benefit
// via valuePerUSD ("how much accuracy-value one dollar buys"), so EV stays a single
// rankable number. It is a DIAGNOSTIC ranking signal for exploration only — never a
// ledger adopt/reject score (§10.9 defers adoption to §4.3).
func (s RepairSpan) ExpectedValue(costPerSecUSD, valuePerUSD float64) float64 {
	benefit := s.PError * s.PSuccess * s.ProductValue
	cost := s.DurationSec() * costPerSecUSD * valuePerUSD
	return benefit - cost
}

// RepairWord is the minimal per-word signal the span-builder needs: its time span,
// the model's confidence, and a product-value weight (entities/actions score
// higher — §10.4). Distinct from calibration's WordConfidence, which pairs
// confidence with a correctness label for ECE/Brier — this one carries TIMING so
// risky stretches can be grouped into spans.
type RepairWord struct {
	StartSec      float64
	EndSec        float64
	Confidence    float64 // P(correct); lower = riskier
	HasConfidence bool    // false → the engine emitted none; can't assess, skip
	ProductValue  float64 // entity/action weight; <=0 treated as 1 (ordinary)
}

// SpansFromWords groups MAXIMAL runs of low-confidence words (confidence < threshold)
// into RepairSpans, so the planner ranks contiguous risky stretches rather than
// isolated words. A non-risky word — or one with no confidence — ends the current
// run. PError is the riskiest (1−confidence) word in the run; ProductValue the max;
// ContextOK and pSuccess come from the repair method the caller is pricing. These
// spans are low-confidence by construction, so none are flagged IsHighConf.
func SpansFromWords(words []RepairWord, threshold, pSuccess float64, contextOK bool) []RepairSpan {
	var spans []RepairSpan
	var cur *RepairSpan
	flush := func() {
		if cur != nil {
			spans = append(spans, *cur)
			cur = nil
		}
	}
	for _, w := range words {
		risky := w.HasConfidence && w.Confidence < threshold && w.EndSec > w.StartSec
		if !risky {
			flush()
			continue
		}
		pe := 1 - w.Confidence
		pv := w.ProductValue
		if pv <= 0 {
			pv = 1
		}
		if cur == nil {
			cur = &RepairSpan{StartSec: w.StartSec, EndSec: w.EndSec, PError: pe, PSuccess: pSuccess, ProductValue: pv, ContextOK: contextOK}
		} else {
			cur.EndSec = w.EndSec
			if pe > cur.PError {
				cur.PError = pe
			}
			if pv > cur.ProductValue {
				cur.ProductValue = pv
			}
		}
	}
	flush()
	return spans
}

// RepairBudget bounds second-pass compute per meeting (§10.6). The second-budget is
// min(speech × ratio, cap); BudgetUSD is the optional dollar guard for LLM/API paths.
type RepairBudget struct {
	SpeechSec     float64
	Ratio         float64 // fraction of speech eligible (normal 0.05–0.15; hi-q 0.15–0.30)
	CapSec        float64 // hard cap (e.g. 300–600s); 0 = no cap
	CostPerSecUSD float64 // marginal $ per repaired second for the method
	BudgetUSD     float64 // dollar guard; 0 = bounded by seconds only
}

// SecBudget is the §10.6 second budget: min(speech×ratio, cap), and further capped
// by BudgetUSD/CostPerSecUSD when a dollar guard is set.
func (b RepairBudget) SecBudget() float64 {
	sec := b.SpeechSec * b.Ratio
	if b.CapSec > 0 && sec > b.CapSec {
		sec = b.CapSec
	}
	if b.BudgetUSD > 0 && b.CostPerSecUSD > 0 {
		if usdSec := b.BudgetUSD / b.CostPerSecUSD; usdSec < sec {
			sec = usdSec
		}
	}
	if sec < 0 {
		sec = 0
	}
	return sec
}

// RepairPlan is the ranked dry-run candidate set under budget: what to attempt,
// what is skipped for budget (recorded, never silently dropped — §10.6 reports
// skipped high-value seconds), and the projected spend.
type RepairPlan struct {
	Attempt        []RepairSpan
	SkippedBudget  []RepairSpan
	Ineligible     []RepairSpan
	BudgetSec      float64
	PlannedSec     float64
	PlannedCostUSD float64
}

// PlanRepairs ranks eligible, positive-EV spans by descending EV and greedily fills
// the second-budget, so the most valuable spans are attempted first. Eligible
// positive-EV spans that no longer fit are recorded in SkippedBudget (the §10.6
// "skipped high-value seconds"); ineligible or non-positive-EV spans go to
// Ineligible. valuePerUSD tunes the EV cost term (see ExpectedValue).
func PlanRepairs(spans []RepairSpan, budget RepairBudget, valuePerUSD float64) RepairPlan {
	plan := RepairPlan{BudgetSec: budget.SecBudget()}

	type scored struct {
		span RepairSpan
		ev   float64
	}
	var cand []scored
	for _, s := range spans {
		if !s.Eligible() {
			plan.Ineligible = append(plan.Ineligible, s)
			continue
		}
		ev := s.ExpectedValue(budget.CostPerSecUSD, valuePerUSD)
		if ev <= 0 {
			plan.Ineligible = append(plan.Ineligible, s) // not worth it
			continue
		}
		cand = append(cand, scored{s, ev})
	}
	// Highest EV first; ties broken by higher P(error) then longer span so the
	// order is deterministic (no map iteration / time dependence).
	sort.SliceStable(cand, func(i, j int) bool {
		if cand[i].ev != cand[j].ev {
			return cand[i].ev > cand[j].ev
		}
		if cand[i].span.PError != cand[j].span.PError {
			return cand[i].span.PError > cand[j].span.PError
		}
		return cand[i].span.DurationSec() > cand[j].span.DurationSec()
	})
	for _, c := range cand {
		d := c.span.DurationSec()
		if plan.PlannedSec+d <= plan.BudgetSec {
			plan.Attempt = append(plan.Attempt, c.span)
			plan.PlannedSec += d
		} else {
			plan.SkippedBudget = append(plan.SkippedBudget, c.span)
		}
	}
	plan.PlannedCostUSD = plan.PlannedSec * budget.CostPerSecUSD
	return plan
}

// RepairOutcome is the measured result of attempting one span against benchmark
// ground truth (dry-run): the cost spent and the quality deltas (negative = better).
// Accepted means the edit improved net quality; Negative means it made it WORSE — a
// negative repair, the §4.1 rate that must stay < 0.5% of accepted edits.
type RepairOutcome struct {
	Span        RepairSpan
	Method      RepairMethod
	CostUSD     float64
	WERDelta    float64 // change in WER for the span (negative = improvement)
	EntityDelta float64 // change in entity WER (negative = improvement)
	Accepted    bool
	Negative    bool
}

// RepairReport aggregates dry-run outcomes into the §10.6 accounting and the B7
// gate signals — the answer to "how good is our accuracy gain, and at what cost".
type RepairReport struct {
	CandidateSec        float64
	AttemptedSec        float64
	AcceptedSec         float64
	SkippedHighValueSec float64
	CostUSD             float64
	NetWERDelta         float64 // sum of accepted WER deltas (negative = net improvement)
	NetEntityDelta      float64
	AcceptedRepairs     int
	NegativeRepairs     int
}

// BuildRepairReport folds a plan + its measured outcomes into the report.
func BuildRepairReport(plan RepairPlan, outcomes []RepairOutcome) RepairReport {
	r := RepairReport{
		AttemptedSec: plan.PlannedSec,
		CandidateSec: plan.PlannedSec,
	}
	for _, s := range plan.SkippedBudget {
		r.SkippedHighValueSec += s.DurationSec()
		r.CandidateSec += s.DurationSec()
	}
	for _, o := range outcomes {
		r.CostUSD += o.CostUSD
		if o.Negative {
			r.NegativeRepairs++
		}
		if o.Accepted {
			r.AcceptedRepairs++
			r.AcceptedSec += o.Span.DurationSec()
			r.NetWERDelta += o.WERDelta
			r.NetEntityDelta += o.EntityDelta
		}
	}
	return r
}

// AcceptedPerUSD is accepted repairs per dollar — the B7 efficiency gate (§14).
func (r RepairReport) AcceptedPerUSD() float64 {
	if r.CostUSD <= 0 {
		return 0
	}
	return float64(r.AcceptedRepairs) / r.CostUSD
}

// NegativeRate is negative repairs as a fraction of accepted edits (§4.1; must stay
// below 0.5% before production writes).
func (r RepairReport) NegativeRate() float64 {
	if r.AcceptedRepairs <= 0 {
		return 0
	}
	return float64(r.NegativeRepairs) / float64(r.AcceptedRepairs)
}

// PassesB7Gate is the dry-run gate (§14): repair must beat do-nothing — a NET
// quality improvement (WER or entity delta < 0), negative repairs under the cap,
// and at least minAcceptedPerUSD accepted edits per dollar. Returns the reasons it
// failed so the runner can report them.
func (r RepairReport) PassesB7Gate(minAcceptedPerUSD, maxNegativeRate float64) (bool, []string) {
	var reasons []string
	if r.NetWERDelta >= 0 && r.NetEntityDelta >= 0 {
		reasons = append(reasons, "no net WER/entity improvement vs do-nothing")
	}
	if r.NegativeRate() > maxNegativeRate {
		reasons = append(reasons, "negative-repair rate over cap")
	}
	if r.AcceptedPerUSD() < minAcceptedPerUSD {
		reasons = append(reasons, "accepted-repairs-per-dollar below threshold")
	}
	return len(reasons) == 0, reasons
}

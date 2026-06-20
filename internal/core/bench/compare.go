package bench

import (
	"fmt"
	"math"
	"strings"
)

// ComparabilityOpts lists fields that must match for a valid compare.
type ComparabilityOpts struct {
	AllowCodeDrift bool
}

// Comparable reports whether baseline and candidate manifests can be compared.
func Comparable(base, cand RunManifest, opts ComparabilityOpts) (bool, string) {
	if base.ExecutionProfile != cand.ExecutionProfile {
		return false, "profile_mismatch"
	}
	if base.OperatingMode != cand.OperatingMode {
		return false, "mode_mismatch"
	}
	if base.SuiteID != cand.SuiteID {
		return false, "suite_mismatch"
	}
	if base.BenchStack != "" && cand.BenchStack != "" && base.BenchStack != cand.BenchStack {
		return false, "bench_stack_mismatch"
	}
	if base.Comparability.TargetConcurrency != cand.Comparability.TargetConcurrency {
		return false, "concurrency_mismatch"
	}
	if base.Comparability.CacheState != cand.Comparability.CacheState {
		return false, "cache_mismatch"
	}
	if base.Comparability.TraceMode != cand.Comparability.TraceMode {
		return false, "trace_mismatch"
	}
	if !opts.AllowCodeDrift && base.GitCommit != "" && cand.GitCommit != "" && base.GitCommit != cand.GitCommit {
		return false, "code_mismatch"
	}
	return true, ""
}

// GateOpts configures adoption thresholds.
type GateOpts struct {
	MinCostImprovementPct   float64
	MaxComplexityDirect     int
	AllowCodeDrift          bool
	MaxQualityRegressionAbs float64
}

func DefaultGateOpts() GateOpts {
	return GateOpts{
		MinCostImprovementPct:   5.0,
		MaxComplexityDirect:     2,
		MaxQualityRegressionAbs: 0.0025,
	}
}

// DecideAdoption applies §4.3 rules (simplified MDE: fixed pct threshold).
func DecideAdoption(base, cand RunBundle, cmp CompareResult, gates GateOpts) Decision {
	ok, reason := Comparable(base.Manifest, cand.Manifest, ComparabilityOpts{AllowCodeDrift: gates.AllowCodeDrift})
	if !ok {
		_ = reason
		return DecisionRetry
	}
	if cand.TraceSummary.DroppedTraceEvents > 0 && cand.TraceSummary.TraceMode != "off" {
		return DecisionRetry
	}
	if cand.TraceSummary.CostWaterfall.UnattributedPct > 2 {
		return DecisionReject
	}
	if len(cmp.GuardrailsFailed) > 0 {
		return DecisionReject
	}
	if cmp.SignatureCheck != nil && !cmp.SignatureCheck.Pass {
		return DecisionReject
	}
	if !cmp.Cost.MeetsMDE {
		return DecisionReject
	}
	if cmp.ComplexityDelta > gates.MaxComplexityDirect {
		return DecisionNeedsConfirm
	}
	return DecisionAdopt
}

// ExceedsMDE checks batch cost improvement against threshold.
func ExceedsMDE(baseCost, candCost, minPct float64) bool {
	if baseCost <= 0 {
		return false
	}
	improvement := (baseCost - candCost) / baseCost * 100
	return improvement >= minPct
}

// WaterfallDeltas returns per-bucket USD deltas (candidate - baseline).
func WaterfallDeltas(base, cand TraceSummary) map[string]float64 {
	out := map[string]float64{}
	add := func(key string, b, c float64) {
		if math.Abs(c-b) > 1e-9 {
			out[key] = roundUSD(c - b)
		}
	}
	add("total", base.CostWaterfall.TotalUSD, cand.CostWaterfall.TotalUSD)
	add("compute", base.CostWaterfall.ComputeUSD, cand.CostWaterfall.ComputeUSD)
	add("warm_idle", base.CostWaterfall.WarmIdleUSD, cand.CostWaterfall.WarmIdleUSD)
	add("unattributed", base.CostWaterfall.UnattributedUSD, cand.CostWaterfall.UnattributedUSD)
	// The remaining waterfall buckets are additive components of total (alongside
	// compute), so a cost move that lands in one of them must appear in the delta
	// map — otherwise waterfallResidual reads the whole total move as unexplained
	// and Compare spuriously returns retry on a real win. add() keeps a zero bucket
	// out of the map, so the common all-zero case is byte-for-byte unchanged.
	add("cold_start", base.CostWaterfall.ColdStartUSD, cand.CostWaterfall.ColdStartUSD)
	add("model_load", base.CostWaterfall.ModelLoadUSD, cand.CostWaterfall.ModelLoadUSD)
	add("audio_stage", base.CostWaterfall.AudioStageUSD, cand.CostWaterfall.AudioStageUSD)
	add("retry", base.CostWaterfall.RetryUSD, cand.CostWaterfall.RetryUSD)
	add("crash_billed", base.CostWaterfall.CrashBilledUSD, cand.CostWaterfall.CrashBilledUSD)
	add("cancel_billed", base.CostWaterfall.CancelBilledUSD, cand.CostWaterfall.CancelBilledUSD)
	add("trace_overhead", base.CostWaterfall.TraceOverheadUSD, cand.CostWaterfall.TraceOverheadUSD)
	stageBase := stageUSDMap(base)
	stageCand := stageUSDMap(cand)
	seen := map[string]bool{}
	for k, v := range stageBase {
		seen[k] = true
		add(k, v, stageCand[k])
	}
	for k, v := range stageCand {
		if !seen[k] {
			add(k, 0, v)
		}
	}
	return out
}

// gpuCompare contrasts the GPU-efficiency block of two runs. Returns nil unless
// BOTH runs sampled the GPU: a one-sided comparison (e.g. a CPU/summary-only
// baseline vs a GPU candidate) has no real other side, and computing a delta
// against the missing side's zero value would fabricate an idle-cost
// "regression/progress" and a "busy 0%→N%" transition out of absence-of-data.
func gpuCompare(base, cand *GPUUtilization) *CompareGPU {
	if base == nil || cand == nil {
		return nil
	}
	g := &CompareGPU{}
	if base != nil {
		g.BaselineBusyPct = base.BusyPct
		g.BaselineIdleCostPerAudioHourUSD = base.IdleCostPerAudioHourUSD
	}
	if cand != nil {
		g.CandidateBusyPct = cand.BusyPct
		g.CandidateIdleCostPerAudioHourUSD = cand.IdleCostPerAudioHourUSD
	}
	g.DeltaIdleCostPerAudioHourUSD = roundUSD(g.CandidateIdleCostPerAudioHourUSD - g.BaselineIdleCostPerAudioHourUSD)
	return g
}

// gpuInsights surfaces idle-cost movement as a machine-readable insight. Idle
// cost is a billed bucket (not raw utilization), so it is allowed by §12.17 —
// the addressable spend the candidate either recovered or wasted.
func gpuInsights(g *CompareGPU) []string {
	if g == nil || math.Abs(g.DeltaIdleCostPerAudioHourUSD) < 0.0005 {
		return nil
	}
	dir := "down"
	if g.DeltaIdleCostPerAudioHourUSD > 0 {
		dir = "up"
	}
	return []string{fmt.Sprintf(
		"gpu idle cost/audio-hr moved %s $%.4f (busy %.1f%%→%.1f%%): %s toward the $%.2f target.",
		dir, math.Abs(g.DeltaIdleCostPerAudioHourUSD),
		g.BaselineBusyPct, g.CandidateBusyPct,
		map[bool]string{true: "progress", false: "regression"}[dir == "down"],
		StretchTargetCostPerAudioHourUSD,
	)}
}

// runScaleModel decomposes one run into fixed-overhead vs marginal-floor via its
// measured GPU idle cost (scale.go). Returns false when the run lacks the GPU
// samples or denominators the decomposition needs (e.g. CPU/integration runs).
func runScaleModel(b RunBundle) (ScaleModel, bool) {
	total := b.TraceSummary.CostWaterfall.TotalUSD
	hours := audioHoursOf(b)
	if total <= 0 || hours <= 0 || b.TraceSummary.GPU == nil {
		return ScaleModel{}, false
	}
	return ScaleModelFromIdle(total, b.TraceSummary.GPU.IdleCostUSD, hours)
}

func audioHoursOf(b RunBundle) float64 {
	if b.Metrics.Denominators != nil {
		if ah, ok := b.Metrics.Denominators["audio_hours"].(float64); ok {
			return ah
		}
	}
	return 0
}

// scaleCompare contrasts the two runs' asymptotic marginal floors so a cost lever
// is judged by whether it moved the floor (the $0.01 mover) — not just the
// headline at the current scale. Returns nil when either run can't be decomposed.
func scaleCompare(base, cand RunBundle) *CompareScale {
	bm, bok := runScaleModel(base)
	cm, cok := runScaleModel(cand)
	if !bok || !cok {
		return nil
	}
	return &CompareScale{
		BaselineFloorUSDPerAudioHour:  bm.Floor(),
		CandidateFloorUSDPerAudioHour: cm.Floor(),
		DeltaFloorUSDPerAudioHour:     roundUSD(cm.Floor() - bm.Floor()),
		BaselineFixedUSD:              roundUSD(bm.FixedUSD),
		CandidateFixedUSD:             roundUSD(cm.FixedUSD),
		TargetReachableByScale:        cm.MarginalUSDPerAudioHour < StretchTargetCostPerAudioHourUSD,
	}
}

// scaleInsights explains floor movement: the headline $/audio-hr can fall just by
// trimming idle (fixed overhead), but only a lower marginal floor changes what is
// achievable at scale — so this calls out which one the lever actually did.
func scaleInsights(s *CompareScale) []string {
	if s == nil || math.Abs(s.DeltaFloorUSDPerAudioHour) < 0.0005 {
		return nil
	}
	dir, verdict := "down", "lowers the asymptotic $/audio-hr — real progress toward the $0.01 floor"
	if s.DeltaFloorUSDPerAudioHour > 0 {
		dir, verdict = "up", "raises the asymptotic $/audio-hr — moves the achievable floor the wrong way"
	}
	return []string{fmt.Sprintf(
		"marginal cost floor moved %s $%.4f/audio-hr ($%.4f→$%.4f): this lever %s.",
		dir, math.Abs(s.DeltaFloorUSDPerAudioHour),
		s.BaselineFloorUSDPerAudioHour, s.CandidateFloorUSDPerAudioHour, verdict)}
}

func stageUSDMap(ts TraceSummary) map[string]float64 {
	m := map[string]float64{}
	for _, s := range ts.ComputeByStage {
		m[s.Stage] = s.USD
	}
	return m
}

func roundUSD(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// Compare builds compare.v1 from baseline and candidate bundles.
func Compare(base, cand RunBundle, opts GateOpts) CompareResult {
	cmp := CompareResult{
		SchemaVersion:  SchemaCompareV1,
		BaselineRunID:  base.Manifest.RunID,
		CandidateRunID: cand.Manifest.RunID,
		HypothesisID:   cand.Manifest.HypothesisID,
		ProgramStep:    cand.Manifest.ProgramStep,
	}
	ok, reason := Comparable(base.Manifest, cand.Manifest, ComparabilityOpts{AllowCodeDrift: opts.AllowCodeDrift})
	cmp.Comparable = ok
	cmp.IncomparableReason = reason
	if !ok {
		cmp.Decision = DecisionRetry
		return cmp
	}

	baseCost := costPerHour(base)
	candCost := costPerHour(cand)
	cmp.Cost.BaselineCostPerProcessedAudioHourUSD = baseCost
	cmp.Cost.CandidateCostPerProcessedAudioHourUSD = candCost
	if baseCost > 0 {
		cmp.Cost.DeltaPct = roundUSD((candCost - baseCost) / baseCost * 100)
	}
	cmp.Cost.MeetsMDE = ExceedsMDE(baseCost, candCost, opts.MinCostImprovementPct)
	cmp.Cost.WaterfallDeltasUSD = WaterfallDeltas(base.TraceSummary, cand.TraceSummary)

	if cand.TraceSummary.CostWaterfall.UnattributedPct > 2 {
		cmp.GuardrailsFailed = append(cmp.GuardrailsFailed, "unattributed_pct")
		cmp.NextExperiment = "fix_unattributed_instrumentation"
		cmp.Decision = DecisionReject
		return cmp
	}

	if totalDelta := cmp.Cost.WaterfallDeltasUSD["total"]; math.Abs(totalDelta) > 1e-6 {
		if residual := waterfallResidual(cmp.Cost.WaterfallDeltasUSD); math.Abs(residual) > 0.05*math.Abs(totalDelta) {
			cmp.Decision = DecisionRetry
			return cmp
		}
	}

	cmp.Quality, cmp.GuardrailsFailed, cmp.GuardrailsSkipped = qualityGuardrails(base.Metrics, cand.Metrics, opts)
	if len(cmp.GuardrailsFailed) > 0 {
		cmp.Decision = DecisionReject
		cmp.NextExperiment = "quality_regression"
		return cmp
	}
	if len(cmp.GuardrailsSkipped) > 0 && !qualitySkipsAllowed(cand.Manifest) {
		cmp.Decision = DecisionRetry
		cmp.NextExperiment = "score_quality_metrics"
		return cmp
	}

	cmp.SignatureCheck = CheckHypothesisSignature(cand.Manifest.HypothesisID, base, cand, cmp.Cost.WaterfallDeltasUSD)
	cmp.ManifestDiff = knobDiff(base.Manifest, cand.Manifest)
	cmp.Intermediates = buildIntermediates(base, cand)
	cmp.AttributionInsights = buildInsights(cand.Manifest, base, cand, cmp.Cost.WaterfallDeltasUSD)
	cmp.GPU = gpuCompare(base.TraceSummary.GPU, cand.TraceSummary.GPU)
	cmp.AttributionInsights = append(cmp.AttributionInsights, gpuInsights(cmp.GPU)...)
	cmp.Scale = scaleCompare(base, cand)
	cmp.AttributionInsights = append(cmp.AttributionInsights, scaleInsights(cmp.Scale)...)
	cmp.ComplexityDelta = complexityDelta(cand.Manifest.Knobs)
	cmp.Decision = DecideAdoption(base, cand, cmp, opts)
	if cmp.Decision == DecisionReject && cmp.NextExperiment == "" && cmp.SignatureCheck != nil && !cmp.SignatureCheck.Pass {
		cmp.NextExperiment = "hypothesis_falsified"
	}
	return cmp
}

func qualitySkipsAllowed(m RunManifest) bool {
	return m.IntegrationOnly || m.Tier == "smoke" || m.Tier == "integration-only"
}

var qualityCeilings = map[string]float64{
	"wer":   0.235,
	"der":   0.113,
	"cpwer": 0.311,
}

// QualityWithinCeilings reports whether aggregate WER/DER/cpWER are all present
// and within the absolute §4 ceilings — the subsample-quality half of the
// scale-readiness gate (scale.go), so a config that already regressed accuracy
// can't earn a full-anchor run.
func QualityWithinCeilings(aggregate map[string]float64) bool {
	for name, ceiling := range qualityCeilings {
		v, ok := aggregate[name]
		if !ok || v > ceiling {
			return false
		}
	}
	return true
}

func qualityGuardrails(base, cand MetricsFile, opts GateOpts) (map[string]any, []string, []string) {
	quality := map[string]any{}
	var failed []string
	var skipped []string
	for _, name := range []string{"wer", "der", "cpwer"} {
		candidate, ok := cand.Aggregate[name]
		if !ok {
			skipped = append(skipped, "quality_"+name)
			continue
		}
		row := map[string]float64{"candidate": candidate}
		if baseline, ok := base.Aggregate[name]; ok {
			row["baseline"] = baseline
			row["delta_abs"] = candidate - baseline
			if baseline > 0 {
				row["delta_pct"] = (candidate - baseline) / baseline * 100
			}
			if candidate > baseline+opts.MaxQualityRegressionAbs {
				failed = append(failed, "quality_"+name)
			}
		} else if ceiling, ok := qualityCeilings[name]; ok {
			row["ceiling"] = ceiling
			row["delta_abs"] = candidate - ceiling
			if candidate > ceiling {
				failed = append(failed, "quality_"+name)
			}
		}
		quality[name] = row
	}
	return quality, failed, skipped
}

func costPerHour(b RunBundle) float64 {
	if b.CostPerProcessedHour > 0 {
		return b.CostPerProcessedHour
	}
	if b.Metrics.Denominators != nil {
		if ah, ok := b.Metrics.Denominators["audio_hours"].(float64); ok && ah > 0 && b.TraceSummary.CostWaterfall.TotalUSD > 0 {
			return b.TraceSummary.CostWaterfall.TotalUSD / ah
		}
	}
	return 0
}

// waterfallResidual checks (in absolute USD) that the leaf cost buckets explain
// the total cost change. "total" is the headline and "compute" is the aggregate
// of the per-stage leaves, so both are excluded — summing either alongside the
// stage deltas would double-count. A small residual means the waterfall accounts
// for the cost move; a large one means a bucket is missing or mis-scaled.
func waterfallResidual(deltas map[string]float64) float64 {
	sum := 0.0
	for k, v := range deltas {
		if k == "total" || k == "compute" {
			continue
		}
		sum += v
	}
	return deltas["total"] - sum
}

func knobDiff(base, cand RunManifest) *ManifestDiff {
	changed := []string{}
	all := map[string]bool{}
	for k := range base.Knobs {
		all[k] = true
	}
	for k := range cand.Knobs {
		all[k] = true
	}
	for k := range all {
		if base.Knobs[k] != cand.Knobs[k] {
			changed = append(changed, k)
		}
	}
	return &ManifestDiff{
		ChangedKnobs:   changed,
		BaselineKnobs:  base.Knobs,
		CandidateKnobs: cand.Knobs,
	}
}

func buildIntermediates(base, cand RunBundle) map[string]any {
	out := map[string]any{}
	if base.Metrics.Denominators != nil && cand.Metrics.Denominators != nil {
		bh, _ := base.Metrics.Denominators["speech_hours"].(float64)
		ch, _ := cand.Metrics.Denominators["speech_hours"].(float64)
		if bh > 0 && ch > 0 {
			out["speech_hours"] = map[string]float64{
				"baseline": bh, "candidate": ch,
				"delta_pct": roundUSD((ch - bh) / bh * 100),
			}
		}
	}
	return out
}

func buildInsights(manifest RunManifest, base, cand RunBundle, deltas map[string]float64) []string {
	var insights []string
	if bh, ok := base.Metrics.Denominators["speech_hours"].(float64); ok {
		if ch, ok := cand.Metrics.Denominators["speech_hours"].(float64); ok && bh > 0 {
			pct := (ch - bh) / bh * 100
			if math.Abs(pct) >= 3 {
				knob := "config"
				if len(manifest.Knobs) == 1 {
					for k, v := range manifest.Knobs {
						knob = k + "=" + v
					}
				}
				dir := "down"
				if pct > 0 {
					dir = "up"
				}
				insights = append(insights, strings.TrimSpace(
					knob+" changed speech_hours from "+formatFloat(bh)+" to "+formatFloat(ch)+
						" ("+formatFloat(pct)+"%), driving speech_sec "+dir+"."))
			}
		}
	}
	if d, ok := deltas["diar_emb"]; ok && math.Abs(d) >= 0.01 {
		insights = append(insights, "diar_emb_usd delta "+formatFloat(d)+" from waterfall.")
	}
	return insights
}

func formatFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func complexityDelta(knobs map[string]string) int {
	if len(knobs) <= 1 {
		return len(knobs)
	}
	return len(knobs) + 1
}

// fmt import needed - I used fmt.Sprintf in formatFloat - add import

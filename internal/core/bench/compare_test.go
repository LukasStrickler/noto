package bench_test

import (
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/bench"
)

func TestComparable_ProfileMismatch(t *testing.T) {
	base := bench.RunManifest{ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue", SuiteID: "gate_ami@2026-06-18"}
	cand := base
	cand.ExecutionProfile = "local_sherpa"
	ok, reason := bench.Comparable(base, cand, bench.ComparabilityOpts{})
	if ok || reason != "profile_mismatch" {
		t.Fatalf("got ok=%v reason=%q", ok, reason)
	}
}

func TestCompare_UnattributedOver2Pct_Reject(t *testing.T) {
	base := bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: "b", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID:       "gate_ami@2026-06-18",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: 0.0194,
		TraceSummary: bench.TraceSummary{
			CostWaterfall: bench.CostWaterfall{TotalUSD: 1, ComputeUSD: 1, UnattributedPct: 1},
		},
		Metrics: bench.MetricsFile{Denominators: map[string]any{"speech_hours": 11.44, "audio_hours": 11.44}},
	}
	cand := base
	cand.Manifest.RunID = "c"
	cand.Manifest.HypothesisID = "H1"
	cand.Manifest.Knobs = map[string]string{"vad": "on"}
	cand.CostPerProcessedHour = 0.0168
	cand.TraceSummary.CostWaterfall.UnattributedPct = 2.3
	cand.Metrics.Denominators = map[string]any{"speech_hours": 9.8, "audio_hours": 11.44}

	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.Decision != bench.DecisionReject {
		t.Fatalf("decision=%s", cmp.Decision)
	}
}

func TestCheckHypothesisSignature_H1Pass(t *testing.T) {
	base := bench.RunBundle{
		Metrics: bench.MetricsFile{Denominators: map[string]any{"speech_hours": 11.44}},
	}
	cand := bench.RunBundle{
		Metrics: bench.MetricsFile{Denominators: map[string]any{"speech_hours": 9.8}},
	}
	wf := map[string]float64{"diar_emb": -0.028}
	sig := bench.CheckHypothesisSignature("H1", base, cand, wf)
	if !sig.Pass {
		t.Fatalf("signature failed: %+v", sig)
	}
}

func TestCheckHypothesisSignature_H1ForbiddenWarmIdle(t *testing.T) {
	base := bench.RunBundle{Metrics: bench.MetricsFile{Denominators: map[string]any{"speech_hours": 11.44}}}
	cand := bench.RunBundle{Metrics: bench.MetricsFile{Denominators: map[string]any{"speech_hours": 11.44}}}
	wf := map[string]float64{"warm_idle": -0.05}
	sig := bench.CheckHypothesisSignature("H1", base, cand, wf)
	if sig.Pass {
		t.Fatal("expected forbidden warm_idle hit")
	}
}

func TestRejectGateKnobBundle(t *testing.T) {
	m := bench.RunManifest{Tier: "gate", Knobs: map[string]string{"vad": "on", "jobs": "10"}}
	if !bench.RejectGateKnobBundle(m) {
		t.Fatal("expected reject")
	}
}

func TestCompare_QualityRegressionRejectsCheapCandidate(t *testing.T) {
	base := bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: "b", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID:       "gate_ami@2026-06-18",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: 0.0194,
		TraceSummary: bench.TraceSummary{
			CostWaterfall:  bench.CostWaterfall{TotalUSD: 0.0194, ComputeUSD: 0.0194, UnattributedPct: 1},
			ComputeByStage: []bench.StageCost{{Stage: "asr", USD: 0.0194}},
		},
		Metrics: bench.MetricsFile{
			Aggregate:    map[string]float64{"wer": 0.235, "der": 0.113, "cpwer": 0.311},
			Denominators: map[string]any{"audio_hours": 11.44, "speech_hours": 11.44},
		},
	}
	cand := base
	cand.Manifest.RunID = "c"
	cand.CostPerProcessedHour = 0.016
	cand.TraceSummary.CostWaterfall.TotalUSD = 0.016
	cand.TraceSummary.CostWaterfall.ComputeUSD = 0.0194
	cand.TraceSummary.ComputeByStage = []bench.StageCost{{Stage: "asr", USD: 0.016}}
	cand.Metrics.Aggregate = map[string]float64{"wer": 0.28, "der": 0.113, "cpwer": 0.311}

	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.Decision != bench.DecisionReject {
		t.Fatalf("decision=%s guardrails=%v", cmp.Decision, cmp.GuardrailsFailed)
	}
	if len(cmp.GuardrailsFailed) == 0 || cmp.GuardrailsFailed[0] != "quality_wer" {
		t.Fatalf("guardrails=%v", cmp.GuardrailsFailed)
	}
}

func TestCompare_CostUpWaterfallConsistent_NotSpuriousRetry(t *testing.T) {
	// Regression: a gate subsample bills absolute USD over ~1.8 audio-hr, so the
	// per-audio-hour cost and the absolute waterfall deltas have different
	// magnitudes. The residual check must compare absolute-to-absolute and not
	// double-count "compute" against its "asr" leaf — otherwise a clean cost
	// regression spuriously returns retry instead of reject.
	base := bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: "b", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID:       "gate_ami@2026-06-18",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: 0.03139,
		TraceSummary: bench.TraceSummary{
			CostWaterfall:  bench.CostWaterfall{TotalUSD: 0.0577, ComputeUSD: 0.0577, UnattributedPct: 0},
			ComputeByStage: []bench.StageCost{{Stage: "asr", USD: 0.0577}},
			GPU:            &bench.GPUUtilization{BusyPct: 45.8, IdleCostPerAudioHourUSD: 0.0145},
		},
		Metrics: bench.MetricsFile{
			Aggregate:    map[string]float64{"wer": 0.207, "der": 0.084, "cpwer": 0.270},
			Denominators: map[string]any{"audio_hours": 1.836},
		},
	}
	cand := base
	cand.Manifest.RunID = "c"
	cand.Manifest.HypothesisID = "H1"
	cand.Manifest.Knobs = map[string]string{"vad": "on"}
	cand.CostPerProcessedHour = 0.03938
	cand.TraceSummary.CostWaterfall = bench.CostWaterfall{TotalUSD: 0.0724, ComputeUSD: 0.0724, UnattributedPct: 0}
	cand.TraceSummary.ComputeByStage = []bench.StageCost{{Stage: "asr", USD: 0.0724}}
	cand.TraceSummary.GPU = &bench.GPUUtilization{BusyPct: 29.5, IdleCostPerAudioHourUSD: 0.0236}

	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.Decision == bench.DecisionRetry {
		t.Fatalf("cost regression should not be a spurious retry; got %s", cmp.Decision)
	}
	if cmp.Decision != bench.DecisionReject {
		t.Fatalf("expected reject (cost up, MDE not met); got %s", cmp.Decision)
	}
	if cmp.GPU == nil || cmp.GPU.DeltaIdleCostPerAudioHourUSD <= 0 {
		t.Fatalf("expected GPU block showing idle cost rose, got %+v", cmp.GPU)
	}
}

func TestCompare_SurfacesGPUIdleCostMovement(t *testing.T) {
	base := bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: "b", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID:       "gate_ami@2026-06-18",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: 0.0194,
		TraceSummary: bench.TraceSummary{
			CostWaterfall:  bench.CostWaterfall{TotalUSD: 0.0194, ComputeUSD: 0.0194, UnattributedPct: 1},
			ComputeByStage: []bench.StageCost{{Stage: "diar_emb", USD: 0.0194}},
			GPU:            &bench.GPUUtilization{BusyPct: 60, IdleCostPerAudioHourUSD: 0.0050},
		},
		Metrics: bench.MetricsFile{
			Aggregate:    map[string]float64{"wer": 0.235, "der": 0.113, "cpwer": 0.311},
			Denominators: map[string]any{"audio_hours": 2.0, "speech_hours": 2.0},
		},
	}
	cand := base
	cand.Manifest.RunID = "c"
	cand.CostPerProcessedHour = 0.0168
	cand.TraceSummary.CostWaterfall.TotalUSD = 0.0168
	cand.TraceSummary.ComputeByStage = []bench.StageCost{{Stage: "diar_emb", USD: 0.0168}}
	// Candidate kept the card busier → less idle waste.
	cand.TraceSummary.GPU = &bench.GPUUtilization{BusyPct: 78, IdleCostPerAudioHourUSD: 0.0021}

	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.GPU == nil {
		t.Fatal("expected GPU compare block")
	}
	if cmp.GPU.DeltaIdleCostPerAudioHourUSD != -0.0029 {
		t.Fatalf("idle delta=%v want -0.0029", cmp.GPU.DeltaIdleCostPerAudioHourUSD)
	}
	if cmp.GPU.CandidateBusyPct != 78 {
		t.Fatalf("busy=%v", cmp.GPU.CandidateBusyPct)
	}
	foundInsight := false
	for _, ins := range cmp.AttributionInsights {
		if strings.Contains(ins, "gpu idle cost/audio-hr moved down") {
			foundInsight = true
		}
	}
	if !foundInsight {
		t.Fatalf("expected idle-cost insight, got %v", cmp.AttributionInsights)
	}
}

func TestCompare_OneSidedGPUEmitsNoFabricatedInsight(t *testing.T) {
	// A summary-only baseline (no GPU samples) vs a GPU candidate has no real other
	// side: gpuCompare must stay quiet rather than compute an idle-cost delta
	// against a phantom 0 and print a fabricated "regression"/"busy 0%→70%" insight.
	base := bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: "b", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID:       "gate_ami@2026-06-18",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: 0.0194,
		TraceSummary: bench.TraceSummary{
			CostWaterfall:  bench.CostWaterfall{TotalUSD: 0.0194, ComputeUSD: 0.0194, UnattributedPct: 1},
			ComputeByStage: []bench.StageCost{{Stage: "diar_emb", USD: 0.0194}},
			GPU:            nil, // summary-only baseline: no GPU samples
		},
		Metrics: bench.MetricsFile{
			Aggregate:    map[string]float64{"wer": 0.235, "der": 0.113, "cpwer": 0.311},
			Denominators: map[string]any{"audio_hours": 2.0, "speech_hours": 2.0},
		},
	}
	cand := base
	cand.Manifest.RunID = "c"
	cand.TraceSummary.GPU = &bench.GPUUtilization{BusyPct: 70, IdleCostPerAudioHourUSD: 0.005}

	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.GPU != nil {
		t.Fatalf("one-sided GPU must produce no compare block, got %+v", cmp.GPU)
	}
	for _, ins := range cmp.AttributionInsights {
		if strings.Contains(ins, "gpu idle cost/audio-hr moved") {
			t.Fatalf("one-sided GPU must not fabricate an idle-cost insight: %q", ins)
		}
	}
}

func TestWaterfallDeltas_TracksAdditiveBuckets(t *testing.T) {
	// A cost move in an additive bucket WaterfallDeltas used to omit (cold_start)
	// must be tracked, so waterfallResidual accounts for it instead of reading the
	// whole total move as unexplained — which spuriously retried a real win.
	base := bench.TraceSummary{CostWaterfall: bench.CostWaterfall{TotalUSD: 1.0, ComputeUSD: 0.6, ColdStartUSD: 0.4}}
	cand := bench.TraceSummary{CostWaterfall: bench.CostWaterfall{TotalUSD: 0.6, ComputeUSD: 0.6, ColdStartUSD: 0.0}}
	d := bench.WaterfallDeltas(base, cand)
	cs, ok := d["cold_start"]
	if !ok {
		t.Fatalf("cold_start move must be tracked, got %v", d)
	}
	// Residual closes: total - sum(non-total/compute) = total - cold_start = 0.
	if cs != d["total"] {
		t.Fatalf("cold_start delta (%v) should equal total delta (%v) so the residual closes", cs, d["total"])
	}
}

func TestCompare_MissingGateQualityRetries(t *testing.T) {
	base := bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: "b", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID: "gate_ami@2026-06-18", Tier: "gate",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: 0.0194,
		TraceSummary: bench.TraceSummary{
			CostWaterfall:  bench.CostWaterfall{TotalUSD: 0.0194, ComputeUSD: 0.0194, UnattributedPct: 1},
			ComputeByStage: []bench.StageCost{{Stage: "asr", USD: 0.0194}},
		},
		Metrics: bench.MetricsFile{
			Aggregate:    map[string]float64{"wer": 0.235, "der": 0.113, "cpwer": 0.311},
			Denominators: map[string]any{"audio_hours": 11.44, "speech_hours": 11.44},
		},
	}
	cand := base
	cand.Manifest.RunID = "c"
	cand.CostPerProcessedHour = 0.016
	cand.TraceSummary.CostWaterfall.TotalUSD = 0.016
	cand.TraceSummary.ComputeByStage = []bench.StageCost{{Stage: "asr", USD: 0.016}}
	cand.Metrics.Aggregate = map[string]float64{}

	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.Decision != bench.DecisionRetry {
		t.Fatalf("decision=%s skipped=%v", cmp.Decision, cmp.GuardrailsSkipped)
	}
	if cmp.NextExperiment != "score_quality_metrics" {
		t.Fatalf("next=%q", cmp.NextExperiment)
	}
}

// scaleBundle builds a comparable run with the cost decomposition inputs the
// marginal-floor compare needs: total cost, absolute GPU idle cost, and audio
// hours. quality is held equal across runs so only cost moves.
func scaleBundle(runID, hyp string, totalUSD, idleUSD, audioHours float64, knobs map[string]string) bench.RunBundle {
	return bench.RunBundle{
		Manifest: bench.RunManifest{
			RunID: runID, HypothesisID: hyp, Knobs: knobs,
			ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			SuiteID:       "gate_ami@2026-06-18",
			Comparability: bench.ManifestCompat{TargetConcurrency: 10, CacheState: "warm", TraceMode: "summary"},
		},
		CostPerProcessedHour: totalUSD / audioHours,
		TraceSummary: bench.TraceSummary{
			CostWaterfall:  bench.CostWaterfall{TotalUSD: totalUSD, ComputeUSD: totalUSD, UnattributedPct: 0},
			ComputeByStage: []bench.StageCost{{Stage: "asr", USD: totalUSD}},
			GPU:            &bench.GPUUtilization{BusyPct: 60, IdleCostUSD: idleUSD},
		},
		Metrics: bench.MetricsFile{
			Aggregate:    map[string]float64{"wer": 0.207, "der": 0.084, "cpwer": 0.270},
			Denominators: map[string]any{"audio_hours": audioHours},
		},
	}
}

// A lever that cuts BUSY compute lowers the marginal floor — the real mover toward
// $0.01. base: total 0.16 idle 0.04 over 2h → floor 0.06; cand: total 0.13 idle
// 0.04 → floor 0.045. The Scale block and an insight must show the floor fell.
func TestCompare_SurfacesMarginalFloorMovement(t *testing.T) {
	base := scaleBundle("b", "", 0.16, 0.04, 2, nil)
	cand := scaleBundle("c", "H-cost", 0.13, 0.04, 2, map[string]string{"batch": "dense"})
	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.Scale == nil {
		t.Fatal("expected a Scale block from two decomposable runs")
	}
	if cmp.Scale.BaselineFloorUSDPerAudioHour <= cmp.Scale.CandidateFloorUSDPerAudioHour {
		t.Fatalf("candidate floor should be lower: base=%v cand=%v",
			cmp.Scale.BaselineFloorUSDPerAudioHour, cmp.Scale.CandidateFloorUSDPerAudioHour)
	}
	if cmp.Scale.DeltaFloorUSDPerAudioHour >= 0 {
		t.Fatalf("delta floor should be negative (floor fell): %v", cmp.Scale.DeltaFloorUSDPerAudioHour)
	}
	var found bool
	for _, in := range cmp.AttributionInsights {
		if strings.Contains(in, "marginal cost floor moved down") && strings.Contains(in, "asymptotic") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a marginal-floor insight, got %v", cmp.AttributionInsights)
	}
}

// A lever that only trims IDLE (same busy compute) lowers the headline cost but
// NOT the floor — the tool must distinguish the two. base: total 0.16 idle 0.06
// → busy 0.10 → floor 0.05; cand: total 0.14 idle 0.04 → busy 0.10 → floor 0.05.
// Cost per hour drops, floor delta ≈ 0, and no false "floor moved" insight fires.
func TestCompare_IdleOnlyTrimDoesNotMoveFloor(t *testing.T) {
	base := scaleBundle("b", "", 0.16, 0.06, 2, nil)
	cand := scaleBundle("c", "H-idle", 0.14, 0.04, 2, map[string]string{"warm": "keep"})
	cmp := bench.Compare(base, cand, bench.DefaultGateOpts())
	if cmp.Scale == nil {
		t.Fatal("expected a Scale block")
	}
	if got := cmp.Scale.DeltaFloorUSDPerAudioHour; got < -0.0005 || got > 0.0005 {
		t.Fatalf("idle-only trim must leave the floor ~unchanged, got delta %v", got)
	}
	// Headline cost DID improve, proving the distinction is real, not a no-op.
	if cmp.Cost.DeltaPct >= 0 {
		t.Fatalf("expected headline cost to drop, delta_pct=%v", cmp.Cost.DeltaPct)
	}
	for _, in := range cmp.AttributionInsights {
		if strings.Contains(in, "marginal cost floor moved") {
			t.Fatalf("idle-only trim must NOT claim a floor move: %q", in)
		}
	}
}

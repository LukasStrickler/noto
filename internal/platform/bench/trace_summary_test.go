package bench_test

import (
	"path/filepath"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestGoldenPipeline_TraceSummary(t *testing.T) {
	root := corebench.FixtureRoot()
	gp := filepath.Join(root, "golden_pipeline")
	summary, hyp, stderr, stageMap, err := bench.IngestFromFiles(
		filepath.Join(gp, "summary_snippet.json"),
		filepath.Join(gp, "hyp_es2002a.json"),
		filepath.Join(gp, "pyannote_stderr.txt"),
		filepath.Join(root, "pyannote_stage_map_v1.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	trace := bench.IngestGolden(summary, hyp, stderr, stageMap)

	if trace.CostWaterfall.TotalUSD != 0.10 {
		t.Fatalf("total usd: got %v want 0.10", trace.CostWaterfall.TotalUSD)
	}
	if trace.CostWaterfall.UnattributedPct >= 2 {
		t.Fatalf("unattributed_pct too high: %v", trace.CostWaterfall.UnattributedPct)
	}
	foundASR, foundEmb := false, false
	for _, s := range trace.ComputeByStage {
		if s.Stage == "asr" {
			foundASR = true
		}
		if s.Stage == "diar_emb" {
			foundEmb = true
		}
	}
	if !foundASR || !foundEmb {
		t.Fatalf("missing stages: asr=%v diar_emb=%v", foundASR, foundEmb)
	}
}

func TestPyannoteStageMap_UnknownKeyGoesToUnattributed(t *testing.T) {
	m := bench.DefaultPyannoteStageMap
	out := m.MapStages(map[string]float64{"unknown_hook": 100})
	if out["unattributed"] != 100 {
		t.Fatalf("got %v", out)
	}
}

func TestBuildTraceSummary_SummaryOnlyUsesCoarsePipelineStage(t *testing.T) {
	trace := bench.BuildTraceSummary(bench.ModalSummary{
		RunID:            "run_summary_only",
		EstimatedCostUSD: 0.0403,
		DurationMS:       65687,
	}, nil, bench.PyannoteTiming{}, bench.DefaultPyannoteStageMap)

	if trace.CostWaterfall.UnattributedPct != 0 {
		t.Fatalf("unattributed_pct=%v", trace.CostWaterfall.UnattributedPct)
	}
	if trace.CostWaterfall.ComputeUSD != 0.0403 {
		t.Fatalf("compute_usd=%v", trace.CostWaterfall.ComputeUSD)
	}
	if len(trace.ComputeByStage) != 1 {
		t.Fatalf("stages=%v", trace.ComputeByStage)
	}
	stage := trace.ComputeByStage[0]
	if stage.Stage != "e2e_pipeline" || stage.System != "benchmark_harness" {
		t.Fatalf("stage=%+v", stage)
	}
	if stage.WallMS != 65687 {
		t.Fatalf("wall_ms=%v", stage.WallMS)
	}
}

func TestBuildTraceSummary_GPUUtilizationIdleCost(t *testing.T) {
	// 1 GPU-hour at $1.94/hr, 60% busy → 40% idle → $0.776 idle over 1 audio hr.
	trace := bench.BuildTraceSummary(bench.ModalSummary{
		RunID:                 "run_gpu",
		EstimatedCostUSD:      2.0,
		DurationMS:            3600000,
		ProcessedAudioHours:   1.0,
		GPUHourlyUSD:          1.94,
		GPUMeanUtilizationPct: 72.0,
		GPUBusyPct:            60.0,
		GPUPeakUtilizationPct: 100.0,
		GPUPeakMemoryMB:       22300,
	}, nil, bench.PyannoteTiming{}, bench.DefaultPyannoteStageMap)

	if trace.GPU == nil {
		t.Fatal("expected GPU utilization block")
	}
	if trace.GPU.PeakVRAMMB != 22300 || trace.GPU.BusyPct != 60 {
		t.Fatalf("gpu=%+v", trace.GPU)
	}
	if trace.GPU.GPUCostUSD != 1.94 {
		t.Fatalf("gpu_cost_usd=%v want 1.94", trace.GPU.GPUCostUSD)
	}
	if got := trace.GPU.IdleCostUSD; got < 0.775 || got > 0.777 {
		t.Fatalf("idle_cost_usd=%v want ~0.776", got)
	}
	if got := trace.GPU.IdleCostPerAudioHourUSD; got < 0.775 || got > 0.777 {
		t.Fatalf("idle_cost_per_audio_hour_usd=%v want ~0.776", got)
	}
}

func TestBuildTraceSummary_DiarSplitFromHypWhenNoStderr(t *testing.T) {
	// Live shape: hyps carry per-meeting stt/diar walls but the pyannote server's
	// stages_ms stderr was not captured (PyannoteTiming empty). The diar share
	// must come from the hyp's diar wall, not collapse to $0 / all-asr.
	hyps := []bench.MeetingHyp{
		{MeetingID: "ES2005a", STTMS: 1090, DiarMS: 16433, AudioSec: 306.4, SpeechSec: 280},
	}
	trace := bench.BuildTraceSummary(bench.ModalSummary{
		RunID:               "run_live_diar",
		EstimatedCostUSD:    0.10,
		DurationMS:          17000,
		ProcessedAudioHours: 306.4 / 3600,
	}, hyps, bench.PyannoteTiming{}, bench.DefaultPyannoteStageMap)

	var asr, diarEmb float64
	for _, s := range trace.ComputeByStage {
		switch s.Stage {
		case "asr":
			asr = s.USD
		case "diar_emb":
			diarEmb = s.USD
		}
	}
	if diarEmb <= 0 {
		t.Fatalf("diar_emb must get a share from the hyp diar wall, got %v (stages=%+v)", diarEmb, trace.ComputeByStage)
	}
	// diar wall (16433) >> stt wall (1090), so diar must dominate the split.
	if diarEmb <= asr {
		t.Fatalf("diar should dominate: asr=%v diar_emb=%v", asr, diarEmb)
	}
}

func TestBuildTraceSummary_NoGPUSamplesOmitsBlock(t *testing.T) {
	trace := bench.BuildTraceSummary(bench.ModalSummary{
		RunID:            "run_cpu",
		EstimatedCostUSD: 0.04,
		DurationMS:       12000,
	}, nil, bench.PyannoteTiming{}, bench.DefaultPyannoteStageMap)
	if trace.GPU != nil {
		t.Fatalf("expected no GPU block for CPU run, got %+v", trace.GPU)
	}
}

func TestCostWaterfall_SumsToTotal(t *testing.T) {
	trace := corebench.TraceSummary{
		CostWaterfall: corebench.CostWaterfall{
			TotalUSD: 1.0, ComputeUSD: 0.98, UnattributedUSD: 0.02, UnattributedPct: 2,
		},
	}
	if trace.CostWaterfall.ComputeUSD+trace.CostWaterfall.UnattributedUSD != trace.CostWaterfall.TotalUSD {
		t.Fatal("waterfall mismatch")
	}
}

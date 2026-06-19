package bench_test

import (
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestRunner_AuditRun_TopStagesSorted(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	if err := store.WriteJSON("run_audit", "trace_summary.json", corebench.TraceSummary{
		SchemaVersion: corebench.SchemaTraceSummaryV1,
		RunID:         "run_audit",
		CostWaterfall: corebench.CostWaterfall{TotalUSD: 1.2, ComputeUSD: 1.1, UnattributedPct: 1.5},
		ComputeByStage: []corebench.StageCost{
			{Stage: "asr", USD: 0.3},
			{Stage: "diar_emb", USD: 0.7},
			{Stage: "merge", USD: 0.1},
		},
	}); err != nil {
		t.Fatal(err)
	}

	audit, err := runner.AuditRun("run_audit")
	if err != nil {
		t.Fatal(err)
	}
	if audit.TopStages[0].Stage != "diar_emb" {
		t.Fatalf("top stages=%v", audit.TopStages)
	}
	if audit.TotalUSD != 1.2 || audit.UnattributedPct != 1.5 {
		t.Fatalf("audit=%+v", audit)
	}
}

func TestRunner_AuditRun_SurfacesGPUUtilization(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	if err := store.WriteJSON("run_gpu_audit", "trace_summary.json", corebench.TraceSummary{
		SchemaVersion: corebench.SchemaTraceSummaryV1,
		RunID:         "run_gpu_audit",
		CostWaterfall: corebench.CostWaterfall{TotalUSD: 0.2, ComputeUSD: 0.2},
		GPU: &corebench.GPUUtilization{
			BusyPct: 87, MeanUtilizationPct: 90.1, PeakVRAMMB: 22300,
			IdleCostUSD: 0.0025, IdleCostPerAudioHourUSD: 0.0021,
		},
	}); err != nil {
		t.Fatal(err)
	}
	audit, err := runner.AuditRun("run_gpu_audit")
	if err != nil {
		t.Fatal(err)
	}
	if audit.GPU == nil || audit.GPU.BusyPct != 87 || audit.GPU.IdleCostPerAudioHourUSD != 0.0021 {
		t.Fatalf("gpu=%+v", audit.GPU)
	}
}

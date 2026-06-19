package bench_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

// seedRun writes a minimal summary.json for a run and registers it in the ledger
// as a verified production-config run, the shape ScaleProjection fits from.
func seedRun(t *testing.T, store *bench.Store, ledger *bench.Ledger, runID string, hours, cost float64) {
	t.Helper()
	dir := store.RunDir(runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"run_id": runID, "estimated_cost_usd": cost, "processed_audio_hours": hours,
	})
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Append(bench.LedgerEntry{
		SchemaVersion: bench.SchemaLedgerEntryV1, RunID: runID,
		ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue", Decision: "baseline",
	}); err != nil {
		t.Fatal(err)
	}
}

// Two production runs at different scales must fit a fixed/marginal split and let
// the projection answer the 10h-scale and $0.01-reachability questions.
func TestRunner_ScaleProjection_FitsTwoRunsAndProjects(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	// True model: fixed 0.05, marginal 0.012/hr.
	seedRun(t, store, runner.Ledger, "run_gate", 2, 0.05+0.012*2)       // 0.074
	seedRun(t, store, runner.Ledger, "run_anchor", 9.5, 0.05+0.012*9.5) // 0.164

	proj, err := runner.ScaleProjection(0.01, nil)
	if err != nil {
		t.Fatalf("scale projection: %v", err)
	}
	if math.Abs(proj.FixedUSD-0.05) > 1e-3 || math.Abs(proj.MarginalUSDPerAudioHour-0.012) > 1e-3 {
		t.Fatalf("fit fixed=%v marginal=%v, want ~0.05/0.012", proj.FixedUSD, proj.MarginalUSDPerAudioHour)
	}
	// $0.01 is below the 0.012 floor → unreachable by scaling audio alone.
	if proj.TargetReachableByScale {
		t.Fatalf("$0.01 below a 0.012 floor must be unreachable by scale; notes=%v", proj.Notes)
	}
	// The 10h point must be present (default scales) and read its modeled $/hr.
	var got10 bool
	for _, p := range proj.Points {
		if p.AudioHours == 10 {
			got10 = true
			// fixed/10 + marginal = 0.005 + 0.012 = 0.017.
			if math.Abs(p.CostPerAudioHourUSD-0.017) > 1e-3 {
				t.Fatalf("10h $/hr = %v, want ~0.017", p.CostPerAudioHourUSD)
			}
		}
	}
	if !got10 {
		t.Fatalf("projection must include the 10h scale point: %+v", proj.Points)
	}
	// The largest observed run (9.5h) is anchored into the curve as a real point.
	var got95 bool
	for _, p := range proj.Points {
		if math.Abs(p.AudioHours-9.5) < 1e-9 {
			got95 = true
		}
	}
	if !got95 {
		t.Fatalf("largest real run scale (9.5h) must be an explicit point: %+v", proj.Points)
	}
}

// A reachable target (above the marginal floor) reports the crossing scale.
func TestRunner_ScaleProjection_ReachableTargetReportsCrossing(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	// fixed 0.05, marginal 0.006/hr → 0.01 is reachable, crossing at
	// 0.05/(0.01-0.006)=12.5h.
	seedRun(t, store, runner.Ledger, "r1", 2, 0.05+0.006*2)
	seedRun(t, store, runner.Ledger, "r2", 10, 0.05+0.006*10)

	proj, err := runner.ScaleProjection(0.01, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !proj.TargetReachableByScale {
		t.Fatalf("0.01 above a 0.006 floor must be reachable; notes=%v", proj.Notes)
	}
	if math.Abs(proj.HoursToReachTarget-12.5) > 0.1 {
		t.Fatalf("crossing hours = %v, want ~12.5", proj.HoursToReachTarget)
	}
}

// When the largest run carries trace (GPU busy%) + metrics (quality), the
// projection includes a scale-readiness verdict gating the full-anchor run.
func TestRunner_ScaleProjection_PopulatesReadinessFromArtifacts(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	// Two scales, true model fixed 0.05 / marginal 0.012 → 10h cost 0.017 ≤ anchor.
	seedRun(t, store, runner.Ledger, "r_small", 2, 0.074)
	seedRun(t, store, runner.Ledger, "r_big", 9.5, 0.164)
	// r_big is the largest run → readiness reads its busy% + quality. Busy 82%,
	// quality within ceilings → ready.
	if err := store.WriteJSON("r_big", "trace_summary.json", corebench.TraceSummary{
		SchemaVersion: corebench.SchemaTraceSummaryV1,
		GPU:           &corebench.GPUUtilization{BusyPct: 82},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteJSON("r_big", "metrics.json", corebench.MetricsFile{
		Aggregate: map[string]float64{"wer": 0.207, "der": 0.084, "cpwer": 0.270},
	}); err != nil {
		t.Fatal(err)
	}

	proj, err := runner.ScaleProjection(0.01, nil)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Readiness == nil {
		t.Fatal("expected a readiness verdict when trace+metrics are present")
	}
	if !proj.Readiness.Ready {
		t.Fatalf("busy 82%% + good quality + on-track 10h cost must be ready: %+v", proj.Readiness)
	}

	// Drop utilization below the floor → not ready, with a reason.
	if err := store.WriteJSON("r_big", "trace_summary.json", corebench.TraceSummary{
		SchemaVersion: corebench.SchemaTraceSummaryV1,
		GPU:           &corebench.GPUUtilization{BusyPct: 40},
	}); err != nil {
		t.Fatal(err)
	}
	proj, err = runner.ScaleProjection(0.01, nil)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Readiness == nil || proj.Readiness.Ready {
		t.Fatalf("40%% busy must block scaling: %+v", proj.Readiness)
	}
}

func TestRunner_ScaleProjection_NoRunsErrors(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	if _, err := runner.ScaleProjection(0.01, nil); err == nil {
		t.Fatal("expected an error when no production runs are on disk")
	}
}

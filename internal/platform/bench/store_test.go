package bench_test

import (
	"strings"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

// writeTree writes a complete, valid artifact tree for runID and returns the store.
func writeTree(t *testing.T, runID string, cost map[string]any) *bench.Store {
	t.Helper()
	s := bench.NewStore(t.TempDir())
	must := func(name string, v any) {
		if err := s.WriteJSON(runID, name, v); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	must("manifest.json", corebench.RunManifest{
		SchemaVersion:    corebench.SchemaManifestV1,
		RunID:            runID,
		ExecutionProfile: "modal_cuda",
		OperatingMode:    "batch_queue",
	})
	must("compute_audit.json", map[string]any{
		"schema_version": "compute_audit.v1",
		"cost":           cost,
	})
	must("trace_summary.json", corebench.TraceSummary{
		SchemaVersion: corebench.SchemaTraceSummaryV1,
		RunID:         runID,
		CostWaterfall: corebench.CostWaterfall{TotalUSD: 0.22, ComputeUSD: 0.20},
	})
	must("metrics.json", corebench.MetricsFile{
		SchemaVersion: corebench.SchemaMetricsV1,
		RunID:         runID,
		Aggregate:     map[string]float64{"wer": 0.214, "der": 0.103, "cpwer": 0.288},
	})
	must("compare.json", corebench.CompareResult{
		SchemaVersion:  corebench.SchemaCompareV1,
		CandidateRunID: runID,
		Decision:       corebench.DecisionAdopt,
	})
	return s
}

func TestStore_LoadRunBundle_RoundTrip(t *testing.T) {
	s := writeTree(t, "run-1", map[string]any{"cost_per_processed_audio_hour_usd": 0.0194})

	b, err := s.LoadRunBundle("run-1")
	if err != nil {
		t.Fatalf("LoadRunBundle: %v", err)
	}
	if b.Manifest.ExecutionProfile != "modal_cuda" || b.Manifest.OperatingMode != "batch_queue" {
		t.Errorf("manifest not loaded: %+v", b.Manifest)
	}
	if b.CostPerProcessedHour != 0.0194 {
		t.Errorf("CostPerProcessedHour = %v, want 0.0194", b.CostPerProcessedHour)
	}
	if b.TraceSummary.CostWaterfall.TotalUSD != 0.22 {
		t.Errorf("trace not loaded: %+v", b.TraceSummary.CostWaterfall)
	}
	if b.Metrics.Aggregate["wer"] != 0.214 {
		t.Errorf("metrics not loaded: %+v", b.Metrics.Aggregate)
	}
}

func TestStore_LoadRunBundle_NormalizesCostAlias(t *testing.T) {
	// Only the legacy alias is present; NormalizeAliases must promote it so the
	// bundle still carries the headline KPI.
	s := writeTree(t, "run-2", map[string]any{"cost_per_audio_hour_usd": 0.0208})

	b, err := s.LoadRunBundle("run-2")
	if err != nil {
		t.Fatalf("LoadRunBundle: %v", err)
	}
	if b.CostPerProcessedHour != 0.0208 {
		t.Errorf("alias not normalized: CostPerProcessedHour = %v, want 0.0208", b.CostPerProcessedHour)
	}
}

func TestStore_ValidateArtifactTree(t *testing.T) {
	s := writeTree(t, "run-3", map[string]any{"cost_per_processed_audio_hour_usd": 0.0194})
	if err := s.ValidateArtifactTree("run-3"); err != nil {
		t.Fatalf("complete tree should validate: %v", err)
	}
	if err := s.ValidateArtifactTree("missing"); err == nil {
		t.Error("nonexistent run should fail validation")
	}
}

func TestStore_ValidateArtifactTreeExcept_ToleratesSkippedFile(t *testing.T) {
	// A seed-baseline run has no compare.json; only ValidateArtifactTreeExcept
	// may tolerate its absence.
	s := bench.NewStore(t.TempDir())
	for _, name := range []string{"manifest.json", "compute_audit.json", "metrics.json", "trace_summary.json"} {
		if err := s.WriteJSON("seed", name, map[string]any{"schema_version": "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ValidateArtifactTree("seed"); err == nil || !strings.Contains(err.Error(), "compare.json") {
		t.Errorf("expected missing compare.json error, got %v", err)
	}
	if err := s.ValidateArtifactTreeExcept("seed", "compare.json"); err != nil {
		t.Errorf("skip should tolerate missing compare.json: %v", err)
	}
}

func TestStore_WriteCompare_LoadCompare(t *testing.T) {
	s := bench.NewStore(t.TempDir())
	want := corebench.CompareResult{
		SchemaVersion:  corebench.SchemaCompareV1,
		BaselineRunID:  "base",
		CandidateRunID: "cand",
		Decision:       corebench.DecisionNeedsConfirm,
	}
	if err := s.WriteCompare("cand", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadCompare("cand")
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != corebench.DecisionNeedsConfirm || got.BaselineRunID != "base" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

package bench_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestRunner_IntegrationRun_WritesArtifacts(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))

	res, err := runner.Run(context.Background(), bench.RunRequest{
		Manifest: corebench.RunManifest{
			SchemaVersion:    corebench.SchemaManifestV1,
			RunID:            "run_integration_test",
			SuiteID:          "smoke@2026-06-18",
			Tier:             "smoke",
			ExecutionProfile: "modal_cuda",
			OperatingMode:    "batch_queue",
			BudgetUSDCap:     0.25,
			BenchStack:       "bench_cuda",
			Comparability: corebench.ManifestCompat{
				TargetConcurrency: 10,
				CacheState:        "warm_models_cold_audio",
				TraceMode:         "summary",
			},
		},
		IntegrationOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TraceValid {
		t.Fatalf("trace invalid: unattributed=%v", res.UnattributedPct)
	}
	for _, name := range []string{"manifest.json", "compute_audit.json", "trace_summary.json", "metrics.json"} {
		if _, err := os.Stat(filepath.Join(store.RunDir(res.RunID), name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	bundle, err := store.LoadRunBundle(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.CostPerProcessedHour <= 0 {
		t.Fatalf("cost_per_processed_hour=%v", bundle.CostPerProcessedHour)
	}
}

func TestRunner_LedgerAppend_RequiresCompare(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))

	res, err := runner.Run(context.Background(), bench.RunRequest{
		Manifest: corebench.RunManifest{
			SchemaVersion: corebench.SchemaManifestV1, RunID: "run_ledger_test",
			SuiteID: "smoke@2026-06-18", Tier: "smoke",
			ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			BudgetUSDCap: 0.25, BenchStack: "bench_cuda",
			Comparability: corebench.ManifestCompat{TargetConcurrency: 10, TraceMode: "summary"},
		},
		IntegrationOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.LedgerAppend(bench.LedgerAppendRequest{
		RunID: res.RunID, Decision: "adopt",
	})
	if err == nil {
		t.Fatal("expected error without compare.json")
	}
}

func TestRunner_LedgerAppend_RejectsAdoptWhenCompareRejected(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))
	manifest := corebench.RunManifest{
		SchemaVersion: corebench.SchemaManifestV1,
		SuiteID:       "smoke@2026-06-18", Tier: "smoke",
		ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
		BudgetUSDCap: 0.25, BenchStack: "bench_cuda",
		Comparability: corebench.ManifestCompat{TargetConcurrency: 10, TraceMode: "summary"},
	}
	base, err := runner.Run(context.Background(), bench.RunRequest{Manifest: manifest, IntegrationOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	manifest.RunID = "run_rejected_adopt"
	cand, err := runner.Run(context.Background(), bench.RunRequest{Manifest: manifest, IntegrationOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := runner.CompareRuns(context.Background(), base.RunID, cand.RunID, corebench.DefaultGateOpts())
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Decision == corebench.DecisionAdopt {
		t.Fatal("fixture must not adopt")
	}
	_, err = runner.LedgerAppend(bench.LedgerAppendRequest{
		RunID: cand.RunID, BaselineRunID: base.RunID, Decision: "adopt",
	})
	if err == nil {
		t.Fatal("expected adopt to require adoptable compare.json")
	}
}

func TestRunner_LedgerAppend_SeedsFirstBaselineWithoutCompare(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))
	manifest := corebench.RunManifest{
		SchemaVersion: corebench.SchemaManifestV1, RunID: "run_baseline_seed",
		SuiteID: "smoke@2026-06-18", Tier: "smoke",
		ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
		BudgetUSDCap: 0.25, BenchStack: "bench_cuda",
		Comparability: corebench.ManifestCompat{TargetConcurrency: 10, TraceMode: "summary"},
	}
	res, err := runner.Run(context.Background(), bench.RunRequest{Manifest: manifest, IntegrationOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	// First reference winner for (modal_cuda, batch_queue): no compare.json yet.
	entry, err := runner.LedgerAppend(bench.LedgerAppendRequest{
		RunID: res.RunID, Decision: "adopt", HypothesisID: "baseline",
	})
	if err != nil {
		t.Fatalf("baseline seed should succeed without compare.json: %v", err)
	}
	if entry.Decision != "adopt" {
		t.Fatalf("decision=%s", entry.Decision)
	}

	// Once a winner exists, a second adopt for the same tuple — even labelled
	// baseline — must still require an adoptable compare.json. No free pass.
	manifest.RunID = "run_baseline_second"
	res2, err := runner.Run(context.Background(), bench.RunRequest{Manifest: manifest, IntegrationOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.LedgerAppend(bench.LedgerAppendRequest{
		RunID: res2.RunID, Decision: "adopt", HypothesisID: "baseline",
	}); err == nil {
		t.Fatal("second baseline adopt must require compare.json once a winner exists")
	}
}

func TestRunner_AgentLoop_Integration(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))
	manifest := corebench.RunManifest{
		SchemaVersion:    corebench.SchemaManifestV1,
		SuiteID:          "smoke@2026-06-18",
		Tier:             "smoke",
		ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
		BudgetUSDCap: 0.25, BenchStack: "bench_cuda",
		Comparability: corebench.ManifestCompat{TargetConcurrency: 10, TraceMode: "summary"},
	}
	base, err := runner.Run(context.Background(), bench.RunRequest{Manifest: manifest, IntegrationOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	manifest.RunID = "run_candidate_loop"
	cand, err := runner.Run(context.Background(), bench.RunRequest{Manifest: manifest, IntegrationOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := runner.CompareRuns(context.Background(), base.RunID, cand.RunID, corebench.DefaultGateOpts())
	if err != nil {
		t.Fatal(err)
	}
	if cmp.SchemaVersion == "" {
		t.Fatal("empty compare")
	}
	_, err = runner.LedgerAppend(bench.LedgerAppendRequest{
		RunID: cand.RunID, BaselineRunID: base.RunID, Decision: "reject",
	})
	if err != nil {
		t.Fatalf("ledger append: %v", err)
	}
}

func TestModalBenchmarkArgs_StartsWithScriptThenRun(t *testing.T) {
	args := bench.ModalBenchmarkArgs("/repo/scripts/modal_benchmark.py", "/tmp/out", "L40S", corebench.RunManifest{
		SuiteID:       "smoke@2026-06-18",
		OperatingMode: "batch_queue",
		Knobs:         map[string]string{"batch_wait_ms": "25"},
	})
	if len(args) < 2 || args[0] != "/repo/scripts/modal_benchmark.py" || args[1] != "run" {
		t.Fatalf("args=%v", args)
	}
	foundOut := false
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--out" && args[i+1] == "/tmp/out" {
			foundOut = true
		}
	}
	if !foundOut {
		t.Fatalf("missing --out: %v", args)
	}
}

func TestRunner_ModalRunCopiesNestedRawArtifactsAndSummaryKPIs(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	modal := bench.NewModalRunner(store, mustRepoRoot(t))
	script := filepath.Join(t.TempDir(), "fake_modal.sh")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
set -eu
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--out" ]; then
    out="$2"
    shift 2
    continue
  fi
  shift
done
run_id="run_nested_raw"
mkdir -p "$out/$run_id"
cat > "$out/$run_id/summary.json" <<'JSON'
{
  "run_id": "run_nested_raw",
  "estimated_cost_usd": 0.0403,
  "duration_ms": 65687,
  "processed_audio_hours": 0.5,
  "kpis": {
    "e2e.synthetic": {
      "wer_pct": 50.9,
      "der_pct": 9.1,
      "cpwer_pct": 54.1,
      "attribution_tax_pts": 3.2
    }
  }
}
JSON
printf 'stages_ms {"embedding": 100}\n' > "$out/$run_id/setup.log"
printf '{"event":"ok"}\n' > "$out/$run_id/raw.jsonl"
cat "$out/$run_id/summary.json"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	modal.Script = script
	modal.Python = "/bin/sh"
	runner := bench.NewRunner(store)
	runner.Modal = modal

	res, err := runner.Run(context.Background(), bench.RunRequest{
		Manifest: corebench.RunManifest{
			SchemaVersion:    corebench.SchemaManifestV1,
			RunID:            "ignored_run_id",
			SuiteID:          "smoke@2026-06-18",
			Tier:             "smoke",
			ExecutionProfile: "modal_cuda",
			OperatingMode:    "batch_queue",
			BudgetUSDCap:     0.25,
			Comparability:    corebench.ManifestCompat{TraceMode: "summary"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TraceValid {
		t.Fatalf("trace invalid: unattributed=%v", res.UnattributedPct)
	}
	for _, name := range []string{"summary.json", "setup.log", "raw.jsonl"} {
		if _, err := os.Stat(filepath.Join(store.RunDir(res.RunID), name)); err != nil {
			t.Fatalf("missing copied %s: %v", name, err)
		}
	}
	var metrics corebench.MetricsFile
	b, err := os.ReadFile(filepath.Join(store.RunDir(res.RunID), "metrics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &metrics); err != nil {
		t.Fatal(err)
	}
	if got := metrics.Aggregate["wer"]; got != 0.509 {
		t.Fatalf("wer=%v", got)
	}
	if got := metrics.Denominators["metrics_source"]; got != "modal_summary.kpis.e2e.synthetic" {
		t.Fatalf("metrics_source=%v", got)
	}
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := bench.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

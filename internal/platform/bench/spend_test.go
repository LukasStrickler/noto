package bench_test

import (
	"context"
	"errors"
	"testing"
	"time"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestSpendLog_CheckCaps_AgentDaily(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	log := bench.NewSpendLog(store)
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	bench.SetSpendClock(log, fixedClock(now))

	// $4.50 already spent today by agent-001.
	for i := 0; i < 3; i++ {
		if err := log.Record(bench.SpendRecord{RunID: "r", AgentID: "agent-001", EstimatedCostUSD: 1.5}); err != nil {
			t.Fatal(err)
		}
	}

	// $1.00 more would hit $5.50 > $5.00 cap → reject.
	err := log.CheckCaps("agent-001", "", 1.0)
	var capErr *bench.SpendCapError
	if !errors.As(err, &capErr) {
		t.Fatalf("expected SpendCapError, got %v", err)
	}
	if capErr.Scope != "agent" || capErr.Window != "day" {
		t.Fatalf("cap err=%+v", capErr)
	}

	// A different agent is unaffected.
	if err := log.CheckCaps("agent-002", "", 1.0); err != nil {
		t.Fatalf("other agent rejected: %v", err)
	}
	// $0.40 more stays under cap.
	if err := log.CheckCaps("agent-001", "", 0.4); err != nil {
		t.Fatalf("under-cap rejected: %v", err)
	}
}

func TestSpendLog_CheckCaps_HypothesisWeekly(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	log := bench.NewSpendLog(store)
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	bench.SetSpendClock(log, fixedClock(now))

	if err := log.Record(bench.SpendRecord{RunID: "r", HypothesisID: "H1", EstimatedCostUSD: 14.0}); err != nil {
		t.Fatal(err)
	}
	if err := log.CheckCaps("", "H1", 2.0); !errors.As(err, new(*bench.SpendCapError)) {
		t.Fatalf("expected weekly cap rejection, got %v", err)
	}
	if err := log.CheckCaps("", "H1", 0.5); err != nil {
		t.Fatalf("under weekly cap rejected: %v", err)
	}
}

func TestSpendLog_OldSpendOutsideWindowIgnored(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	log := bench.NewSpendLog(store)
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	bench.SetSpendClock(log, fixedClock(now))

	// Spend from two days ago is outside the 24h agent window.
	if err := log.Record(bench.SpendRecord{
		RunID: "old", AgentID: "agent-001", EstimatedCostUSD: 4.9,
		Timestamp: now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := log.CheckCaps("agent-001", "", 1.0); err != nil {
		t.Fatalf("stale spend should not count: %v", err)
	}
}

func TestSpendLog_EmptyIDsSkipCaps(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	log := bench.NewSpendLog(store)
	if err := log.CheckCaps("", "", 1000.0); err != nil {
		t.Fatalf("untagged run should not hit caps: %v", err)
	}
}

func TestRunner_Run_RejectsWhenAgentOverDailyCap(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	bench.SetSpendClock(runner.Spend, fixedClock(now))

	// Pre-load the agent at the cap.
	if err := runner.Spend.Record(bench.SpendRecord{RunID: "prior", AgentID: "agent-001", EstimatedCostUSD: 5.0}); err != nil {
		t.Fatal(err)
	}

	_, err := runner.Run(context.Background(), bench.RunRequest{
		Manifest: corebench.RunManifest{
			SchemaVersion: corebench.SchemaManifestV1, RunID: "run_capped",
			SuiteID: "gate_ami@2026-06-18", Tier: "gate",
			ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			BudgetUSDCap: 1.5, AgentID: "agent-001",
			Comparability: corebench.ManifestCompat{TargetConcurrency: 10, TraceMode: "summary"},
		},
		// Not integration-only → billable → cap check runs before any GPU launch.
	})
	if !errors.As(err, new(*bench.SpendCapError)) {
		t.Fatalf("expected spend cap rejection before GPU launch, got %v", err)
	}
}

func TestRunner_Run_IntegrationOnlyExemptFromCaps(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runner.Modal = bench.NewModalRunner(store, mustRepoRoot(t))
	bench.SetSpendClock(runner.Spend, fixedClock(time.Now()))

	if err := runner.Spend.Record(bench.SpendRecord{RunID: "prior", AgentID: "agent-001", EstimatedCostUSD: 99.0}); err != nil {
		t.Fatal(err)
	}
	res, err := runner.Run(context.Background(), bench.RunRequest{
		Manifest: corebench.RunManifest{
			SchemaVersion: corebench.SchemaManifestV1, RunID: "run_integration_exempt",
			SuiteID: "smoke@2026-06-18", Tier: "smoke",
			ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
			BudgetUSDCap: 0.25, AgentID: "agent-001",
			Comparability: corebench.ManifestCompat{TargetConcurrency: 10, TraceMode: "summary"},
		},
		IntegrationOnly: true,
	})
	if err != nil {
		t.Fatalf("integration-only run should be exempt from caps: %v", err)
	}
	if res.RunID == "" {
		t.Fatal("expected a run id")
	}
	// And it must not have written a spend record.
	recs, err := runner.Spend.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.RunID == "run_integration_exempt" {
			t.Fatal("integration-only run should not record spend")
		}
	}
}

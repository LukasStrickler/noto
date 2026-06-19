package bench_test

import (
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestLedger_AppendOnly_NoRewrite(t *testing.T) {
	dir := t.TempDir()
	store := bench.NewStore(dir)
	ledger := bench.NewLedger(store)
	e := bench.LedgerEntry{
		RunID: "r1", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
		Decision: "adopt", CostPerProcessedAudioHourUSD: 0.0194,
	}
	if err := ledger.Append(e); err != nil {
		t.Fatal(err)
	}
	e.RunID = "r2"
	if err := ledger.Append(e); err != nil {
		t.Fatal(err)
	}
	all, err := ledger.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("len=%d", len(all))
	}
}

func TestLedger_RejectAdoptWhenHistoricalUnverified(t *testing.T) {
	dir := t.TempDir()
	ledger := bench.NewLedger(bench.NewStore(dir))
	err := ledger.Append(bench.LedgerEntry{
		RunID: "r1", ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
		Decision: "adopt", HistoricalUnverified: true,
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStore_ValidateArtifactTree_MissingCompare(t *testing.T) {
	dir := t.TempDir()
	store := bench.NewStore(dir)
	runID := "run_test"
	for _, f := range []string{"manifest.json", "compute_audit.json", "metrics.json", "trace_summary.json"} {
		if err := store.WriteJSON(runID, f, map[string]string{"ok": "1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ValidateArtifactTree(runID); err == nil {
		t.Fatal("expected missing compare.json error")
	}
	_ = filepath.Join(store.RunDir(runID), "compare.json")
}

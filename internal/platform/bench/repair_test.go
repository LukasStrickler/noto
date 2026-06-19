package bench_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func writeHyp(t *testing.T, store *bench.Store, runID, name, content string) {
	t.Helper()
	dir := filepath.Join(store.RunDir(runID), "hyps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRepairPreview_FindsCandidatesFromConfidence(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	// One high-confidence word, then a run of low-confidence words (an entity stretch
	// the model is unsure about) → one candidate repair span.
	writeHyp(t, store, "run-conf", "m1.json", `{
		"meeting_id":"m1","speech_sec":600,
		"words":[
			{"text":"the","start":0,"end":0.5,"confidence":0.98},
			{"text":"kubernetes","start":0.5,"end":1.5,"confidence":0.22},
			{"text":"ingress","start":1.5,"end":2.2,"confidence":0.30}
		]
	}`)

	res, err := runner.RepairPreview("run-conf", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasConfidence {
		t.Fatal("run carries word confidence → HasConfidence should be true")
	}
	if res.CandidateSpans < 1 {
		t.Errorf("expected >=1 candidate span, got %d", res.CandidateSpans)
	}
	if res.BudgetSec != 60 {
		t.Errorf("budget = %v, want 60 (600s speech × 10%%)", res.BudgetSec)
	}
	if res.AttemptSec <= 0 {
		t.Error("a candidate that fits the budget should be attempted")
	}
	if res.ProjectedCostUSD <= 0 {
		t.Error("attempting a candidate should project a non-zero cost")
	}
}

func TestRepairPreview_NoConfidenceReportsNone(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	writeHyp(t, store, "run-noconf", "m1.json", `{
		"meeting_id":"m1","speech_sec":600,
		"words":[{"text":"hello","start":0,"end":0.5},{"text":"world","start":0.5,"end":1.0}]
	}`)

	res, err := runner.RepairPreview("run-noconf", 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if res.HasConfidence {
		t.Error("no confidence field → HasConfidence must be false")
	}
	if res.CandidateSpans != 0 {
		t.Errorf("no confidence → no repair candidates, got %d", res.CandidateSpans)
	}
}

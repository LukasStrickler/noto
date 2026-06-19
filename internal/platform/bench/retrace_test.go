package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

// TestRunner_Retrace_RederivesDiarShareFromStoredHyps reproduces the A6a live
// shape — a completed run whose hyps carry per-meeting diar walls but whose
// pyannote stages_ms stderr was never captured — and asserts that re-deriving the
// trace from the stored raw artifacts (no GPU) gives diar its real share instead
// of collapsing to $0/all-asr. This is the cheap repair path for the on-disk
// artifact of a run made by a pre-fix binary.
func TestRunner_Retrace_RederivesDiarShareFromStoredHyps(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runID := "run_retrace_a6a"
	dir := store.RunDir(runID)
	if err := os.MkdirAll(filepath.Join(dir, "hyps"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeJSON := func(name string, v any) {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeJSON("summary.json", map[string]any{
		"run_id":                runID,
		"estimated_cost_usd":    0.10,
		"duration_ms":           17000,
		"processed_audio_hours": 306.4 / 3600,
	})
	writeJSON("manifest.json", corebench.RunManifest{
		SchemaVersion: corebench.SchemaManifestV1, RunID: runID,
		SuiteID: "gate_ami@2026-06-18", Tier: "gate",
		ExecutionProfile: "modal_cuda", OperatingMode: "batch_queue",
	})
	// Diar wall (16433) >> stt wall (1090): diar must dominate the split.
	if err := os.WriteFile(filepath.Join(dir, "hyps", "ES2005a.json"), []byte(
		`{"meeting_id":"ES2005a","stt_ms":1090,"diar_ms":16433,"audio_sec":306.4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// A setup.log WITHOUT pyannote stages_ms — the live gap that forces the fallback.
	if err := os.WriteFile(filepath.Join(dir, "setup.log"), []byte("starting run\nok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	trace, err := runner.Retrace(runID)
	if err != nil {
		t.Fatalf("retrace: %v", err)
	}

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
	if diarEmb <= asr {
		t.Fatalf("diar should dominate: asr=%v diar_emb=%v", asr, diarEmb)
	}

	// The rewrite must have landed on disk, and re-loadable by the audit path.
	reloaded, err := store.LoadTraceSummary(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ComputeByStage) == 0 {
		t.Fatal("trace_summary.json not rewritten")
	}
	if _, err := os.Stat(filepath.Join(dir, "compute_audit.json")); err != nil {
		t.Fatalf("compute_audit.json not rewritten: %v", err)
	}
}

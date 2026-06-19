package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func conf(v float64) *float64 { return &v }

// The alignment must pair each confident hyp word with whether it matched the
// reference, so a low-confidence wrong word and a high-confidence right word both
// land where calibration expects them.
func TestBuildWordConfidences_AlignsConfidenceToCorrectness(t *testing.T) {
	ref := metrics.Normalize("the cat sat on the mat")
	// hyp: "the cat sit on the mat" — "sit" is a substitution (wrong), and the
	// model is appropriately unsure about it.
	words := []bench.HypWord{
		{Text: "the", Start: 0.0, Confidence: conf(0.99)},
		{Text: "cat", Start: 0.5, Confidence: conf(0.98)},
		{Text: "sit", Start: 1.0, Confidence: conf(0.20)}, // wrong, low confidence
		{Text: "on", Start: 1.5, Confidence: conf(0.97)},
		{Text: "the", Start: 2.0, Confidence: conf(0.99)},
		{Text: "mat", Start: 2.5, Confidence: conf(0.96)},
	}
	got := bench.BuildWordConfidences(ref, words, "overlap")
	if len(got) != 6 {
		t.Fatalf("want 6 scored words, got %d: %+v", len(got), got)
	}
	for i, w := range got {
		if w.Slice != "overlap" {
			t.Errorf("word %d slice=%q, want overlap", i, w.Slice)
		}
	}
	// Index 2 is "sit": the one wrong word, and the only low-confidence one.
	if got[2].Correct {
		t.Errorf("substituted word should be marked incorrect: %+v", got[2])
	}
	if got[2].Confidence != 0.20 {
		t.Errorf("wrong word confidence = %v, want 0.20", got[2].Confidence)
	}
	for i, w := range got {
		if i == 2 {
			continue
		}
		if !w.Correct {
			t.Errorf("word %d (%v) should be correct", i, w)
		}
	}
}

// A word with no confidence still participates in the alignment (so labels stay
// correct) but produces no scored record; a run whose engine emits zero
// confidence yields no records at all.
func TestBuildWordConfidences_SkipsWordsWithoutConfidence(t *testing.T) {
	ref := metrics.Normalize("alpha beta gamma")
	mixed := []bench.HypWord{
		{Text: "alpha", Start: 0, Confidence: conf(0.9)},
		{Text: "beta", Start: 1}, // no confidence
		{Text: "gamma", Start: 2, Confidence: conf(0.8)},
	}
	got := bench.BuildWordConfidences(ref, mixed, "")
	if len(got) != 2 {
		t.Fatalf("want only the 2 confident words scored, got %d", len(got))
	}

	none := []bench.HypWord{{Text: "alpha", Start: 0}, {Text: "beta", Start: 1}}
	if got := bench.BuildWordConfidences(metrics.Normalize("alpha beta"), none, ""); got != nil {
		t.Fatalf("no-confidence run must score nothing, got %+v", got)
	}
}

// An inserted hyp word (no reference token) is marked incorrect, so a confidently
// hallucinated word is visible to the high-confidence-error guardrail.
func TestBuildWordConfidences_InsertionIsIncorrect(t *testing.T) {
	ref := metrics.Normalize("hello world")
	words := []bench.HypWord{
		{Text: "hello", Start: 0, Confidence: conf(0.95)},
		{Text: "there", Start: 0.5, Confidence: conf(0.92)}, // inserted, confident
		{Text: "world", Start: 1, Confidence: conf(0.95)},
	}
	got := bench.BuildWordConfidences(ref, words, "")
	if len(got) != 3 || got[1].Correct {
		t.Fatalf("inserted word must be incorrect: %+v", got)
	}
}

func TestWriteCalibration_WritesArtifactOnlyWhenScored(t *testing.T) {
	store := bench.NewStore(t.TempDir())
	runner := bench.NewRunner(store)
	runID := "run_cal"
	if err := os.MkdirAll(store.RunDir(runID), 0o755); err != nil {
		t.Fatal(err)
	}

	// No words -> no artifact, no error.
	if _, written, err := runner.WriteCalibration(runID, nil); err != nil || written {
		t.Fatalf("empty calibration: written=%v err=%v, want false/nil", written, err)
	}
	if _, err := os.Stat(filepath.Join(store.RunDir(runID), "calibration.json")); !os.IsNotExist(err) {
		t.Fatalf("calibration.json must not exist for an unscored run")
	}

	words := []corebench.WordConfidence{
		{Confidence: 0.9, Correct: true}, {Confidence: 0.1, Correct: false},
	}
	rep, written, err := runner.WriteCalibration(runID, words)
	if err != nil || !written {
		t.Fatalf("write calibration: written=%v err=%v", written, err)
	}
	if rep.SchemaVersion != corebench.SchemaCalibrationV1 {
		t.Fatalf("schema=%q", rep.SchemaVersion)
	}
	b, err := os.ReadFile(filepath.Join(store.RunDir(runID), "calibration.json"))
	if err != nil {
		t.Fatalf("read calibration.json: %v", err)
	}
	var onDisk corebench.CalibrationReport
	if err := json.Unmarshal(b, &onDisk); err != nil {
		t.Fatalf("calibration.json invalid: %v", err)
	}
	if onDisk.Words != 2 || onDisk.Errors != 1 {
		t.Fatalf("on-disk words=%d errors=%d, want 2/1", onDisk.Words, onDisk.Errors)
	}
}

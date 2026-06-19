package bench_test

import (
	"math"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want ~%v (tol %v)", name, got, want, tol)
	}
}

// A perfectly calibrated set: every confidence bin's mean confidence equals its
// empirical accuracy, so ECE must be ~0.
func TestECE_PerfectCalibrationIsZero(t *testing.T) {
	var words []corebench.WordConfidence
	// 50 words at confidence 1.0, all correct; 50 at 0.0, all wrong.
	for i := 0; i < 50; i++ {
		words = append(words, corebench.WordConfidence{Confidence: 1.0, Correct: true})
		words = append(words, corebench.WordConfidence{Confidence: 0.0, Correct: false})
	}
	if got := corebench.ECE(words, 10); got > 1e-6 {
		t.Fatalf("ECE perfectly calibrated = %v, want ~0", got)
	}
}

// Overconfident: a bin claims 1.0 confidence but is only 50% accurate → ECE 0.5.
func TestECE_OverconfidentBinSurfacesGap(t *testing.T) {
	var words []corebench.WordConfidence
	for i := 0; i < 100; i++ {
		words = append(words, corebench.WordConfidence{Confidence: 1.0, Correct: i%2 == 0})
	}
	approx(t, "ECE", corebench.ECE(words, 10), 0.5, 1e-6)
}

func TestBrier_PerfectAndWorst(t *testing.T) {
	perfect := []corebench.WordConfidence{
		{Confidence: 1.0, Correct: true},
		{Confidence: 0.0, Correct: false},
	}
	if got := corebench.Brier(perfect); got > 1e-9 {
		t.Fatalf("Brier perfect = %v, want 0", got)
	}
	worst := []corebench.WordConfidence{
		{Confidence: 1.0, Correct: false},
		{Confidence: 0.0, Correct: true},
	}
	approx(t, "Brier worst", corebench.Brier(worst), 1.0, 1e-9)
}

// Errors concentrated at the lowest confidences → bottom decile captures all of
// them; a confidence-blind spread captures only ~the inspected fraction.
func TestBottomDecileCapture_RanksErrorsAboveRandom(t *testing.T) {
	var ranked []corebench.WordConfidence
	for i := 0; i < 100; i++ {
		conf := float64(i) / 100.0
		ranked = append(ranked, corebench.WordConfidence{Confidence: conf, Correct: i >= 10})
	}
	// All 10 errors sit in conf [0.00,0.09] → bottom decile (k=10) captures 10/10.
	approx(t, "ranked capture", corebench.BottomDecileCapture(ranked), 1.0, 1e-9)

	var spread []corebench.WordConfidence
	for i := 0; i < 100; i++ {
		conf := float64(i) / 100.0
		spread = append(spread, corebench.WordConfidence{Confidence: conf, Correct: i%10 != 0})
	}
	// Errors at conf 0.00,0.10,…,0.90 → only the 0.00 error lands in bottom 10.
	approx(t, "spread capture", corebench.BottomDecileCapture(spread), 0.1, 1e-9)
}

func TestBottomDecileCapture_NoErrorsIsZero(t *testing.T) {
	words := []corebench.WordConfidence{{Confidence: 0.5, Correct: true}}
	if got := corebench.BottomDecileCapture(words); got != 0 {
		t.Fatalf("capture with no errors = %v, want 0", got)
	}
}

func TestHighConfidenceErrorRate(t *testing.T) {
	words := []corebench.WordConfidence{
		{Confidence: 0.95, Correct: false}, // high-conf error
		{Confidence: 0.95, Correct: true},
		{Confidence: 0.99, Correct: true},
		{Confidence: 0.99, Correct: true},
		{Confidence: 0.10, Correct: false}, // low-conf, ignored
	}
	// 4 words ≥0.9, 1 wrong → 0.25.
	approx(t, "high-conf err", corebench.HighConfidenceErrorRate(words), 0.25, 1e-9)
}

func TestRiskCoverageAUC_PerfectRankingBeatsInverted(t *testing.T) {
	allCorrect := []corebench.WordConfidence{
		{Confidence: 0.9, Correct: true}, {Confidence: 0.8, Correct: true},
	}
	if got := corebench.RiskCoverageAUC(allCorrect); got != 0 {
		t.Fatalf("AUC all-correct = %v, want 0", got)
	}
	allWrong := []corebench.WordConfidence{
		{Confidence: 0.9, Correct: false}, {Confidence: 0.8, Correct: false},
	}
	approx(t, "AUC all-wrong", corebench.RiskCoverageAUC(allWrong), 1.0, 1e-9)

	// Good ranking (errors least confident) must have lower selective risk than
	// the inverted ranking (errors most confident).
	var good, bad []corebench.WordConfidence
	for i := 0; i < 100; i++ {
		conf := float64(i) / 100.0
		good = append(good, corebench.WordConfidence{Confidence: conf, Correct: i >= 10})
		bad = append(bad, corebench.WordConfidence{Confidence: conf, Correct: i < 90})
	}
	if corebench.RiskCoverageAUC(good) >= corebench.RiskCoverageAUC(bad) {
		t.Fatalf("good ranking AUC %v should be < inverted %v",
			corebench.RiskCoverageAUC(good), corebench.RiskCoverageAUC(bad))
	}
}

func TestBuildCalibrationReport_SchemaAndSlices(t *testing.T) {
	words := []corebench.WordConfidence{
		{Confidence: 0.2, Correct: false, Slice: "overlap"},
		{Confidence: 0.8, Correct: true, Slice: "overlap"},
		{Confidence: 0.9, Correct: true, Slice: "entity"},
		{Confidence: 0.1, Correct: false},
	}
	rep := corebench.BuildCalibrationReport(words)
	if rep.SchemaVersion != corebench.SchemaCalibrationV1 {
		t.Fatalf("schema=%q", rep.SchemaVersion)
	}
	if _, ok := rep.ECEBySlice["default"]; !ok {
		t.Fatal("missing default slice")
	}
	for _, want := range []string{"overlap", "entity"} {
		if _, ok := rep.ECEBySlice[want]; !ok {
			t.Fatalf("missing slice %q in ECEBySlice", want)
		}
	}
	if rep.Words != 4 || rep.Errors != 2 {
		t.Fatalf("words=%d errors=%d", rep.Words, rep.Errors)
	}
}

func TestEvaluateCalibrationGate_AdmitsGoodRejectsRandomAndGuardrail(t *testing.T) {
	// Good signal: errors ranked into the bottom decile, no high-conf errors.
	var good []corebench.WordConfidence
	for i := 0; i < 100; i++ {
		good = append(good, corebench.WordConfidence{Confidence: float64(i) / 100.0, Correct: i >= 10})
	}
	gate := corebench.EvaluateCalibrationGate(corebench.BuildCalibrationReport(good), 0)
	if !gate.SignalAdmissible {
		t.Fatalf("good signal must be admissible, reasons=%v", gate.Reasons)
	}
	if gate.CaptureLiftOverRandom <= 0 {
		t.Fatalf("expected positive lift, got %v", gate.CaptureLiftOverRandom)
	}

	// Confidence-blind spread: capture ~baseline → not admissible.
	var spread []corebench.WordConfidence
	for i := 0; i < 100; i++ {
		spread = append(spread, corebench.WordConfidence{Confidence: float64(i) / 100.0, Correct: i%10 != 0})
	}
	if g := corebench.EvaluateCalibrationGate(corebench.BuildCalibrationReport(spread), 0); g.SignalAdmissible {
		t.Fatal("random-equivalent ranking must NOT be admissible")
	}

	// Good ranking but high-conf error rate breaches the ceiling → rejected.
	withHighConfErr := append([]corebench.WordConfidence(nil), good...)
	for i := 0; i < 5; i++ {
		withHighConfErr = append(withHighConfErr, corebench.WordConfidence{Confidence: 0.99, Correct: false})
	}
	rep := corebench.BuildCalibrationReport(withHighConfErr)
	if g := corebench.EvaluateCalibrationGate(rep, 0.01); g.SignalAdmissible {
		t.Fatalf("guardrail breach must reject; high_conf_err=%v ceiling=0.01", rep.HighConfErrorRate)
	}
}

func TestCalibration_HandlesEmptyAndOutOfRange(t *testing.T) {
	if corebench.ECE(nil, 10) != 0 || corebench.Brier(nil) != 0 || corebench.RiskCoverageAUC(nil) != 0 {
		t.Fatal("empty input must yield 0, not NaN/panic")
	}
	// Out-of-range confidences are clamped, not panicking.
	words := []corebench.WordConfidence{
		{Confidence: 1.7, Correct: true},
		{Confidence: -0.4, Correct: false},
	}
	if got := corebench.Brier(words); got != 0 {
		t.Fatalf("clamped Brier = %v, want 0", got)
	}
}

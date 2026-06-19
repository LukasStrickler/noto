package bench

import (
	"math"
	"sort"
)

// SchemaCalibrationV1 identifies calibration.v1 (§22.13) — the per-run confidence
// snapshot stored in calibration.json and trended in the ledger.
const SchemaCalibrationV1 = "calibration.v1"

// HighConfidenceThreshold is the confidence floor above which an error is a
// "high-confidence error" — the §4.1 guardrail words (≥0.9) whose error rate an
// optimization may never raise.
const HighConfidenceThreshold = 0.9

// BottomDecileFraction is the share of lowest-confidence words the bottom-decile
// capture metric inspects (§4.2). A random or confidence-blind ranking captures
// exactly this fraction of all errors there, so it is also the random/do-nothing
// baseline the B6 gate must beat (§10.2).
const BottomDecileFraction = 0.10

// WordConfidence is one scored word: the model's predicted P(correct) and whether
// the word was actually correct against the reference. The pure calibration
// scorers take a slice of these — no I/O and no transcript shapes (§9 core/bench).
type WordConfidence struct {
	Confidence float64 // P(correct) ∈ [0,1]
	Correct    bool    // true if the word matched the reference
	Slice      string  // optional protected-slice label (language, overlap, entity, …)
}

// ECE is the expected calibration error over `bins` equal-width confidence bins:
// the count-weighted average gap between a bin's mean confidence and its actual
// accuracy. 0 = perfectly calibrated; lower is better (§4.2). bins<=0 defaults to 10.
func ECE(words []WordConfidence, bins int) float64 {
	if len(words) == 0 {
		return 0
	}
	if bins <= 0 {
		bins = 10
	}
	type bucket struct {
		n          int
		sumConf    float64
		sumCorrect float64
	}
	buckets := make([]bucket, bins)
	for _, w := range words {
		c := clamp01(w.Confidence)
		idx := int(c * float64(bins))
		if idx >= bins {
			idx = bins - 1
		}
		buckets[idx].n++
		buckets[idx].sumConf += c
		if w.Correct {
			buckets[idx].sumCorrect++
		}
	}
	n := float64(len(words))
	ece := 0.0
	for _, b := range buckets {
		if b.n == 0 {
			continue
		}
		meanConf := b.sumConf / float64(b.n)
		accuracy := b.sumCorrect / float64(b.n)
		ece += float64(b.n) / n * math.Abs(meanConf-accuracy)
	}
	return roundProb(ece)
}

// Brier is the mean squared error between predicted P(correct) and the 0/1
// outcome. 0 = perfect, 1 = maximally wrong; lower is better (§4.2).
func Brier(words []WordConfidence) float64 {
	if len(words) == 0 {
		return 0
	}
	sum := 0.0
	for _, w := range words {
		y := 0.0
		if w.Correct {
			y = 1
		}
		d := clamp01(w.Confidence) - y
		sum += d * d
	}
	return roundProb(sum / float64(len(words)))
}

// BottomDecileCapture is the fraction of all errors that fall in the lowest-
// confidence decile (§4.2). A random or confidence-blind ranking captures
// ~BottomDecileFraction (0.10); a useful confidence model captures MORE,
// concentrating errors where it is least sure. Higher is better; returns 0 when
// there are no errors to capture.
func BottomDecileCapture(words []WordConfidence) float64 {
	return BottomFractionCapture(words, BottomDecileFraction)
}

// BottomFractionCapture generalises BottomDecileCapture to an arbitrary
// lowest-confidence fraction (e.g. 0.05 for a tighter high-risk bin).
func BottomFractionCapture(words []WordConfidence, frac float64) float64 {
	totalErr := 0
	for _, w := range words {
		if !w.Correct {
			totalErr++
		}
	}
	if totalErr == 0 || len(words) == 0 {
		return 0
	}
	sorted := append([]WordConfidence(nil), words...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Confidence < sorted[j].Confidence })
	k := int(math.Ceil(frac * float64(len(sorted))))
	if k < 1 {
		k = 1
	}
	if k > len(sorted) {
		k = len(sorted)
	}
	captured := 0
	for _, w := range sorted[:k] {
		if !w.Correct {
			captured++
		}
	}
	return roundProb(float64(captured) / float64(totalErr))
}

// HighConfidenceErrorRate is the error rate among words the model marked highly
// confident (≥ HighConfidenceThreshold) — the §4.1 guardrail an optimization may
// not raise. Returns 0 when no words clear the threshold.
func HighConfidenceErrorRate(words []WordConfidence) float64 {
	n, errs := 0, 0
	for _, w := range words {
		if w.Confidence >= HighConfidenceThreshold {
			n++
			if !w.Correct {
				errs++
			}
		}
	}
	if n == 0 {
		return 0
	}
	return roundProb(float64(errs) / float64(n))
}

// RiskCoverageAUC is the mean selective risk: admit words most-confident first
// and average the error rate (risk) among those kept across every coverage level.
// A model that ranks errors well keeps risk low until low-confidence words are
// admitted, so LOWER area is better. This is the curve §10.3 calls "selective
// risk" (do-nothing keeps the run's overall error rate at every coverage).
func RiskCoverageAUC(words []WordConfidence) float64 {
	if len(words) == 0 {
		return 0
	}
	sorted := append([]WordConfidence(nil), words...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Confidence > sorted[j].Confidence })
	cumErr := 0.0
	sumRisk := 0.0
	for i, w := range sorted {
		if !w.Correct {
			cumErr++
		}
		sumRisk += cumErr / float64(i+1)
	}
	return roundProb(sumRisk / float64(len(sorted)))
}

// CalibrationReport is calibration.v1 (§22.13). ECEBySlice/BrierBySlice keys are
// the protected slices (§10.1); "default" is all words. The headline scalars
// cover the whole run. Stored in calibration.json and trended in the ledger.
type CalibrationReport struct {
	SchemaVersion       string             `json:"schema_version"`
	ECEBySlice          map[string]float64 `json:"ece_by_slice"`
	BrierBySlice        map[string]float64 `json:"brier_by_slice,omitempty"`
	BottomDecileCapture float64            `json:"bottom_decile_capture"`
	RiskCoverageAUC     float64            `json:"risk_coverage_auc,omitempty"`
	HighConfErrorRate   float64            `json:"high_conf_error_rate"`
	Words               int                `json:"words,omitempty"`
	Errors              int                `json:"errors,omitempty"`
}

// BuildCalibrationReport scores word-level confidences into calibration.v1. It
// computes headline metrics over all words and ECE/Brier per protected slice —
// every word contributes to "default", and additionally to its own Slice when
// set. Pure: no I/O (§9).
func BuildCalibrationReport(words []WordConfidence) CalibrationReport {
	bySlice := map[string][]WordConfidence{}
	for _, w := range words {
		bySlice["default"] = append(bySlice["default"], w)
		if w.Slice != "" && w.Slice != "default" {
			bySlice[w.Slice] = append(bySlice[w.Slice], w)
		}
	}
	ece := map[string]float64{}
	brier := map[string]float64{}
	for slice, ws := range bySlice {
		ece[slice] = ECE(ws, 10)
		brier[slice] = Brier(ws)
	}
	errs := 0
	for _, w := range words {
		if !w.Correct {
			errs++
		}
	}
	return CalibrationReport{
		SchemaVersion:       SchemaCalibrationV1,
		ECEBySlice:          ece,
		BrierBySlice:        brier,
		BottomDecileCapture: BottomDecileCapture(words),
		RiskCoverageAUC:     RiskCoverageAUC(words),
		HighConfErrorRate:   HighConfidenceErrorRate(words),
		Words:               len(words),
		Errors:              errs,
	}
}

// CalibrationGate is the B6 decision (§10.2): the confidence signal is admissible
// for repair spend ONLY if its bottom-decile error capture beats the random /
// do-nothing baseline (both equal BottomDecileFraction, since a confidence-blind
// ranking captures exactly the inspected fraction of errors), and the §4.1
// high-confidence error guardrail is not breached. This is the gate the repair
// planner (B7/B8) is forbidden to bypass — repair must not use confidence for
// spend decisions until SignalAdmissible is true.
type CalibrationGate struct {
	BottomDecileCapture   float64  `json:"bottom_decile_capture"`
	RandomBaseline        float64  `json:"random_baseline"`
	CaptureLiftOverRandom float64  `json:"capture_lift_over_random"`
	HighConfErrorRate     float64  `json:"high_conf_error_rate"`
	HighConfErrorCeiling  float64  `json:"high_conf_error_ceiling,omitempty"`
	SignalAdmissible      bool     `json:"signal_admissible"`
	Reasons               []string `json:"reasons,omitempty"`
}

// EvaluateCalibrationGate runs the B6 admissibility check on a report.
// highConfCeiling is the high-confidence error rate the run must not exceed
// (typically the prior winner's rate — the "must not rise" guardrail); pass <=0
// to skip the guardrail when there is no baseline yet.
func EvaluateCalibrationGate(rep CalibrationReport, highConfCeiling float64) CalibrationGate {
	g := CalibrationGate{
		BottomDecileCapture:   rep.BottomDecileCapture,
		RandomBaseline:        BottomDecileFraction,
		CaptureLiftOverRandom: roundProb(rep.BottomDecileCapture - BottomDecileFraction),
		HighConfErrorRate:     rep.HighConfErrorRate,
		HighConfErrorCeiling:  roundProb(highConfCeiling),
	}
	beatsBaseline := rep.BottomDecileCapture > BottomDecileFraction
	if !beatsBaseline {
		g.Reasons = append(g.Reasons, "bottom_decile_capture does not beat random/do-nothing baseline")
	}
	guardrailOK := highConfCeiling <= 0 || rep.HighConfErrorRate <= highConfCeiling
	if !guardrailOK {
		g.Reasons = append(g.Reasons, "high_confidence_error_rate exceeds guardrail ceiling")
	}
	g.SignalAdmissible = beatsBaseline && guardrailOK
	return g
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func roundProb(v float64) float64 {
	return math.Round(v*10000) / 10000
}

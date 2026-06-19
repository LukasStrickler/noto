package bench

import (
	"sort"

	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// calibration.go bridges captured hypotheses to the pure B6 calibration scorers
// in core/bench. core/bench does the math (ECE/Brier/capture/risk-coverage,
// §9.6) and knows nothing about transcripts; this file owns the I/O-adjacent
// step the plan assigns to platform/bench (§0.1, §21): align a meeting's hyp
// words to its reference, attach each word's model confidence, and persist the
// run-level calibration.v1 report as calibration.json.
//
// A run only produces calibration.json when its STT engine actually emitted word
// confidence (NeMo on the parakeet-server path, §9.2 B2.6). Runs captured by an
// engine that emits none simply have no calibration artifact — there is nothing
// to score, and a fabricated 0/1 confidence would poison the gate.

// BuildWordConfidences aligns one meeting's captured hyp words to its reference
// tokens and returns a P(correct)+correctness record for every hyp word that
// carries a model confidence. slice labels every record for per-slice
// calibration (§10.1); pass "" for the default-only run.
//
// The full hypothesis (including words without confidence) drives the alignment
// so the correctness labels match the meeting's WER exactly; only the confident
// words become scored records. refTokens must be the normalized reference token
// stream (dataset.Meeting.Reference()), the same shape WER is scored against.
func BuildWordConfidences(refTokens []string, words []HypWord, slice string) []corebench.WordConfidence {
	if len(words) == 0 {
		return nil
	}
	ws := append([]HypWord(nil), words...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Start < ws[j].Start })

	// Per hyp token: its source word's confidence (if any). Normalizing each word
	// separately yields the SAME token stream as normalizing the space-joined
	// transcript (a space always ends a token), so these tokens line up 1:1 with
	// the alignment and with hypWordTokens in the scoring path.
	type tokConf struct {
		conf float64
		has  bool
	}
	var hypTokens []string
	var meta []tokConf
	anyConf := false
	for _, w := range ws {
		mc := tokConf{}
		if w.Confidence != nil {
			mc.conf, mc.has, anyConf = *w.Confidence, true, true
		}
		for _, tok := range metrics.Normalize(w.Text) {
			hypTokens = append(hypTokens, tok)
			meta = append(meta, mc)
		}
	}
	if !anyConf {
		return nil
	}

	labels := metrics.AlignHyp(refTokens, hypTokens)
	out := make([]corebench.WordConfidence, 0, len(hypTokens))
	for i, ok := range labels {
		if !meta[i].has {
			continue
		}
		out = append(out, corebench.WordConfidence{Confidence: meta[i].conf, Correct: ok, Slice: slice})
	}
	return out
}

// WriteCalibration builds the calibration.v1 report from all scored words and
// stores it as calibration.json under the run dir. It returns (report, written,
// err): written is false with a nil error when there were no confident words to
// score, so a no-confidence run leaves no calibration artifact rather than an
// empty one. This is the §9.6 "wire calibration.json into the run artifacts" step.
func (r *Runner) WriteCalibration(runID string, words []corebench.WordConfidence) (corebench.CalibrationReport, bool, error) {
	if len(words) == 0 {
		return corebench.CalibrationReport{}, false, nil
	}
	rep := corebench.BuildCalibrationReport(words)
	if err := r.Store.WriteJSON(runID, "calibration.json", rep); err != nil {
		return rep, false, err
	}
	return rep, true, nil
}

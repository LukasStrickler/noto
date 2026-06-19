package bench

import (
	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair_measure.go is the platform half of benchmark-measured repair (§10.4): it
// scores a meeting's WER and cpWER (the speaker-attributed "who said what" rate)
// BEFORE and AFTER splicing a re-decode of a risky span into the hypothesis, against
// the reference. The pure word surgery + accept/reject rule live in core
// (splice.go); this file does the scoring that decides whether a repair earned its
// compute. GPU-free — the re-decode that produced the replacement happens elsewhere;
// this is the truth check that the edit actually moved the benchmark, not confidence.

// SpliceMeasurement is the benchmark scoring of one repair edit. Negative deltas are
// improvements. WER is the overall word error; CpWER is speaker-attributed (the
// product-critical axis — a fix that lands the words on the wrong speaker is not a fix).
type SpliceMeasurement struct {
	WERBefore   float64 `json:"wer_before"`
	WERAfter    float64 `json:"wer_after"`
	WERDelta    float64 `json:"wer_delta"`
	CpWERBefore float64 `json:"cpwer_before"`
	CpWERAfter  float64 `json:"cpwer_after"`
	CpWERDelta  float64 `json:"cpwer_delta"`
}

// MeasureSplice scores a meeting's WER/cpWER before and after replacing the baseline
// words in [startSec,endSec) with `replacement`, against the reference. The whole
// transcript is re-scored (the benchmark WER is global), so a repair is credited only
// for the net effect it has on the reference error — including any new errors it
// introduces at the splice seams.
func MeasureSplice(ref dataset.Meeting, baseline []HypWord, startSec, endSec float64, replacement []HypWord) SpliceMeasurement {
	after := spliceHypWords(baseline, startSec, endSec, replacement)

	refTokens := ref.Reference()
	werBefore := werRate(metrics.WER(refTokens, hypWordTokens(baseline)))
	werAfter := werRate(metrics.WER(refTokens, hypWordTokens(after)))

	refBySpk := ref.ReferenceBySpeaker()
	cpBefore := werRate(metrics.CpWER(refBySpk, hypWordsBySpeaker(baseline)))
	cpAfter := werRate(metrics.CpWER(refBySpk, hypWordsBySpeaker(after)))

	return SpliceMeasurement{
		WERBefore:   round4(werBefore),
		WERAfter:    round4(werAfter),
		WERDelta:    round4(werAfter - werBefore),
		CpWERBefore: round4(cpBefore),
		CpWERAfter:  round4(cpAfter),
		CpWERDelta:  round4(cpAfter - cpBefore),
	}
}

// Outcome folds a measurement into the §10.4 repair outcome, applying the pure
// accept/reject rule. WER is the primary axis and cpWER the secondary guardrail, so a
// transcription fix that lands the words on the wrong speaker does not get adopted.
func (m SpliceMeasurement) Outcome(span corebench.RepairSpan, method corebench.RepairMethod, costUSD float64) corebench.RepairOutcome {
	return corebench.OutcomeFromDeltas(span, method, costUSD, m.WERDelta, m.CpWERDelta)
}

// spliceHypWords converts to the pure core's TimedWord, splices, and converts back —
// keeping the word surgery in core (testable, no benchmark deps) while this layer
// owns the HypWord shape the run actually stores.
func spliceHypWords(base []HypWord, startSec, endSec float64, repl []HypWord) []HypWord {
	toTimed := func(ws []HypWord) []corebench.TimedWord {
		out := make([]corebench.TimedWord, len(ws))
		for i, w := range ws {
			out[i] = corebench.TimedWord{Text: w.Text, Start: w.Start, End: w.End, Speaker: w.Speaker}
		}
		return out
	}
	spliced := corebench.SpliceSpan(toTimed(base), startSec, endSec, toTimed(repl))
	out := make([]HypWord, len(spliced))
	for i, w := range spliced {
		out[i] = HypWord{Text: w.Text, Start: w.Start, End: w.End, Speaker: w.Speaker}
	}
	return out
}

// werRate is errors / reference length for a single meeting (0 when no reference).
func werRate(r metrics.Result) float64 {
	if r.RefLen <= 0 {
		return 0
	}
	return float64(r.Errors()) / float64(r.RefLen)
}

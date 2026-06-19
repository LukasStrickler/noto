package bench

import (
	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair_diar_measure.go is the diarization half of benchmark-measured repair, the
// mirror of repair_measure.go: it scores a meeting's DER BEFORE and AFTER splicing a
// re-diarization of an overlap span into the baseline turns, against the RTTM
// reference. The pure turn surgery lives in core (diar_splice.go); this file does the
// scoring that decides whether re-diarizing an overlap region actually lowered the
// diarization error. GPU-free — the re-diarization (separate the system channel,
// re-diarize the split) happens elsewhere; this is the truth check on the benchmark.
//
// Unlike transcription, diarization does NOT need a span-local measure. DER does
// optimal speaker PERMUTATION, so clipping to a short single-speaker window makes any
// hyp label map perfectly to the reference → a spurious 0 (the permutation invariance
// that local WER doesn't have). And it isn't needed: DER error is denominated in
// SECONDS, so a multi-second overlap fix moves whole-meeting DER by ~seconds/speech
// (≈ 2/1500), comfortably above the accept threshold — where a 1-word WER fix is
// 1/N_words and rounds away. So whole-meeting DER is both the decision and the KPI.

// DiarSpliceMeasurement is the benchmark scoring of one re-diarization edit. Negative
// deltas are improvements (less diarization error).
type DiarSpliceMeasurement struct {
	DERBefore float64 `json:"der_before"`
	DERAfter  float64 `json:"der_after"`
	DERDelta  float64 `json:"der_delta"`
}

// MeasureDiarSplice scores whole-meeting DER before/after replacing the baseline
// diarization within [startSec,endSec) with `replacement`, against the reference —
// the aggregate "with vs without re-diarization" number (apply every accepted edit,
// then call this once).
func MeasureDiarSplice(refTurns []dataset.Turn, baseline []HypTurn, startSec, endSec float64, replacement []HypTurn) DiarSpliceMeasurement {
	refSegs := dataset.Meeting{Turns: refTurns}.Segments()
	before := hypTurnsToSegments(baseline)
	after := hypTurnsToSegments(spliceHypTurns(baseline, startSec, endSec, replacement))
	db := metrics.DER(refSegs, before, metrics.DefaultDEROptions())
	da := metrics.DER(refSegs, after, metrics.DefaultDEROptions())
	return DiarSpliceMeasurement{
		DERBefore: round4(db.Rate),
		DERAfter:  round4(da.Rate),
		DERDelta:  round4(da.Rate - db.Rate),
	}
}

// Outcome folds a DER measurement into the §10 repair outcome via the shared
// accept/reject rule. DER is the primary axis; there is no secondary regression axis
// for a pure diarization edit, so the secondary delta is 0 (re-diarization is adopted
// only when it lowers DER, never when it washes or regresses).
func (m DiarSpliceMeasurement) Outcome(span corebench.RepairSpan, method corebench.RepairMethod, costUSD float64) corebench.RepairOutcome {
	return corebench.OutcomeFromDeltas(span, method, costUSD, m.DERDelta, 0)
}

// spliceHypTurns converts to the pure core's SpeakerSpan, splices the span, and
// converts back — keeping the turn surgery in core (testable, no benchmark deps)
// while this layer owns the HypTurn shape the run stores. Mirrors spliceHypWords.
func spliceHypTurns(base []HypTurn, startSec, endSec float64, repl []HypTurn) []HypTurn {
	toSpan := func(ts []HypTurn) []corebench.SpeakerSpan {
		out := make([]corebench.SpeakerSpan, len(ts))
		for i, t := range ts {
			out[i] = corebench.SpeakerSpan{Speaker: t.Speaker, Start: t.Start, End: t.End}
		}
		return out
	}
	spliced := corebench.SpliceTurns(toSpan(base), startSec, endSec, toSpan(repl))
	out := make([]HypTurn, len(spliced))
	for i, s := range spliced {
		out[i] = HypTurn{Speaker: s.Speaker, Start: s.Start, End: s.End}
	}
	return out
}

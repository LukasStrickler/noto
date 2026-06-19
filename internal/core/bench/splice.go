package bench

import "sort"

// splice.go is the pure measurement spine of benchmark-measured repair (§10.4): the
// edit a second pass makes — substitute a re-decode of a risky span back into the
// hypothesis — and the rule that decides whether that edit was a win, a wash, or a
// regression. The actual WER/cpWER scoring lives in the platform layer (it needs the
// benchmark metrics package + references); this file owns the word surgery and the
// accept/reject decision, both pure and testable without a GPU. The whole point: a
// repair is judged by whether it moves the REFERENCE error, never the model's
// confidence — confidence only chose WHERE to look.

// TimedWord is one hypothesis word with timing + speaker — the unit a repair pass
// substitutes. Unlike the calibration/repair word types (which carry confidence or
// correctness), this carries the TEXT and speaker so a re-decode can be spliced back
// in and the benchmark re-scored.
type TimedWord struct {
	Text    string
	Start   float64
	End     float64
	Speaker string
}

// SpliceSpan replaces every baseline word whose MIDPOINT falls in [startSec,endSec)
// with the replacement words, returning a time-sorted hypothesis. Using the midpoint
// (not any overlap) makes membership unambiguous — a word is in-span iff its centre
// is, so a word straddling a boundary is neither double-removed nor duplicated. This
// is the core edit of a benchmark-measured repair: drop the suspect words in the
// span, drop in the re-decode, and let the platform re-score WER/cpWER before vs after.
func SpliceSpan(words []TimedWord, startSec, endSec float64, replacement []TimedWord) []TimedWord {
	out := make([]TimedWord, 0, len(words)+len(replacement))
	for _, w := range words {
		if mid := (w.Start + w.End) / 2; mid >= startSec && mid < endSec {
			continue // removed; the replacement covers this region
		}
		out = append(out, w)
	}
	out = append(out, replacement...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out
}

// WordsInSpan returns the baseline words whose midpoint falls in [startSec,endSec) —
// the words a re-decode of that span would replace. Used to bound a span to the words
// it actually covers (e.g. to skip an empty span) before paying to re-decode it.
func WordsInSpan(words []TimedWord, startSec, endSec float64) []TimedWord {
	var in []TimedWord
	for _, w := range words {
		if mid := (w.Start + w.End) / 2; mid >= startSec && mid < endSec {
			in = append(in, w)
		}
	}
	return in
}

// RepairMinImproveAbs is the smallest absolute WER/entity delta that counts as a real
// change. Below it an edit is a WASH — neither accepted nor a negative repair — so
// float noise and trivial equivalent rewrites don't inflate either rate (the §4.1
// negative-repair guardrail must measure genuine regressions, not rounding).
const RepairMinImproveAbs = 0.0005

// OutcomeFromDeltas applies the §10.4 accept/reject rule to a measured splice: a
// repair is ACCEPTED when it improves at least one quality axis (WER or entity,
// negative = better) and regresses neither; it is NEGATIVE when it regresses at least
// one axis and improves neither; a mixed or no-op result is a wash (both false), so it
// counts toward neither the accepted nor the negative-repair tally. Pure — the platform
// feeds it the benchmark deltas it measured.
func OutcomeFromDeltas(span RepairSpan, method RepairMethod, costUSD, werDelta, entityDelta float64) RepairOutcome {
	o := RepairOutcome{Span: span, Method: method, CostUSD: costUSD, WERDelta: werDelta, EntityDelta: entityDelta}
	improved := werDelta <= -RepairMinImproveAbs || entityDelta <= -RepairMinImproveAbs
	regressed := werDelta >= RepairMinImproveAbs || entityDelta >= RepairMinImproveAbs
	switch {
	case improved && !regressed:
		o.Accepted = true
	case regressed && !improved:
		o.Negative = true
	}
	return o
}

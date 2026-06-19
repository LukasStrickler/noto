package bench

import "sort"

// diar_splice.go is the diarization analog of splice.go: the pure turn-interval
// surgery a re-diarization of an overlap region performs. Transcription repair
// replaces WORDS in a span and re-scores WER; diarization repair replaces TURNS in a
// span and re-scores DER/cpWER. They share the accept/reject rule (OutcomeFromDeltas,
// fed DER as primary + cpWER as secondary) — a re-diarization is adopted only when it
// lowers the diarization error without regressing "who said what". No I/O: the runner
// feeds reference/hyp speaker spans (the same SpeakerSpan the overlap math uses).

// SpliceTurns replaces the diarization within [startSec,endSec) with `replacement`:
// every baseline turn is clipped to the portion OUTSIDE the span (a turn spanning the
// whole region splits into a head before it and a tail after it; a turn entirely
// inside is dropped), then the replacement turns — which describe the re-diarized
// span — are inserted. The result is time-sorted. This is what splicing a corrected
// overlap segment into the baseline diarization does before re-scoring DER.
func SpliceTurns(turns []SpeakerSpan, startSec, endSec float64, replacement []SpeakerSpan) []SpeakerSpan {
	out := make([]SpeakerSpan, 0, len(turns)+len(replacement))
	if endSec <= startSec {
		// Degenerate span: nothing to clip; keep all baseline turns + replacement.
		for _, t := range turns {
			if t.valid() {
				out = append(out, t)
			}
		}
	} else {
		for _, t := range turns {
			if !t.valid() {
				continue
			}
			// Part before the span.
			if t.Start < startSec {
				head := t
				if head.End > startSec {
					head.End = startSec
				}
				out = append(out, head)
			}
			// Part after the span.
			if t.End > endSec {
				tail := t
				if tail.Start < endSec {
					tail.Start = endSec
				}
				out = append(out, tail)
			}
		}
	}
	for _, r := range replacement {
		if r.valid() {
			out = append(out, r)
		}
	}
	sortTurns(out)
	return out
}

// TurnsInSpan returns the baseline turns that overlap [startSec,endSec), each clipped
// to the span — the local diarization a re-diarization is scored against (the
// diarization analog of the word-span windowing the transcription measure uses).
func TurnsInSpan(turns []SpeakerSpan, startSec, endSec float64) []SpeakerSpan {
	var out []SpeakerSpan
	if endSec <= startSec {
		return out
	}
	for _, t := range turns {
		if !t.valid() {
			continue
		}
		s, e := t.Start, t.End
		if s < startSec {
			s = startSec
		}
		if e > endSec {
			e = endSec
		}
		if e > s {
			out = append(out, SpeakerSpan{Speaker: t.Speaker, Start: s, End: e})
		}
	}
	return out
}

func sortTurns(ts []SpeakerSpan) {
	sort.SliceStable(ts, func(i, j int) bool {
		if ts[i].Start != ts[j].Start {
			return ts[i].Start < ts[j].Start
		}
		return ts[i].End < ts[j].End
	})
}

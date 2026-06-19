package bench

import "sort"

// overlap.go is the PURE core of TARGETED overlap repair — the diarization analog of
// the repair ceiling (repair.go). It exists because of how noto captures audio: TWO
// channels only — the recorder's microphone (you) on one, and ALL remote
// participants mixed onto the system channel. No per-platform hooks. That split is
// the cheap win: you-talking-over-a-participant is already two physical signals, so
// that overlap costs nothing to resolve. The ONLY overlap that needs the expensive
// separate-and-re-transcribe pass is two REMOTE speakers talking at once WITHIN the
// system channel — and that is rare and bounded.
//
// So the system must (1) find those hard-overlap seconds CHEAPLY and (2) spend the
// expensive pass ONLY there — never on the whole meeting. This file is the math that
// proves the discipline pays: how much audio is hard overlap, what repairing only
// those seconds costs versus repairing everywhere, and how much that blanket
// alternative would waste. No I/O — the runner feeds it reference speaker spans
// (from RTTM) or, in production, the diarizer's own segments.

// SpeakerSpan is one speaker active over [Start,End). The overlap math needs only
// timing + identity; it is deliberately NOT benchmark/metrics.Segment so the pure
// core imports nothing from the benchmark harness (the runner converts).
type SpeakerSpan struct {
	Speaker string
	Start   float64
	End     float64
}

func (s SpeakerSpan) valid() bool { return s.End > s.Start }

// OverlapRegion is a maximal time interval where at least the requested number of
// DISTINCT speakers are simultaneously active — a stretch single-pass transcription
// mixes, and that targeted repair would separate and re-transcribe.
type OverlapRegion struct {
	Start       float64 `json:"start_sec"`
	End         float64 `json:"end_sec"`
	MaxSpeakers int     `json:"max_speakers"` // peak concurrent distinct speakers in the region
}

// DurationSec is the region length.
func (r OverlapRegion) DurationSec() float64 {
	if r.End > r.Start {
		return r.End - r.Start
	}
	return 0
}

// SpeechSeconds is the union duration where at least one speaker is active — the
// denominator for the overlap fraction (silence and no-score gaps don't count).
func SpeechSeconds(spans []SpeakerSpan) float64 {
	intervals := make([][2]float64, 0, len(spans))
	for _, s := range spans {
		if s.valid() {
			intervals = append(intervals, [2]float64{s.Start, s.End})
		}
	}
	return unionLength(intervals)
}

// OverlapRegions returns the maximal intervals where >= minSpeakers distinct
// speakers overlap, with each region's peak concurrency. minSpeakers <= 1 is
// treated as 2 (overlap means at least two). It sweeps the atomic intervals between
// every boundary (the der.go idiom) counting DISTINCT speakers — same speaker's
// back-to-back segments never inflate the count — then merges adjacent overlapped
// intervals. O(boundaries · spans): trivial for meeting-sized inputs and obviously
// correct, over clever.
func OverlapRegions(spans []SpeakerSpan, minSpeakers int) []OverlapRegion {
	if minSpeakers < 2 {
		minSpeakers = 2
	}
	bounds := overlapBoundaries(spans)
	var regions []OverlapRegion
	var cur *OverlapRegion
	flush := func() {
		if cur != nil {
			regions = append(regions, *cur)
			cur = nil
		}
	}
	for k := 0; k+1 < len(bounds); k++ {
		t0, t1 := bounds[k], bounds[k+1]
		if t1 <= t0 {
			continue
		}
		mid := (t0 + t1) / 2
		n := distinctActive(spans, mid)
		if n < minSpeakers {
			flush()
			continue
		}
		if cur == nil {
			cur = &OverlapRegion{Start: t0, End: t1, MaxSpeakers: n}
		} else {
			cur.End = t1
			if n > cur.MaxSpeakers {
				cur.MaxSpeakers = n
			}
		}
	}
	flush()
	return regions
}

// OverlapSeconds is the total duration of all overlap regions (>= 2 speakers).
func OverlapSeconds(spans []SpeakerSpan) float64 {
	sec := 0.0
	for _, r := range OverlapRegions(spans, 2) {
		sec += r.DurationSec()
	}
	return sec
}

func overlapBoundaries(spans []SpeakerSpan) []float64 {
	pts := make([]float64, 0, 2*len(spans))
	for _, s := range spans {
		if s.valid() {
			pts = append(pts, s.Start, s.End)
		}
	}
	sort.Float64s(pts)
	out := pts[:0:0]
	for i, t := range pts {
		if i == 0 || t != pts[i-1] {
			out = append(out, t)
		}
	}
	return out
}

func distinctActive(spans []SpeakerSpan, t float64) int {
	set := map[string]struct{}{}
	for _, s := range spans {
		if s.valid() && s.Start <= t && t < s.End {
			set[s.Speaker] = struct{}{}
		}
	}
	return len(set)
}

// unionLength returns the total length covered by a set of intervals, counting
// overlaps once (the speech denominator).
func unionLength(intervals [][2]float64) float64 {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
	total := 0.0
	curStart, curEnd := intervals[0][0], intervals[0][1]
	for _, iv := range intervals[1:] {
		if iv[0] > curEnd {
			total += curEnd - curStart
			curStart, curEnd = iv[0], iv[1]
			continue
		}
		if iv[1] > curEnd {
			curEnd = iv[1]
		}
	}
	total += curEnd - curStart
	return total
}

// OverlapRepairBudget prices the targeted overlap pass against the blanket
// alternative. The separate-and-re-transcribe pass (a separation network plus
// re-decoding the separated streams) costs SepCostFactor times the BASE per-second
// pipeline cost for every second it runs on. Targeted runs it on the measured
// overlap seconds only; blanket would run it across all speech.
type OverlapRepairBudget struct {
	TotalSpeechSec float64 // union speech seconds in the run (the blanket denominator)
	OverlapSec     float64 // hard-overlap seconds the cheap detector found
	BaseCostPerSec float64 // base pipeline $/sec = cost_per_audio_hour / 3600
	SepCostFactor  float64 // separation+re-decode cost as a multiple of base $/sec
	CapFraction    float64 // never repair more than this fraction of speech (0 = no cap)
}

// OverlapRepairCost is the decision artifact: what targeted overlap repair costs, what
// the blanket alternative would cost, and the multiple between them — the number that
// justifies building the cheap detector instead of separating everything.
type OverlapRepairCost struct {
	OverlapFraction  float64 `json:"overlap_fraction"`   // overlap / speech
	RepairedSec      float64 `json:"repaired_sec"`       // min(overlap, cap·speech)
	CappedSkippedSec float64 `json:"capped_skipped_sec"` // overlap seconds the cap dropped (never silent)
	TargetedExtraUSD float64 `json:"targeted_extra_usd"` // $ added by repairing only overlap
	BlanketExtraUSD  float64 `json:"blanket_extra_usd"`  // $ if every speech second were separated
	BaseCostUSD      float64 `json:"base_cost_usd"`      // base pipeline cost for the run's speech
	TargetedExtraPct float64 `json:"targeted_extra_pct"` // targeted extra as % of base cost
	BlanketExtraPct  float64 `json:"blanket_extra_pct"`  // blanket extra as % of base cost
	SavingsFactor    float64 `json:"savings_factor"`     // blanket / targeted (how many× cheaper)
}

// PlanOverlapRepair computes the targeted-vs-blanket cost. The cap (CapFraction)
// bounds worst-case spend on a pathological all-overlap meeting; seconds the cap
// drops are reported in CappedSkippedSec, never silently lost (mirrors PlanRepairs'
// SkippedBudget discipline).
func PlanOverlapRepair(b OverlapRepairBudget) OverlapRepairCost {
	res := OverlapRepairCost{}
	if b.TotalSpeechSec <= 0 || b.BaseCostPerSec <= 0 || b.SepCostFactor <= 0 {
		return res
	}
	overlap := b.OverlapSec
	if overlap < 0 {
		overlap = 0
	}
	if overlap > b.TotalSpeechSec {
		overlap = b.TotalSpeechSec
	}
	res.OverlapFraction = roundProb(overlap / b.TotalSpeechSec)

	repaired := overlap
	if b.CapFraction > 0 {
		capSec := b.CapFraction * b.TotalSpeechSec
		if repaired > capSec {
			res.CappedSkippedSec = roundUSD(repaired - capSec)
			repaired = capSec
		}
	}
	res.RepairedSec = roundUSD(repaired)

	marginalPerSec := b.SepCostFactor * b.BaseCostPerSec
	res.TargetedExtraUSD = roundUSD(repaired * marginalPerSec)
	res.BlanketExtraUSD = roundUSD(b.TotalSpeechSec * marginalPerSec)
	res.BaseCostUSD = roundUSD(b.TotalSpeechSec * b.BaseCostPerSec)

	if res.BaseCostUSD > 0 {
		res.TargetedExtraPct = roundUSD(res.TargetedExtraUSD / res.BaseCostUSD * 100)
		res.BlanketExtraPct = roundUSD(res.BlanketExtraUSD / res.BaseCostUSD * 100)
	}
	// Savings factor is the blanket/targeted ratio. Both passes share the same
	// per-second rate, so it reduces to speech/repaired — computed from the seconds
	// (not the rounded dollar figures) so the tiny targeted $ can't skew the ratio.
	if repaired > 0 {
		res.SavingsFactor = roundUSD(b.TotalSpeechSec / repaired)
	}
	return res
}

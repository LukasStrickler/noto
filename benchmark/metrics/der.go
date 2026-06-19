package metrics

import "sort"

// Segment is a single-speaker span on a timeline, the unit DER and attribution
// score over. It is intentionally decoupled from artifacts.Segment so this
// package imports nothing from the rest of the tree; callers convert.
type Segment struct {
	Speaker string
	Start   float64
	End     float64
}

// DEROptions tunes diarization scoring.
type DEROptions struct {
	// Collar is the no-score zone in seconds applied on each side of every
	// reference segment boundary (start and end). 0 disables the collar.
	Collar float64
	// SkipOverlap drops intervals where more than one reference speaker is
	// active, scoring only single-speaker reference regions.
	SkipOverlap bool
}

// DefaultDEROptions returns the conventional NIST/pyannote-style defaults: a
// 0.25 s collar with overlapping reference speech scored (the honest worst case
// for meeting audio).
func DefaultDEROptions() DEROptions { return DEROptions{Collar: 0.25, SkipOverlap: false} }

// DERResult breaks diarization error into its three standard components, all in
// seconds, plus the reference speech denominator.
type DERResult struct {
	Missed     float64 // reference speech with too few hypothesis speakers
	FalseAlarm float64 // hypothesis speech with no reference speaker
	Confusion  float64 // reference speech attributed to the wrong speaker
	RefSpeech  float64 // Σ (active reference speakers · duration) over scored time
	Rate       float64 // (Missed+FalseAlarm+Confusion) / RefSpeech
}

// Total returns Missed+FalseAlarm+Confusion (seconds of error).
func (r DERResult) Total() float64 { return r.Missed + r.FalseAlarm + r.Confusion }

// DER computes the diarization error rate of hyp against ref. The optimal
// reference→hypothesis speaker mapping is found by maximizing co-occurrence time
// (over the whole file, collar-independent), then error is accumulated per
// atomic interval following the md-eval / pyannote decomposition:
//
//	missed     = Σ max(0, Nref − Nhyp)·d
//	falseAlarm = Σ max(0, Nhyp − Nref)·d
//	confusion  = Σ (min(Nref, Nhyp) − Ncorrect)·d
//	DER        = (missed + falseAlarm + confusion) / Σ Nref·d
//
// where Nref/Nhyp are the active speaker counts in the interval and Ncorrect is
// the number of reference speakers whose mapped hypothesis speaker is also
// active. This handles overlap (Nref may exceed 1).
func DER(ref, hyp []Segment, opts DEROptions) DERResult {
	mapping := speakerMapping(ref, hyp) // refLabel → hypLabel

	bounds := scoringBoundaries(ref, hyp, opts.Collar)
	var res DERResult
	for k := 0; k+1 < len(bounds); k++ {
		t0, t1 := bounds[k], bounds[k+1]
		d := t1 - t0
		if d <= 0 {
			continue
		}
		mid := (t0 + t1) / 2
		if opts.Collar > 0 && withinCollar(mid, ref, opts.Collar) {
			continue
		}

		refSet := activeSet(ref, mid)
		hypSet := activeSet(hyp, mid)
		nRef, nHyp := len(refSet), len(hypSet)
		if opts.SkipOverlap && nRef > 1 {
			continue
		}

		correct := 0
		for r := range refSet {
			if h, ok := mapping[r]; ok && hypSet[h] {
				correct++
			}
		}

		res.RefSpeech += float64(nRef) * d
		res.Missed += float64(max(0, nRef-nHyp)) * d
		res.FalseAlarm += float64(max(0, nHyp-nRef)) * d
		res.Confusion += float64(min(nRef, nHyp)-correct) * d
	}

	if res.RefSpeech > 0 {
		res.Rate = res.Total() / res.RefSpeech
	}
	return res
}

// speakerMapping returns the reference→hypothesis label assignment that
// maximizes total co-occurrence time, computed over every atomic interval.
func speakerMapping(ref, hyp []Segment) map[string]string {
	refLabels := segmentLabels(ref)
	hypLabels := segmentLabels(hyp)
	refIdx := indexOf(refLabels)
	hypIdx := indexOf(hypLabels)
	if len(refLabels) == 0 || len(hypLabels) == 0 {
		return map[string]string{}
	}

	weight := make([][]float64, len(refLabels))
	for i := range weight {
		weight[i] = make([]float64, len(hypLabels))
	}

	bounds := scoringBoundaries(ref, hyp, 0)
	for k := 0; k+1 < len(bounds); k++ {
		t0, t1 := bounds[k], bounds[k+1]
		d := t1 - t0
		if d <= 0 {
			continue
		}
		mid := (t0 + t1) / 2
		refSet := activeSet(ref, mid)
		hypSet := activeSet(hyp, mid)
		for r := range refSet {
			for h := range hypSet {
				weight[refIdx[r]][hypIdx[h]] += d
			}
		}
	}

	mapping := make(map[string]string)
	for r, h := range maxWeightMapping(weight) {
		mapping[refLabels[r]] = hypLabels[h]
	}
	return mapping
}

// scoringBoundaries returns the sorted, de-duplicated set of timeline split
// points: every segment start/end, plus the ±collar offsets of each reference
// boundary when collar > 0 (so a scored interval never straddles a collar edge).
func scoringBoundaries(ref, hyp []Segment, collar float64) []float64 {
	var pts []float64
	add := func(t float64) { pts = append(pts, t) }
	for _, s := range ref {
		add(s.Start)
		add(s.End)
		if collar > 0 {
			add(s.Start - collar)
			add(s.Start + collar)
			add(s.End - collar)
			add(s.End + collar)
		}
	}
	for _, s := range hyp {
		add(s.Start)
		add(s.End)
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

// withinCollar reports whether t lies within collar of any reference boundary.
func withinCollar(t float64, ref []Segment, collar float64) bool {
	for _, s := range ref {
		if abs(t-s.Start) < collar || abs(t-s.End) < collar {
			return true
		}
	}
	return false
}

// activeSet returns the set of speakers whose segment covers the instant t.
func activeSet(segs []Segment, t float64) map[string]bool {
	set := make(map[string]bool)
	for _, s := range segs {
		if s.Start <= t && t < s.End {
			set[s.Speaker] = true
		}
	}
	return set
}

func segmentLabels(segs []Segment) []string {
	seen := make(map[string]bool)
	var labels []string
	for _, s := range segs {
		if !seen[s.Speaker] {
			seen[s.Speaker] = true
			labels = append(labels, s.Speaker)
		}
	}
	sort.Strings(labels)
	return labels
}

func indexOf(labels []string) map[string]int {
	idx := make(map[string]int, len(labels))
	for i, l := range labels {
		idx[l] = i
	}
	return idx
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

package metrics

import "time"

// AttributionAccuracy returns the fraction of reference speech time that is given
// the correct speaker, under the same optimal reference→hypothesis mapping DER
// uses (no collar). It equals Σ Ncorrect·d / Σ Nref·d, where Ncorrect counts
// reference speakers whose mapped hypothesis speaker is active. 1.0 means every
// second of reference speech was attributed to the right (mapped) speaker; it is
// the diarization complement of confusion+miss and ignores false alarms. Returns
// 0 when there is no reference speech.
func AttributionAccuracy(ref, hyp []Segment) float64 {
	mapping := speakerMapping(ref, hyp)
	bounds := scoringBoundaries(ref, hyp, 0)

	var correctTime, refTime float64
	for k := 0; k+1 < len(bounds); k++ {
		t0, t1 := bounds[k], bounds[k+1]
		d := t1 - t0
		if d <= 0 {
			continue
		}
		mid := (t0 + t1) / 2
		refSet := activeSet(ref, mid)
		hypSet := activeSet(hyp, mid)
		refTime += float64(len(refSet)) * d
		for r := range refSet {
			if h, ok := mapping[r]; ok && hypSet[h] {
				correctTime += d
			}
		}
	}

	if refTime <= 0 {
		return 0
	}
	return correctTime / refTime
}

// Timing records the wall-clock cost of one pipeline stage against the audio it
// processed, the basis for the real-time factor.
type Timing struct {
	Stage        string        // e.g. "stt", "diarize", "embed", "match"
	Wall         time.Duration // wall-clock time the stage took
	AudioSeconds float64       // duration of the audio processed
}

// RTF is the real-time factor: wall seconds ÷ audio seconds. RTF < 1 is faster
// than real time. Returns 0 for non-positive audio duration. RTF is
// hardware-dependent — record the compute backend alongside it.
func (t Timing) RTF() float64 {
	if t.AudioSeconds <= 0 {
		return 0
	}
	return t.Wall.Seconds() / t.AudioSeconds
}

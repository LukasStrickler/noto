package bench

import (
	"fmt"
	"path/filepath"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair_redecode.go is the cheapest concrete ReDecoder (the one GPU seam of the
// B7 attempt+measure loop, §10.4): instead of an on-demand single-span GPU call,
// the "alternate decode" is a SECOND completed run captured with a different decode
// config (e.g. fp32 precision, or a beam/maes strategy), and re-decoding a span
// just slices that run's words for the same meeting+time. This needs no on-demand
// Modal endpoint and no audio slicing — only two runs over the same meetings — so
// the only real GPU spend is producing the alternate run once.
//
// The marginal cost charged per attempted span is STT-ONLY (a second pass re-runs
// transcription, never diarization — §10.7), derived from the alternate run's own
// asr-vs-audio rate. So a TARGETED production re-decode is priced honestly even
// though this validation paid for the whole alternate pass up front: we report what
// repairing only the flagged seconds would cost, not the full second pass.

// runReDecoder satisfies ReDecoder by serving an alternate run's words for a span.
type runReDecoder struct {
	alt                map[string][]HypWord // meeting Key() -> alternate-decode words
	sttCostPerAudioSec float64              // STT-only marginal $ per audio second
}

// newRunReDecoder loads an alternate run's hyps (keyed by meeting Key()) and its
// STT-only cost rate, so re-decoding a span is a pure lookup + slice. A missing
// trace_summary is non-fatal — the cost rate falls back to 0 and the report still
// shows accuracy deltas (the gate's accepted-per-dollar simply can't be judged).
func newRunReDecoder(altDir string) (*runReDecoder, error) {
	hyps, err := loadMeetingHyps(filepath.Join(altDir, "hyps"))
	if err != nil {
		return nil, err
	}
	alt := make(map[string][]HypWord, len(hyps))
	for _, h := range hyps {
		if k := h.Key(); k != "" {
			alt[k] = h.Words
		}
	}
	rate, _ := sttCostPerAudioSec(altDir) // best-effort; 0 when no trace_summary
	return &runReDecoder{alt: alt, sttCostPerAudioSec: rate}, nil
}

// ReDecode returns the alternate run's words whose MIDPOINT falls in [startSec,
// endSec) — the same midpoint rule SpliceSpan uses to decide which baseline words a
// re-decode replaces, so the slice and the splice agree on span membership. Cost is
// the span's audio seconds at the STT-only rate. An unknown meeting is an error
// (the loop skips it cleanly), never a silent empty replacement.
func (d *runReDecoder) ReDecode(meetingID string, startSec, endSec float64, _ corebench.RepairMethod) ([]HypWord, float64, error) {
	ws, ok := d.alt[meetingID]
	if !ok {
		return nil, 0, fmt.Errorf("alternate run has no decode for meeting %q", meetingID)
	}
	var span []HypWord
	for _, w := range ws {
		mid := (w.Start + w.End) / 2
		if mid >= startSec && mid < endSec {
			span = append(span, w)
		}
	}
	cost := 0.0
	if endSec > startSec {
		cost = (endSec - startSec) * d.sttCostPerAudioSec
	}
	return span, cost, nil
}

// sttCostPerAudioSec reads a run's trace_summary.json and returns the STT-only
// marginal dollars per audio-second: total asr stage cost / total audio seconds.
// This is the rate a targeted span re-decode is charged at (STT re-runs, diar does
// not). Returns 0 (no error) when the trace carries no asr cost or no audio.
func sttCostPerAudioSec(runDir string) (float64, error) {
	var ts corebench.TraceSummary
	if err := readJSON(filepath.Join(runDir, "trace_summary.json"), &ts); err != nil {
		return 0, err
	}
	asr, audio := 0.0, 0.0
	for _, m := range ts.PerMeeting {
		asr += m.USDByStage["asr"]
		audio += m.AudioSec
	}
	if audio <= 0 {
		return 0, nil
	}
	return asr / audio, nil
}

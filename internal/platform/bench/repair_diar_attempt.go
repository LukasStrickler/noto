package bench

import (
	"fmt"
	"path/filepath"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair_diar_attempt.go is the diarization counterpart of repair_attempt.go: it
// re-diarizes each meeting's OVERLAP regions (≥2 speakers at once — the hard overlap
// the two-channel capture can't separate for free) and measures the DER each
// re-diarization moves against the reference. DRY-RUN: writes no production
// diarization. `bench overlap` shows ~33% of DER error lives in these regions, so this
// is the HIGH-headroom half of repair (vs <0.5% for same-family transcription
// re-decode). The ReDiarizer is the single GPU seam (source-separate the system
// channel + re-diarize the span); a fake drives the loop in tests, a run-pair drives
// it from a second diarization run.
//
// It REUSES corebench.RepairReport + the B7 gate, which are generic over a primary
// error delta: here that delta carries DER (the transcription loop puts WER there).
// The user-facing DiarAttemptResult renames it NetDERDelta so nothing leaks the
// overload. A re-diarization is adopted only when it lowers DER.

const (
	diarB7MinAcceptedPerUSD = 50.0  // accepted re-diarizations per $ (§14)
	diarB7MaxNegativeRate   = 0.005 // negative rate cap before any production write (§4.1)
)

// ReDiarizer re-diarizes one overlap span of a meeting, returning corrected turns
// (absolute meeting time) and the marginal compute cost in USD. The single GPU seam.
type ReDiarizer interface {
	ReDiarize(meetingID string, startSec, endSec float64) (turns []HypTurn, costUSD float64, err error)
}

// DiarAttemptResult is the run-level diarization-repair report: how much DER a
// targeted re-diarization of the overlap regions recovered (naive apply-all vs the
// oracle ceiling) and the B7 gate. Negative deltas are improvements.
type DiarAttemptResult struct {
	RunID             string   `json:"run_id"`
	AltRunID          string   `json:"alt_run_id,omitempty"`
	MeetingsAttempted int      `json:"meetings_attempted"`
	RegionsAttempted  int      `json:"regions_attempted"`
	RegionsDiffered   int      `json:"regions_differed"`
	AcceptedRepairs   int      `json:"accepted_repairs"`
	NegativeRepairs   int      `json:"negative_repairs"`
	CostUSD           float64  `json:"cost_usd"`
	NetDERDelta       float64  `json:"net_der_delta"`
	CeilingDERDelta   float64  `json:"ceiling_der_delta"`
	CeilingAccepted   int      `json:"ceiling_accepted"`
	GatePass          bool     `json:"gate_pass"`
	GateReasons       []string `json:"gate_reasons,omitempty"`
}

// AttemptDiarRepairs runs the diarization B7 loop over a run using altRunID (a second
// diarization run with a different config — VAD, model, or a separation pass) as the
// re-diarization source. The only GPU spend is producing altRunID once; this call is
// pure I/O over two runs' artifacts + the RTTM references.
func (r *Runner) AttemptDiarRepairs(runID, altRunID string) (DiarAttemptResult, error) {
	res := DiarAttemptResult{RunID: runID, AltRunID: altRunID}
	dec, err := newRunReDiarizer(r.Store.RunDir(altRunID))
	if err != nil {
		return res, err
	}
	repoRoot, err := RepoRoot()
	if err != nil {
		return res, err
	}
	dir := r.Store.RunDir(runID)
	var manifest corebench.RunManifest
	if err := readJSON(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return res, err
	}
	opts := ScoreOptionsForRepo(repoRoot, ResolveSuite(manifest.SuiteID))
	hyps, err := loadMeetingHyps(filepath.Join(dir, "hyps"))
	if err != nil {
		return res, err
	}

	var agg corebench.RepairReport
	for _, h := range hyps {
		id := h.Key()
		if id == "" || len(h.Turns) == 0 {
			continue
		}
		refTurns, err := dataset.LoadRTTM(filepath.Join(opts.RTTMDir, id+".rttm"))
		if err != nil || len(refTurns) == 0 {
			continue
		}
		rep, regions, differed, attempted := attemptDiarMeeting(refTurns, h, dec)
		if !attempted {
			continue
		}
		res.MeetingsAttempted++
		res.RegionsAttempted += regions
		res.RegionsDiffered += differed
		agg = corebench.MergeRepairReports(agg, rep)
	}

	res.AcceptedRepairs = agg.AcceptedRepairs
	res.NegativeRepairs = agg.NegativeRepairs
	res.CostUSD = round4(agg.CostUSD)
	res.NetDERDelta = round4(agg.NetWERDelta) // RepairReport's primary delta carries DER here
	res.CeilingDERDelta = round4(agg.CeilingWERDelta)
	res.CeilingAccepted = agg.CeilingAccepted
	res.GatePass, res.GateReasons = agg.PassesB7Gate(diarB7MinAcceptedPerUSD, diarB7MaxNegativeRate)
	return res, nil
}

// appliedDiarRepair is one accepted re-diarization edit. localDERDelta orders the
// oracle-ceiling greedy commit; repl is kept so the meeting's whole-DER delta is
// measured from edits applied together.
type appliedDiarRepair struct {
	startSec      float64
	endSec        float64
	repl          []HypTurn
	localDERDelta float64
}

// attemptDiarMeeting plans the reference overlap regions, re-diarizes each, measures
// the DER each moves, and folds into a RepairReport (its primary delta carries DER).
// Returns the report, regions attempted, how many got a different re-diarization, and
// whether any were. Pure inputs, so the loop is unit-testable with a fake ReDiarizer.
func attemptDiarMeeting(refTurns []dataset.Turn, h MeetingHyp, dec ReDiarizer) (corebench.RepairReport, int, int, bool) {
	spans := make([]corebench.SpeakerSpan, len(refTurns))
	for i, t := range refTurns {
		spans[i] = corebench.SpeakerSpan{Speaker: t.Speaker, Start: t.StartSeconds, End: t.EndSeconds}
	}
	regions := corebench.OverlapRegions(spans, 2)
	if len(regions) == 0 {
		return corebench.RepairReport{}, 0, 0, false
	}

	var rep corebench.RepairReport
	var accepted []appliedDiarRepair
	differed := 0
	for _, rg := range regions {
		repl, cost, err := dec.ReDiarize(h.Key(), rg.Start, rg.End)
		if err != nil {
			continue // a failed re-diarization is skipped, never crashes the run
		}
		rep.CostUSD += cost
		if diarTurnsDiffer(h.Turns, rg.Start, rg.End, repl) {
			differed++
		}
		m := MeasureDiarSplice(refTurns, h.Turns, rg.Start, rg.End, repl)
		o := m.Outcome(corebench.RepairSpan{StartSec: rg.Start, EndSec: rg.End}, corebench.MethodAlternateDecode, cost)
		if o.Negative {
			rep.NegativeRepairs++
		}
		if o.Accepted {
			rep.AcceptedRepairs++
			rep.AcceptedSec += rg.DurationSec()
			accepted = append(accepted, appliedDiarRepair{startSec: rg.Start, endSec: rg.End, repl: repl, localDERDelta: m.DERDelta})
		}
	}

	if len(accepted) > 0 {
		// Naive: apply EVERY accepted re-diarization and re-score whole-meeting DER.
		after := h.Turns
		for _, a := range accepted {
			after = spliceHypTurns(after, a.startSec, a.endSec, a.repl)
		}
		rep.NetWERDelta = wholeDER(refTurns, after) - wholeDER(refTurns, h.Turns)
		// Oracle ceiling: keep only the edits that lower whole-meeting DER.
		rep.CeilingWERDelta, rep.CeilingAccepted = diarOracleCeiling(refTurns, h.Turns, accepted)
	}
	return rep, len(regions), differed, true
}

// diarOracleCeiling is the diarization (DER over HypTurns) view of greedyCeiling: the
// max a re-diarization could buy with perfect region selection. See greedyCeiling.
func diarOracleCeiling(refTurns []dataset.Turn, base []HypTurn, cands []appliedDiarRepair) (float64, int) {
	return greedyCeiling(base, cands,
		func(c appliedDiarRepair) float64 { return c.localDERDelta },
		func(s []HypTurn, c appliedDiarRepair) []HypTurn {
			return spliceHypTurns(s, c.startSec, c.endSec, c.repl)
		},
		func(s []HypTurn) float64 { return wholeDER(refTurns, s) })
}

// wholeDER is the whole-meeting DER of a hypothesis turn list against the reference.
func wholeDER(refTurns []dataset.Turn, turns []HypTurn) float64 {
	refSegs := dataset.Meeting{Turns: refTurns}.Segments()
	return metrics.DER(refSegs, hypTurnsToSegments(turns), metrics.DefaultDEROptions()).Rate
}

// diarTurnsDiffer reports whether the re-diarization changed the set of speakers
// active in the span — the signal that separates "no different diarization to try"
// from "the different turns didn't help" (the diar analog of spanTextDiffers).
func diarTurnsDiffer(baseline []HypTurn, startSec, endSec float64, repl []HypTurn) bool {
	bs := speakerSetInSpan(baseline, startSec, endSec)
	rs := speakerSetInSpan(repl, startSec, endSec)
	if len(bs) != len(rs) {
		return true
	}
	for s := range bs {
		if _, ok := rs[s]; !ok {
			return true
		}
	}
	return false
}

func speakerSetInSpan(turns []HypTurn, startSec, endSec float64) map[string]struct{} {
	set := map[string]struct{}{}
	for _, t := range turns {
		if t.End > startSec && t.Start < endSec {
			set[t.Speaker] = struct{}{}
		}
	}
	return set
}

// runReDiarizer is a ReDiarizer backed by a SECOND completed run's turns: the
// alternate diarization is a whole run captured with a different config (VAD, model,
// or a separation pass), and re-diarizing a span just slices that run's turns for the
// meeting+region, clipped to the span. The diar analog of runReDecoder.
type runReDiarizer struct {
	alt        map[string][]HypTurn
	costPerSec float64
}

func newRunReDiarizer(altDir string) (*runReDiarizer, error) {
	hyps, err := loadMeetingHyps(filepath.Join(altDir, "hyps"))
	if err != nil {
		return nil, err
	}
	alt := make(map[string][]HypTurn, len(hyps))
	for _, h := range hyps {
		if k := h.Key(); k != "" {
			alt[k] = h.Turns
		}
	}
	rate, _ := diarCostPerAudioSec(altDir) // best-effort; 0 when no trace_summary
	return &runReDiarizer{alt: alt, costPerSec: rate}, nil
}

// diarCostPerAudioSec reads a run's trace_summary.json and returns the DIARIZATION-only
// marginal dollars per audio-second: (total − asr) summed over meetings, divided by total
// audio. Using total−asr (rather than the diar stage keys) is robust to the per-stage
// attribution gap where the diar wall is sometimes left unsplit (L1) — diarization is
// simply everything that isn't STT. The diar analog of sttCostPerAudioSec; returns 0 (no
// error) when the trace is missing or carries no audio, so the report still shows DER
// deltas (only accepted-per-dollar can't be judged).
func diarCostPerAudioSec(runDir string) (float64, error) {
	var ts corebench.TraceSummary
	if err := readJSON(filepath.Join(runDir, "trace_summary.json"), &ts); err != nil {
		return 0, err
	}
	diar, audio := 0.0, 0.0
	for _, m := range ts.PerMeeting {
		if d := m.USD - m.USDByStage["asr"]; d > 0 {
			diar += d
		}
		audio += m.AudioSec
	}
	if audio <= 0 {
		return 0, nil
	}
	return diar / audio, nil
}

func (d *runReDiarizer) ReDiarize(meetingID string, startSec, endSec float64) ([]HypTurn, float64, error) {
	turns, ok := d.alt[meetingID]
	if !ok {
		return nil, 0, fmt.Errorf("alternate run has no diarization for meeting %q", meetingID)
	}
	var span []HypTurn
	for _, t := range turns {
		if t.End > startSec && t.Start < endSec {
			s, e := t.Start, t.End
			if s < startSec {
				s = startSec
			}
			if e > endSec {
				e = endSec
			}
			if e > s {
				span = append(span, HypTurn{Speaker: t.Speaker, Start: s, End: e})
			}
		}
	}
	cost := 0.0
	if endSec > startSec {
		cost = (endSec - startSec) * d.costPerSec
	}
	return span, cost, nil
}

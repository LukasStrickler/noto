package bench

import (
	"path/filepath"
	"sort"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// overlap.go wires the pure targeted-overlap-repair model (core/bench/overlap.go) to
// a run's stored hyps and the RTTM references — GPU-free and read-only. It answers
// the two questions that gate ANY spend on overlap separation:
//
//  1. how much HARD overlap (>=2 distinct speakers) is in this run, and
//  2. how much of the diarization error actually LIVES in those overlap regions —
//     the addressable headroom (DER total minus DER-skipping-overlap = the error
//     seconds accumulated where speakers overlap).
//
// Reference overlap is the ORACLE upper bound (max addressable); production would act
// on the cheap live detector over the system channel instead. The answer: "how much
// accuracy could targeted overlap separation recover on this run, and what would it
// cost" — before a dollar of GPU is spent on the separation pass.

const (
	// defaultSepCostFactor prices the separate-and-re-transcribe pass at ~0.5x the
	// base pipeline $/sec it runs on (a separation network + STT on the split
	// streams; STT is ~10% of pipeline cost, so this stays well under 1x). A
	// pre-measurement prior — a B7-style overlap run will replace it with the real rate.
	defaultSepCostFactor = 0.5
	// overlapRepairCapFraction caps targeted repair at 30% of speech so a pathological
	// all-cross-talk meeting can't blow the budget; capped seconds are reported.
	overlapRepairCapFraction = 0.30
	// anchorCostPerAudioHourUSD is the fallback base pipeline cost when a run carries
	// no measured cost (e.g. an integration capture) — the production anchor.
	anchorCostPerAudioHourUSD = 0.0194
)

// OverlapMeetingResult is one meeting's overlap profile + addressable diarization
// error, both derived from the RTTM reference (the oracle).
type OverlapMeetingResult struct {
	MeetingID           string  `json:"meeting_id"`
	SpeechSec           float64 `json:"speech_sec"`
	OverlapSec          float64 `json:"overlap_sec"`
	OverlapFraction     float64 `json:"overlap_fraction"`
	OverlapRegions      int     `json:"overlap_regions"`
	PeakSpeakers        int     `json:"peak_speakers"`
	DER                 float64 `json:"der"`
	DERErrorSec         float64 `json:"der_error_sec"`
	OverlapErrorSec     float64 `json:"overlap_error_sec"`
	AddressableFraction float64 `json:"addressable_fraction"` // overlap error / total error
}

// OverlapAnalysisResult is the run-level "is overlap repair worth it" report: the
// hard-overlap fraction, the addressable diarization error, and the targeted-vs-
// blanket cost — the GPU-free gate for the (later) GPU separation pass.
type OverlapAnalysisResult struct {
	RunID                   string                      `json:"run_id"`
	Meetings                int                         `json:"meetings"`
	Scored                  int                         `json:"scored"`
	HasDiarization          bool                        `json:"has_diarization"`
	BaseCostPerAudioHourUSD float64                     `json:"base_cost_per_audio_hour_usd"`
	SepCostFactor           float64                     `json:"sep_cost_factor"`
	TotalSpeechSec          float64                     `json:"total_speech_sec"`
	TotalOverlapSec         float64                     `json:"total_overlap_sec"`
	OverlapFraction         float64                     `json:"overlap_fraction"`
	PeakSpeakers            int                         `json:"peak_speakers"`
	TotalDERErrorSec        float64                     `json:"total_der_error_sec"`
	OverlapDERErrorSec      float64                     `json:"overlap_der_error_sec"`
	AddressableFraction     float64                     `json:"addressable_fraction"`
	Cost                    corebench.OverlapRepairCost `json:"cost"`
	PerMeeting              []OverlapMeetingResult      `json:"per_meeting"`
}

// OverlapAnalysis loads a run's per-meeting hyps + RTTM references and computes the
// overlap cost and addressable diarization error. sepCostFactor<=0 uses the prior;
// it is the separation pass cost as a multiple of the base pipeline $/sec.
func (r *Runner) OverlapAnalysis(runID string, sepCostFactor float64) (OverlapAnalysisResult, error) {
	if sepCostFactor <= 0 {
		sepCostFactor = defaultSepCostFactor
	}
	res := OverlapAnalysisResult{RunID: runID, SepCostFactor: round4(sepCostFactor)}

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
	res.Meetings = len(hyps)

	// Base pipeline cost: the run's measured rate, else the production anchor.
	res.BaseCostPerAudioHourUSD = runCostPerAudioHour(dir)

	for _, h := range hyps {
		id := h.Key()
		if id == "" {
			continue
		}
		refTurns, err := dataset.LoadRTTM(filepath.Join(opts.RTTMDir, id+".rttm"))
		if err != nil || len(refTurns) == 0 {
			continue
		}
		m := scoreOverlapMeeting(id, h, refTurns)
		res.Scored++
		res.TotalSpeechSec += m.SpeechSec
		res.TotalOverlapSec += m.OverlapSec
		res.TotalDERErrorSec += m.DERErrorSec
		res.OverlapDERErrorSec += m.OverlapErrorSec
		if m.PeakSpeakers > res.PeakSpeakers {
			res.PeakSpeakers = m.PeakSpeakers
		}
		if len(h.Turns) > 0 {
			res.HasDiarization = true
		}
		res.PerMeeting = append(res.PerMeeting, m)
	}
	sort.Slice(res.PerMeeting, func(i, j int) bool { return res.PerMeeting[i].MeetingID < res.PerMeeting[j].MeetingID })

	if res.TotalSpeechSec > 0 {
		res.OverlapFraction = round4(res.TotalOverlapSec / res.TotalSpeechSec)
	}
	if res.TotalDERErrorSec > 0 {
		res.AddressableFraction = round4(res.OverlapDERErrorSec / res.TotalDERErrorSec)
	}
	res.Cost = corebench.PlanOverlapRepair(corebench.OverlapRepairBudget{
		TotalSpeechSec: res.TotalSpeechSec,
		OverlapSec:     res.TotalOverlapSec,
		BaseCostPerSec: res.BaseCostPerAudioHourUSD / 3600,
		SepCostFactor:  sepCostFactor,
		CapFraction:    overlapRepairCapFraction,
	})
	return res, nil
}

// scoreOverlapMeeting computes one meeting's overlap profile and addressable error.
// Overlap regions and speech come from the reference (the oracle); the addressable
// error is the diarization error that occurs in overlap regions — DER total minus
// DER scored with overlap intervals skipped, the same decomposition the metrics
// package already uses, so the two share the speaker mapping and accounting.
func scoreOverlapMeeting(id string, h MeetingHyp, refTurns []dataset.Turn) OverlapMeetingResult {
	spans := make([]corebench.SpeakerSpan, len(refTurns))
	for i, t := range refTurns {
		spans[i] = corebench.SpeakerSpan{Speaker: t.Speaker, Start: t.StartSeconds, End: t.EndSeconds}
	}
	m := OverlapMeetingResult{MeetingID: id, SpeechSec: round4(corebench.SpeechSeconds(spans))}
	for _, rg := range corebench.OverlapRegions(spans, 2) {
		m.OverlapSec += rg.DurationSec()
		m.OverlapRegions++
		if rg.MaxSpeakers > m.PeakSpeakers {
			m.PeakSpeakers = rg.MaxSpeakers
		}
	}
	m.OverlapSec = round4(m.OverlapSec)
	if m.SpeechSec > 0 {
		m.OverlapFraction = round4(m.OverlapSec / m.SpeechSec)
	}

	refSegs := dataset.Meeting{ID: id, Turns: refTurns}.Segments()
	hypSegs := hypTurnsToSegments(h.Turns)
	derFull := metrics.DER(refSegs, hypSegs, metrics.DefaultDEROptions())
	derSkip := metrics.DER(refSegs, hypSegs, metrics.DEROptions{Collar: 0.25, SkipOverlap: true})
	m.DER = round4(derFull.Rate)
	m.DERErrorSec = round4(derFull.Total())
	overlapErr := derFull.Total() - derSkip.Total()
	if overlapErr < 0 {
		overlapErr = 0 // guard float drift; the two scorings share a mapping
	}
	m.OverlapErrorSec = round4(overlapErr)
	if derFull.Total() > 0 {
		m.AddressableFraction = round4(overlapErr / derFull.Total())
	}
	return m
}

// runCostPerAudioHour reads the run's measured base pipeline cost from
// compute_audit.json (best-effort, minimal shape), falling back to the production
// anchor when absent so the cost projection still works on integration captures.
func runCostPerAudioHour(dir string) float64 {
	var audit struct {
		Cost struct {
			CostPerProcessedAudioHourUSD float64 `json:"cost_per_processed_audio_hour_usd"`
		} `json:"cost"`
	}
	if err := readJSON(filepath.Join(dir, "compute_audit.json"), &audit); err == nil && audit.Cost.CostPerProcessedAudioHourUSD > 0 {
		return audit.Cost.CostPerProcessedAudioHourUSD
	}
	return anchorCostPerAudioHourUSD
}

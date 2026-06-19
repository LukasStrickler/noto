package bench

import (
	"path/filepath"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair_attempt.go is the B7 attempt+measure loop (§10.4, DRY-RUN): it actually
// re-decodes the planned low-confidence spans, measures the benchmark WER/cpWER each
// edit moves, and folds the result into the B7 gate — the answer to "how much
// accuracy did repair buy, and at what cost", measured on the REFERENCE, not on
// confidence. It writes NO production transcript; this is exploration + a gate.
//
// Every step is pure and tested offline (planning, splicing, scoring, the gate); the
// ONLY GPU-coupled seam is the ReDecoder, injected so the whole loop runs against a
// fake in tests and against the STT server in a real run.

const (
	// b7MinAcceptedPerUSD: a passing dry-run must land at least this many accepted
	// edits per dollar of repair compute (§14 efficiency gate).
	b7MinAcceptedPerUSD = 50.0
	// b7MaxNegativeRate: negative repairs must stay below this share of accepted
	// edits before any production write is considered (§4.1).
	b7MaxNegativeRate = 0.005
)

// ReDecoder re-transcribes one audio span of a meeting with an alternate method,
// returning the replacement words (timestamps in ABSOLUTE meeting time) and the
// marginal compute cost in USD. The real implementation calls the STT server on the
// span (GPU); a fake drives the loop in tests. This is the single GPU seam in B7.
type ReDecoder interface {
	ReDecode(meetingID string, startSec, endSec float64, method corebench.RepairMethod) (words []HypWord, costUSD float64, err error)
}

// RepairAttemptResult is the run-level B7 attempt+measure report. NetCpWERDelta is the
// speaker-attributed axis (carried in the report's secondary slot); negative deltas
// are improvements. GatePass answers whether the dry-run beat do-nothing efficiently
// enough to justify a production repair (B8) — never auto-applied here.
type RepairAttemptResult struct {
	RunID             string                 `json:"run_id"`
	AltRunID          string                 `json:"alt_run_id,omitempty"`
	Method            string                 `json:"method"`
	MeetingsAttempted int                    `json:"meetings_attempted"`
	SpansAttempted    int                    `json:"spans_attempted"`
	AcceptedRepairs   int                    `json:"accepted_repairs"`
	NegativeRepairs   int                    `json:"negative_repairs"`
	CostUSD           float64                `json:"cost_usd"`
	AcceptedPerUSD    float64                `json:"accepted_per_usd"`
	NegativeRate      float64                `json:"negative_rate"`
	NetWERDelta       float64                `json:"net_wer_delta"`
	NetCpWERDelta     float64                `json:"net_cpwer_delta"`
	GatePass          bool                   `json:"gate_pass"`
	GateReasons       []string               `json:"gate_reasons,omitempty"`
	Report            corebench.RepairReport `json:"report"`
}

// AttemptRepairs runs the B7 attempt+measure loop over a run: per meeting, plan the
// low-confidence spans under a repair budget, re-decode each via dec, measure the
// benchmark delta, and fold into a run-level report + gate. method defaults to the
// cheap same-model alternate decode; threshold<=0 uses the default confidence floor.
func (r *Runner) AttemptRepairs(runID string, dec ReDecoder, threshold float64, method corebench.RepairMethod) (RepairAttemptResult, error) {
	if threshold <= 0 {
		threshold = DefaultRepairThreshold
	}
	if method == "" {
		method = corebench.MethodAlternateDecode
	}
	res := RepairAttemptResult{RunID: runID, Method: string(method)}

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
		if id == "" {
			continue
		}
		ref, err := loadRefMeeting(id, opts)
		if err != nil {
			continue
		}
		rep, spans, attempted := attemptMeeting(ref, h, dec, threshold, method)
		if !attempted {
			continue
		}
		res.MeetingsAttempted++
		res.SpansAttempted += spans
		agg = corebench.MergeRepairReports(agg, rep)
	}

	res.Report = agg
	res.AcceptedRepairs = agg.AcceptedRepairs
	res.NegativeRepairs = agg.NegativeRepairs
	res.CostUSD = round4(agg.CostUSD)
	res.AcceptedPerUSD = round4(agg.AcceptedPerUSD())
	res.NegativeRate = round4(agg.NegativeRate())
	res.NetWERDelta = round4(agg.NetWERDelta)
	res.NetCpWERDelta = round4(agg.NetEntityDelta)
	res.GatePass, res.GateReasons = agg.PassesB7Gate(b7MinAcceptedPerUSD, b7MaxNegativeRate)
	return res, nil
}

// AttemptRepairsFromRun runs the B7 attempt+measure loop using a SECOND completed
// run (altRunID) as the alternate decode source: each low-confidence span in runID
// is re-decoded by slicing altRunID's words for the same meeting+span (see
// runReDecoder). This is the cheapest path to a REAL RepairReport — the only GPU
// spend is producing altRunID once with a different decode config; this call is
// pure I/O over two runs' artifacts. The two runs must cover the same meetings, and
// altRunID should differ in decode (precision/strategy) — an identical decode just
// measures the plumbing (every splice a no-op wash).
func (r *Runner) AttemptRepairsFromRun(runID, altRunID string, threshold float64, method corebench.RepairMethod) (RepairAttemptResult, error) {
	dec, err := newRunReDecoder(r.Store.RunDir(altRunID))
	if err != nil {
		return RepairAttemptResult{RunID: runID, AltRunID: altRunID}, err
	}
	res, err := r.AttemptRepairs(runID, dec, threshold, method)
	res.AltRunID = altRunID
	return res, err
}

// attemptMeeting plans and attempts one meeting's repairs, returning the report, the
// number of spans attempted, and whether any were. Takes the reference + hyp directly
// (no disk), so the full attempt+measure loop is unit-testable with a fake ReDecoder.
func attemptMeeting(ref dataset.Meeting, h MeetingHyp, dec ReDecoder, threshold float64, method corebench.RepairMethod) (corebench.RepairReport, int, bool) {
	words, _ := repairWordsOf(h)
	spans := corebench.SpansFromWords(words, threshold, repairSuccessPrior, true)
	speech := h.SpeechSec
	if speech <= 0 {
		speech = h.AudioSec
	}
	plan := corebench.PlanRepairs(spans, corebench.RepairBudget{
		SpeechSec:     speech,
		Ratio:         0.10,
		CapSec:        300,
		CostPerSecUSD: repairCostPerSecUSD,
	}, repairValuePerUSD)
	if len(plan.Attempt) == 0 {
		return corebench.RepairReport{}, 0, false
	}

	var outcomes []corebench.RepairOutcome
	var accepted []appliedRepair
	attempted := 0
	for _, span := range plan.Attempt {
		repl, cost, err := dec.ReDecode(h.Key(), span.StartSec, span.EndSec, method)
		if err != nil {
			continue // a failed re-decode is skipped, never crashes the run (§B2.6)
		}
		attempted++
		// Decide accept/reject on the SPAN-LOCAL WER/cpWER (sensitive); a single-word
		// fix moves whole-transcript WER by ~1/N and rounds to nothing (§10.4).
		m := MeasureSpliceLocal(ref, h.Words, span.StartSec, span.EndSec, repl)
		o := m.Outcome(span, method, cost)
		outcomes = append(outcomes, o)
		if o.Accepted {
			accepted = append(accepted, appliedRepair{startSec: span.StartSec, endSec: span.EndSec, repl: repl})
		}
	}

	rep := corebench.BuildRepairReport(plan, outcomes)
	// The reported KPI delta is the TRUE whole-transcript move from applying EVERY
	// accepted edit together and re-scoring once — not the sum of per-span local
	// deltas (different denominators). This is the "with vs without repair" number.
	if len(accepted) > 0 {
		after := h.Words
		for _, a := range accepted {
			after = spliceHypWords(after, a.startSec, a.endSec, a.repl)
		}
		agg := measureWhole(ref, h.Words, after)
		rep.NetWERDelta = agg.WERDelta
		rep.NetEntityDelta = agg.CpWERDelta
	}
	return rep, attempted, true
}

// appliedRepair is one accepted span edit, kept so the meeting's whole-transcript KPI
// delta is measured from all edits applied together rather than summed per span.
type appliedRepair struct {
	startSec float64
	endSec   float64
	repl     []HypWord
}

// loadRefMeeting loads a meeting's reference words + diarization turns into the shape
// the scorers want. Mirrors scoreMeetingHyp's reference loading, factored for reuse.
func loadRefMeeting(id string, opts ScoreOptions) (dataset.Meeting, error) {
	refWords, err := dataset.LoadWords(filepath.Join(opts.WordsDir, id+".words.json"))
	if err != nil {
		return dataset.Meeting{}, err
	}
	refTurns, err := dataset.LoadRTTM(filepath.Join(opts.RTTMDir, id+".rttm"))
	if err != nil {
		return dataset.Meeting{}, err
	}
	return dataset.Meeting{ID: id, Words: refWords, Turns: refTurns}, nil
}

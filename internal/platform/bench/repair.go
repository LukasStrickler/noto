package bench

import (
	"path/filepath"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// repair.go wires the pure B7 dry-run repair core (core/bench/repair.go) to a run's
// stored artifacts: it reads the per-meeting hyps, groups low-confidence words into
// candidate spans, and plans them under a repair budget — GPU-free and read-only
// (no re-decode, no production writes, §10.4). The answer to "how much accuracy work
// is on the table for this run, and what would attempting it cost?".

const (
	// DefaultRepairThreshold: words below this confidence are repair candidates.
	DefaultRepairThreshold = 0.5
	// repairCostPerSecUSD is a nominal marginal $ per re-decoded second for the
	// cheap same-model alternate decode — a placeholder for the preview's projected
	// cost until a B7 run measures the real rate.
	repairCostPerSecUSD = 0.00005
	repairValuePerUSD   = 1.0
	repairSuccessPrior  = 0.6 // P(repair fixes a flagged span | alternate decode), pre-B7 prior
)

// RepairPreviewResult is the dry-run candidate ANALYSIS for one run (§10.4): what
// the repair system WOULD attempt, derived from stored word confidences, with no
// GPU and no transcript writes.
type RepairPreviewResult struct {
	RunID               string  `json:"run_id"`
	HasConfidence       bool    `json:"has_confidence"`
	Meetings            int     `json:"meetings"`
	TotalSpeechSec      float64 `json:"total_speech_sec"`
	ConfidenceThreshold float64 `json:"confidence_threshold"`
	CandidateSpans      int     `json:"candidate_spans"`
	CandidateSec        float64 `json:"candidate_sec"`
	AttemptSec          float64 `json:"attempt_sec"`
	SkippedBudgetSec    float64 `json:"skipped_budget_sec"`
	BudgetSec           float64 `json:"budget_sec"`
	ProjectedCostUSD    float64 `json:"projected_cost_usd"`
}

// RepairPreview loads a run's hyps, groups low-confidence words into candidate
// repair spans, and plans them under a default repair budget (10% of speech, 5-min
// cap). HasConfidence is false when the run was captured without word confidence
// (NOTO_PARAKEET_CONFIDENCE unset) — then there are no candidates and the caller
// should re-run with confidence enabled before repair has anything to act on.
func (r *Runner) RepairPreview(runID string, threshold float64) (RepairPreviewResult, error) {
	if threshold <= 0 {
		threshold = DefaultRepairThreshold
	}
	hyps, err := loadMeetingHyps(filepath.Join(r.Store.RunDir(runID), "hyps"))
	if err != nil {
		return RepairPreviewResult{}, err
	}

	res := RepairPreviewResult{RunID: runID, Meetings: len(hyps), ConfidenceThreshold: threshold}
	var spans []corebench.RepairSpan
	for _, h := range hyps {
		// Repair budget is on speech seconds; fall back to total audio when the
		// run carries no VAD-retained speech figure (the common no-VAD case),
		// else the budget is 0 and every candidate is skipped.
		speech := h.SpeechSec
		if speech <= 0 {
			speech = h.AudioSec
		}
		res.TotalSpeechSec += speech
		words, hasConf := repairWordsOf(h)
		if hasConf {
			res.HasConfidence = true
		}
		spans = append(spans, corebench.SpansFromWords(words, threshold, repairSuccessPrior, true)...)
	}

	plan := corebench.PlanRepairs(spans, corebench.RepairBudget{
		SpeechSec:     res.TotalSpeechSec,
		Ratio:         0.10,
		CapSec:        300,
		CostPerSecUSD: repairCostPerSecUSD,
	}, repairValuePerUSD)

	res.CandidateSpans = len(plan.Attempt) + len(plan.SkippedBudget)
	res.AttemptSec = plan.PlannedSec
	res.BudgetSec = plan.BudgetSec
	res.ProjectedCostUSD = plan.PlannedCostUSD
	for _, s := range plan.Attempt {
		res.CandidateSec += s.DurationSec()
	}
	for _, s := range plan.SkippedBudget {
		res.CandidateSec += s.DurationSec()
		res.SkippedBudgetSec += s.DurationSec()
	}
	return res, nil
}

// repairWordsOf converts a meeting's hypothesis words into the repair-span signal:
// timing + confidence (when the engine emitted it). hasConf reports whether ANY word
// carried confidence — false means this run can't drive confidence-guided repair.
// Shared by the dry-run preview and the attempt+measure loop.
func repairWordsOf(h MeetingHyp) (words []corebench.RepairWord, hasConf bool) {
	words = make([]corebench.RepairWord, 0, len(h.Words))
	for _, w := range h.Words {
		rw := corebench.RepairWord{StartSec: w.Start, EndSec: w.End, ProductValue: 1}
		if w.Confidence != nil {
			rw.Confidence = *w.Confidence
			rw.HasConfidence = true
			hasConf = true
		}
		words = append(words, rw)
	}
	return words, hasConf
}

package bench

import (
	"fmt"
	"path/filepath"
	"sort"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// scale.go derives a measured ScaleModel from the runs already on disk and
// projects the cost curve toward the $0.01/audio-hour target — pure CPU, reading
// the ledger + each run's summary.json (no GPU). It answers the two questions the
// cost program keeps asking: what does $/audio-hour become at 10h of real
// meeting, and is $0.01 reachable by scaling audio at all or does it need a
// utilization/batching/work-reduction win (§1, §3.1).
//
// Apples-to-apples matters: fixed-vs-marginal differs by GPU and profile, so the
// fit is restricted to the production config (modal_cuda × batch_queue, the
// config AnchorCostPerProcessedAudioHourUSD is defined for). With ≥2 distinct
// scales it least-squares-fits both terms; with only one it falls back to the
// winner's measured GPU idle cost as the fixed-overhead proxy.

// ScaleProjection derives the model from stored runs and projects $/audio-hour at
// `scales` (default 1 / 10 / 100) toward `target` (default the §1 $0.01 stretch).
func (r *Runner) ScaleProjection(target float64, scales []float64) (corebench.ScaleProjection, error) {
	if target <= 0 {
		target = corebench.StretchTargetCostPerAudioHourUSD
	}
	entries, err := r.Ledger.ReadAll()
	if err != nil {
		return corebench.ScaleProjection{}, err
	}

	// One point per run (dedup ledger re-entries), production config only, real
	// cost + audio measured. Newest-wins on duplicate run IDs.
	seen := map[string]bool{}
	var points []corebench.ScalePoint
	var topRun string
	var topHours float64
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.HistoricalUnverified || e.RunID == "" || seen[e.RunID] {
			continue
		}
		if e.ExecutionProfile != "modal_cuda" || e.OperatingMode != "batch_queue" {
			continue
		}
		seen[e.RunID] = true
		sum, err := LoadModalSummary(filepath.Join(r.Store.RunDir(e.RunID), "summary.json"))
		if err != nil || sum.EstimatedCostUSD <= 0 || sum.ProcessedAudioHours <= 0 {
			continue
		}
		points = append(points, corebench.ScalePoint{AudioHours: sum.ProcessedAudioHours, CostUSD: sum.EstimatedCostUSD})
		if sum.ProcessedAudioHours > topHours {
			topHours, topRun = sum.ProcessedAudioHours, e.RunID
		}
	}
	if len(points) == 0 {
		return corebench.ScaleProjection{}, fmt.Errorf(
			"no verified modal_cuda/batch_queue runs with cost+audio on disk to fit a scale model")
	}

	// Always include the largest real run as an explicit scale point so the curve
	// is anchored to observed reality, not just 1/10/100.
	if topHours > 0 {
		scales = withScale(scales, topHours)
	}

	if model, ok := corebench.FitScaleModel(points); ok {
		proj := corebench.ProjectScale(model, target, scales)
		proj.Readiness = r.scaleReadiness(topRun, model)
		proj.Notes = append(proj.Notes, fitNote(len(points)))
		return proj, nil
	}

	// Single scale only: decompose the largest run via its measured GPU idle cost.
	sum, err := LoadModalSummary(filepath.Join(r.Store.RunDir(topRun), "summary.json"))
	if err != nil {
		return corebench.ScaleProjection{}, err
	}
	idle := 0.0
	if trace, err := r.Store.LoadTraceSummary(topRun); err == nil && trace.GPU != nil {
		idle = trace.GPU.IdleCostUSD
	}
	model, ok := corebench.ScaleModelFromIdle(sum.EstimatedCostUSD, idle, sum.ProcessedAudioHours)
	if !ok {
		return corebench.ScaleProjection{}, fmt.Errorf("could not decompose run %s for a scale model", topRun)
	}
	proj := corebench.ProjectScale(model, target, scales)
	proj.Readiness = r.scaleReadiness(topRun, model)
	proj.Notes = append(proj.Notes,
		"single-scale fit: fixed overhead estimated from the run's measured GPU idle cost (a conservative upper bound on the marginal floor) — add a run at a different scale to fit it directly")
	return proj, nil
}

// scaleReadiness evaluates the "may we scale to the full anchor?" gate from the
// largest subsample run: its GPU busy% and whether its quality is within the
// ceilings, read against the modeled 10h cost. Best-effort — returns nil when the
// run lacks the trace/metrics artifacts the gate needs (e.g. a summary-only run).
func (r *Runner) scaleReadiness(runID string, model corebench.ScaleModel) *corebench.ScaleReadiness {
	trace, err := r.Store.LoadTraceSummary(runID)
	if err != nil || trace.GPU == nil {
		return nil
	}
	var metrics corebench.MetricsFile
	if err := readJSON(filepath.Join(r.Store.RunDir(runID), "metrics.json"), &metrics); err != nil {
		return nil
	}
	qualityOK := corebench.QualityWithinCeilings(metrics.Aggregate)
	rd := corebench.EvaluateScaleReadiness(trace.GPU.BusyPct, qualityOK, model, corebench.DefaultScaleReadinessThresholds())
	return &rd
}

// withScale appends s to scales if not already present, keeping the list sorted.
func withScale(scales []float64, s float64) []float64 {
	if len(scales) == 0 {
		scales = []float64{1, 10, 100}
	}
	for _, v := range scales {
		if v == s {
			return scales
		}
	}
	scales = append(append([]float64(nil), scales...), s)
	sort.Float64s(scales)
	return scales
}

func fitNote(n int) string {
	if n == 2 {
		return "fitted from 2 runs across distinct scales"
	}
	return "least-squares fit across multiple runs"
}

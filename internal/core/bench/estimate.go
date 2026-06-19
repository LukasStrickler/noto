package bench

// Cost anchors used to project and judge a run before it executes.
//
// AnchorCostPerProcessedAudioHourUSD is the audited hosted-batch winner
// (§3.1: modal_cuda × batch_queue, L40S jobs=10). StretchTargetCostPerAudioHourUSD
// is the §1 aspirational floor after conservative work reduction (VAD). The gap
// between them is the addressable headroom an optimization is trying to close —
// the same idle waste GPUUtilization.IdleCostPerAudioHourUSD measures after a run.
const (
	AnchorCostPerProcessedAudioHourUSD = 0.0194
	StretchTargetCostPerAudioHourUSD   = 0.01
)

// SchemaEstimateV1 identifies bench_estimate.v1.
const SchemaEstimateV1 = "bench_estimate.v1"

// EstimateInput is the pure input for projecting a run's cost and KPI headroom
// before any GPU runs. AudioHours is the processed-audio-hour denominator the
// suite will feed the GPU; TierCapUSD is the hard ceiling the runner enforces.
type EstimateInput struct {
	SuiteID    string
	Tier       string
	AudioHours float64
	TierCapUSD float64
}

// EstimateResult is bench_estimate.v1 — a cost + KPI forecast (no GPU touched).
type EstimateResult struct {
	SchemaVersion string  `json:"schema_version"`
	SuiteID       string  `json:"suite_id"`
	Tier          string  `json:"tier"`
	AudioHours    float64 `json:"audio_hours,omitempty"`

	AnchorCostPerProcessedAudioHourUSD float64 `json:"anchor_cost_per_processed_audio_hour_usd"`
	ProjectedCostUSD                   float64 `json:"projected_cost_usd,omitempty"`
	TierBudgetCapUSD                   float64 `json:"tier_budget_cap_usd"`
	WithinBudget                       bool    `json:"within_budget"`

	TargetCostPerProcessedAudioHourUSD float64 `json:"target_cost_per_processed_audio_hour_usd"`
	IdleHeadroomPerAudioHourUSD        float64 `json:"idle_headroom_per_audio_hour_usd"`
	ProjectedTargetCostUSD             float64 `json:"projected_target_cost_usd,omitempty"`
	ProjectedHeadroomSavingsUSD        float64 `json:"projected_headroom_savings_usd,omitempty"`

	Notes []string `json:"notes,omitempty"`
}

// Estimate projects a run's cost and KPI headroom from the audited anchor rate.
// It is a planning aid (§12.6 noto bench estimate) and never launches the GPU:
// projected cost = anchor $/processed-audio-hour × audio hours, and the headroom
// is the spread between that anchor and the $0.01 stretch target.
func Estimate(in EstimateInput) EstimateResult {
	r := EstimateResult{
		SchemaVersion:                      SchemaEstimateV1,
		SuiteID:                            in.SuiteID,
		Tier:                               in.Tier,
		AudioHours:                         in.AudioHours,
		AnchorCostPerProcessedAudioHourUSD: AnchorCostPerProcessedAudioHourUSD,
		TierBudgetCapUSD:                   in.TierCapUSD,
		TargetCostPerProcessedAudioHourUSD: StretchTargetCostPerAudioHourUSD,
		IdleHeadroomPerAudioHourUSD:        roundUSD(AnchorCostPerProcessedAudioHourUSD - StretchTargetCostPerAudioHourUSD),
	}
	if in.AudioHours > 0 {
		r.ProjectedCostUSD = roundUSD(AnchorCostPerProcessedAudioHourUSD * in.AudioHours)
		r.ProjectedTargetCostUSD = roundUSD(StretchTargetCostPerAudioHourUSD * in.AudioHours)
		r.ProjectedHeadroomSavingsUSD = roundUSD(r.ProjectedCostUSD - r.ProjectedTargetCostUSD)
	} else {
		r.Notes = append(r.Notes, "audio_hours unknown for suite; cost projected against tier cap only")
	}
	r.WithinBudget = in.TierCapUSD <= 0 || r.ProjectedCostUSD <= in.TierCapUSD
	if !r.WithinBudget {
		r.Notes = append(r.Notes, "projected cost exceeds tier cap; runner will stop before overspend")
	}
	return r
}

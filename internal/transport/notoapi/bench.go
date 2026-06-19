package notoapi

import "context"

// BenchPreflightRequest is input for noto bench preflight.
type BenchPreflightRequest struct {
	SuiteID          string            `json:"suite_id"`
	Tier             string            `json:"tier"`
	OperatingMode    string            `json:"operating_mode"`
	ExecutionProfile string            `json:"execution_profile"`
	BudgetUSDCap     float64           `json:"budget_usd_cap"`
	Knobs            map[string]string `json:"knobs,omitempty"`
	IntegrationOnly  bool              `json:"integration_only,omitempty"`
	AgentID          string            `json:"agent_id,omitempty"`
}

// BenchPreflightResult is bench_preflight.v1.
type BenchPreflightResult struct {
	SchemaVersion      string   `json:"schema_version"`
	SuiteID            string   `json:"suite_id"`
	Tier               string   `json:"tier"`
	BudgetUSDCap       float64  `json:"budget_usd_cap"`
	ProjectedCostUSD   float64  `json:"projected_cost_usd,omitempty"`
	ComparableToWinner bool     `json:"comparable_to_winner"`
	Blockers           []string `json:"blockers"`
	Warnings           []string `json:"warnings,omitempty"`
}

// BenchEstimateRequest projects a run's cost + KPI headroom before it executes.
type BenchEstimateRequest struct {
	SuiteID          string `json:"suite_id"`
	Tier             string `json:"tier"`
	OperatingMode    string `json:"operating_mode,omitempty"`
	ExecutionProfile string `json:"execution_profile,omitempty"`
}

// BenchEstimateResult is bench_estimate.v1.
type BenchEstimateResult struct {
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

// BenchCompareRequest compares two benchmark runs.
type BenchCompareRequest struct {
	BaselineRunID  string `json:"baseline_run_id"`
	CandidateRunID string `json:"candidate_run_id"`
}

// BenchCompareResult is compare.v1 (JSON-serializable subset).
type BenchCompareResult struct {
	SchemaVersion       string             `json:"schema_version"`
	BaselineRunID       string             `json:"baseline_run_id"`
	CandidateRunID      string             `json:"candidate_run_id"`
	Comparable          bool               `json:"comparable"`
	IncomparableReason  string             `json:"incomparable_reason,omitempty"`
	Decision            string             `json:"decision"`
	Cost                BenchCompareCost   `json:"cost,omitempty"`
	Quality             map[string]any     `json:"quality,omitempty"`
	GuardrailsFailed    []string           `json:"guardrails_failed,omitempty"`
	GuardrailsSkipped   []string           `json:"guardrails_skipped,omitempty"`
	NextExperiment      string             `json:"next_experiment,omitempty"`
	AttributionInsights []string           `json:"attribution_insights,omitempty"`
	GPU                 *BenchCompareGPU   `json:"gpu,omitempty"`
	Scale               *BenchCompareScale `json:"scale,omitempty"`
}

// BenchCompareScale is the asymptotic cost-floor contrast: the per-audio-hour
// marginal rate each run converges to at scale. A lower candidate floor is the
// real mover toward $0.01 (scaling only amortizes fixed overhead).
type BenchCompareScale struct {
	BaselineFloorUSDPerAudioHour  float64 `json:"baseline_floor_usd_per_audio_hour"`
	CandidateFloorUSDPerAudioHour float64 `json:"candidate_floor_usd_per_audio_hour"`
	DeltaFloorUSDPerAudioHour     float64 `json:"delta_floor_usd_per_audio_hour"`
	TargetReachableByScale        bool    `json:"target_reachable_by_scale"`
}

// BenchCompareGPU surfaces the GPU idle-cost movement between two runs — the
// addressable waste recovered (delta < 0) or regressed (delta > 0).
type BenchCompareGPU struct {
	BaselineBusyPct                  float64 `json:"baseline_busy_pct,omitempty"`
	CandidateBusyPct                 float64 `json:"candidate_busy_pct,omitempty"`
	BaselineIdleCostPerAudioHourUSD  float64 `json:"baseline_idle_cost_per_audio_hour_usd,omitempty"`
	CandidateIdleCostPerAudioHourUSD float64 `json:"candidate_idle_cost_per_audio_hour_usd,omitempty"`
	DeltaIdleCostPerAudioHourUSD     float64 `json:"delta_idle_cost_per_audio_hour_usd,omitempty"`
}

type BenchCompareCost struct {
	BaselineCostPerProcessedAudioHourUSD  float64            `json:"baseline_cost_per_processed_audio_hour_usd"`
	CandidateCostPerProcessedAudioHourUSD float64            `json:"candidate_cost_per_processed_audio_hour_usd"`
	DeltaPct                              float64            `json:"delta_pct"`
	MeetsMDE                              bool               `json:"meets_mde"`
	WaterfallDeltasUSD                    map[string]float64 `json:"waterfall_deltas_usd,omitempty"`
}

// BenchRunRequest starts a benchmark run.
type BenchRunRequest struct {
	SuiteID          string            `json:"suite_id"`
	Tier             string            `json:"tier"`
	OperatingMode    string            `json:"operating_mode"`
	ExecutionProfile string            `json:"execution_profile"`
	BudgetUSDCap     float64           `json:"budget_usd_cap,omitempty"`
	Knobs            map[string]string `json:"knobs,omitempty"`
	HypothesisID     string            `json:"hypothesis_id,omitempty"`
	ProgramStep      string            `json:"program_step,omitempty"`
	IntegrationOnly  bool              `json:"integration_only,omitempty"`
	AgentID          string            `json:"agent_id,omitempty"`
	GPU              string            `json:"gpu,omitempty"`
}

// BenchRunResult is returned after a completed bench run.
type BenchRunResult struct {
	SchemaVersion                string  `json:"schema_version"`
	RunID                        string  `json:"run_id"`
	ArtifactDir                  string  `json:"artifact_dir"`
	EstimatedCostUSD             float64 `json:"estimated_cost_usd"`
	CostPerProcessedAudioHourUSD float64 `json:"cost_per_processed_audio_hour_usd"`
	UnattributedPct              float64 `json:"unattributed_pct"`
	TraceValid                   bool    `json:"trace_valid"`
	GPUBusyPct                   float64 `json:"gpu_busy_pct,omitempty"`
	GPUIdleCostPerAudioHourUSD   float64 `json:"gpu_idle_cost_per_audio_hour_usd,omitempty"`
}

// BenchLedgerAppendRequest appends a ledger decision.
type BenchLedgerAppendRequest struct {
	RunID                string `json:"run_id"`
	BaselineRunID        string `json:"baseline_run_id,omitempty"`
	Decision             string `json:"decision"`
	HypothesisID         string `json:"hypothesis_id,omitempty"`
	ProgramStep          string `json:"program_step,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	HistoricalUnverified bool   `json:"historical_unverified,omitempty"`
}

// BenchLedgerAppendResult confirms ledger append.
type BenchLedgerAppendResult struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	Decision      string `json:"decision"`
}

// BenchDatasetListResult lists registered suites.
type BenchDatasetListResult struct {
	Suites []BenchDatasetSuite `json:"suites"`
}

type BenchDatasetSuite struct {
	ID          string  `json:"id"`
	ModalSuite  string  `json:"modal_suite"`
	Quick       bool    `json:"quick"`
	Hours       float64 `json:"hours,omitempty"`
	Description string  `json:"description,omitempty"`
}

// BenchLedgerWinnersOpts filters ledger winners.
type BenchLedgerWinnersOpts struct {
	Profile string `json:"profile,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// BenchLedgerWinner is a ledger winner row.
type BenchLedgerWinner struct {
	RunID                        string  `json:"run_id"`
	ExecutionProfile             string  `json:"execution_profile"`
	OperatingMode                string  `json:"operating_mode"`
	CostPerProcessedAudioHourUSD float64 `json:"cost_per_processed_audio_hour_usd"`
}

// BenchLedgerWinnersResult lists current winners.
type BenchLedgerWinnersResult struct {
	Winners []BenchLedgerWinner `json:"winners"`
}

// BenchAuditResult is the compact compute attribution view for one run.
type BenchAuditResult struct {
	SchemaVersion   string            `json:"schema_version"`
	RunID           string            `json:"run_id"`
	ArtifactDir     string            `json:"artifact_dir"`
	TotalUSD        float64           `json:"total_usd"`
	ComputeUSD      float64           `json:"compute_usd"`
	UnattributedPct float64           `json:"unattributed_pct"`
	TopStages       []BenchAuditStage `json:"top_stages"`
	GPU             *BenchAuditGPU    `json:"gpu,omitempty"`
}

type BenchAuditStage struct {
	Stage  string  `json:"stage"`
	System string  `json:"system,omitempty"`
	USD    float64 `json:"usd"`
}

// BenchAuditGPU is the GPU-efficiency view: how saturated the card stayed and
// the idle dollars still on the table toward the $/audio-hour target.
type BenchAuditGPU struct {
	MeanUtilizationPct      float64 `json:"mean_utilization_pct,omitempty"`
	PeakUtilizationPct      float64 `json:"peak_utilization_pct,omitempty"`
	BusyPct                 float64 `json:"busy_pct,omitempty"`
	PeakVRAMMB              float64 `json:"peak_vram_mb,omitempty"`
	IdleCostUSD             float64 `json:"idle_cost_usd,omitempty"`
	IdleCostPerAudioHourUSD float64 `json:"idle_cost_per_audio_hour_usd,omitempty"`
}

// BenchScaleRequest projects the cost curve toward a target $/audio-hour from the
// runs already on disk. TargetCostPerAudioHourUSD<=0 defaults to the $0.01 stretch;
// AudioHours selects the scale points to report (empty → 1/10/100 + the largest run).
type BenchScaleRequest struct {
	TargetCostPerAudioHourUSD float64   `json:"target_cost_per_audio_hour_usd,omitempty"`
	AudioHours                []float64 `json:"audio_hours,omitempty"`
}

// BenchScalePoint is the modeled cost at one scale.
type BenchScalePoint struct {
	AudioHours          float64 `json:"audio_hours"`
	CostUSD             float64 `json:"cost_usd"`
	CostPerAudioHourUSD float64 `json:"cost_per_audio_hour_usd"`
}

// BenchScaleResult is scale_projection.v1: how $/audio-hour falls toward the
// marginal floor as run scale grows, and whether the target is reachable by scale
// (vs needing a utilization/batching/work-reduction win).
type BenchScaleResult struct {
	SchemaVersion           string               `json:"schema_version"`
	FixedUSD                float64              `json:"fixed_usd"`
	MarginalUSDPerAudioHour float64              `json:"marginal_usd_per_audio_hour"`
	FloorUSDPerAudioHour    float64              `json:"floor_usd_per_audio_hour"`
	TargetUSDPerAudioHour   float64              `json:"target_usd_per_audio_hour"`
	TargetReachableByScale  bool                 `json:"target_reachable_by_scale"`
	HoursToReachTarget      float64              `json:"hours_to_reach_target,omitempty"`
	Points                  []BenchScalePoint    `json:"points"`
	Readiness               *BenchScaleReadiness `json:"readiness,omitempty"`
	Notes                   []string             `json:"notes,omitempty"`
}

// BenchScaleReadiness is the "may we scale to the full anchor?" gate: all of
// subsample quality, GPU utilization, and the modeled 10h cost must pass.
type BenchScaleReadiness struct {
	Ready                    bool     `json:"ready"`
	BusyPct                  float64  `json:"busy_pct"`
	MinBusyPct               float64  `json:"min_busy_pct"`
	QualityWithinGuardrails  bool     `json:"quality_within_guardrails"`
	CostPerAudioHourAt10hUSD float64  `json:"cost_per_audio_hour_at_10h_usd"`
	Reasons                  []string `json:"reasons,omitempty"`
}

// BenchKPIWeights are the weights behind the composite score (cost-led by default).
type BenchKPIWeights struct {
	Cost        float64 `json:"cost"`
	Quality     float64 `json:"quality"`
	Utilization float64 `json:"utilization"`
}

// BenchInsightsResult is one run's full development-facing snapshot — everything
// `noto bench insights` shows in a single command: the weighted KPI score and its
// transparent component breakdown, the cost-vs-anchor/target position, quality vs
// guardrails, GPU busy% + idle waste (money not well spent), the top cost stages,
// and the asymptotic cost floor. Assembled from the run's metrics + audit + scale.
type BenchInsightsResult struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	FromWinner    bool   `json:"from_winner"`

	Score           float64            `json:"score"` // 0..100
	GuardrailsPass  bool               `json:"guardrails_pass"`
	ScoreComponents map[string]float64 `json:"score_components"` // cost/quality/utilization 0..1
	Weights         BenchKPIWeights    `json:"weights"`
	Notes           []string           `json:"notes,omitempty"`

	CostPerProcessedAudioHourUSD float64 `json:"cost_per_processed_audio_hour_usd"`
	AnchorCostUSD                float64 `json:"anchor_cost_usd"`
	TargetCostUSD                float64 `json:"target_cost_usd"`

	WER   float64 `json:"wer,omitempty"`
	DER   float64 `json:"der,omitempty"`
	CpWER float64 `json:"cpwer,omitempty"`

	GPUBusyPct                 float64 `json:"gpu_busy_pct,omitempty"`
	GPUMeanUtilizationPct      float64 `json:"gpu_mean_utilization_pct,omitempty"`
	GPUPeakUtilizationPct      float64 `json:"gpu_peak_utilization_pct,omitempty"`
	GPUIdleCostPerAudioHourUSD float64 `json:"gpu_idle_cost_per_audio_hour_usd,omitempty"`

	// Tracking / perf-accuracy: how much of the bill we can actually explain.
	// UnattributedPct is the share of cost the trace could NOT attribute to a
	// stage; the §4.1 guardrail auto-rejects a run above 2% — a low number means
	// we genuinely understand where the money went.
	UnattributedPct            float64 `json:"unattributed_pct"`
	AttributionWithinGuardrail bool    `json:"attribution_within_guardrail"`

	TopStages []BenchAuditStage `json:"top_stages,omitempty"`

	MarginalFloorUSDPerAudioHour float64 `json:"marginal_floor_usd_per_audio_hour,omitempty"`
	TargetReachableByScale       bool    `json:"target_reachable_by_scale,omitempty"`
}

// BenchRepairResult is a run's B7 dry-run repair PREVIEW (§10.4): the candidate
// repair spans derived from stored word confidences, planned under budget — what a
// second pass WOULD attempt and what it would cost, with no GPU and no transcript
// writes. HasConfidence is false when the run carries no word confidence (re-run
// with `--knob confidence=1`).
type BenchRepairResult struct {
	SchemaVersion       string  `json:"schema_version"`
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

// BenchCalibrationResult is a run's B6 confidence-calibration report (§10.3): how
// well the model's word confidence predicts its own errors. SignalAdmissible is the
// gate — confidence may drive repair spend only when it beats the do-nothing
// baseline (bottom-decile capture > random) without a high-confidence error spike.
// HasConfidence is false when the run carried no word confidence to score.
type BenchCalibrationResult struct {
	SchemaVersion         string   `json:"schema_version"`
	RunID                 string   `json:"run_id"`
	HasConfidence         bool     `json:"has_confidence"`
	Words                 int      `json:"words"`
	Errors                int      `json:"errors"`
	ECE                   float64  `json:"ece"`
	Brier                 float64  `json:"brier,omitempty"`
	BottomDecileCapture   float64  `json:"bottom_decile_capture"`
	RiskCoverageAUC       float64  `json:"risk_coverage_auc,omitempty"`
	HighConfErrorRate     float64  `json:"high_conf_error_rate"`
	CaptureLiftOverRandom float64  `json:"capture_lift_over_random"`
	SignalAdmissible      bool     `json:"signal_admissible"`
	Reasons               []string `json:"reasons,omitempty"`

	// Repair ceiling: benchmark headroom from oracle-repairing the lowest-
	// confidence decile (the MAX error-rate drop repair could buy).
	RepairFraction        float64 `json:"repair_fraction"`
	CurrentErrorRate      float64 `json:"current_error_rate"`
	CeilingErrorRate      float64 `json:"ceiling_error_rate"`
	RepairFixableErrors   int     `json:"repair_fixable_errors"`
}

// BenchOverlapResult is a run's targeted-overlap-repair analysis (GPU-free): how
// much HARD overlap (>=2 distinct speakers) the references contain, how much
// diarization error LIVES in those overlap regions (AddressableFraction — the
// headroom a separation pass could recover), and what repairing ONLY those seconds
// costs versus separating everywhere. The gate for spending GPU on an overlap
// separation pass: build it only when the addressable error is meaningful AND the
// targeted cost is small. noto captures two channels (your mic + all remote
// participants on system audio), so you-over-room overlap is already free; this
// measures the residual remote-vs-remote overlap that needs real separation.
type BenchOverlapResult struct {
	SchemaVersion           string  `json:"schema_version"`
	RunID                   string  `json:"run_id"`
	Meetings                int     `json:"meetings"`
	Scored                  int     `json:"scored"`
	HasDiarization          bool    `json:"has_diarization"`
	OverlapFraction         float64 `json:"overlap_fraction"`
	TotalOverlapSec         float64 `json:"total_overlap_sec"`
	TotalSpeechSec          float64 `json:"total_speech_sec"`
	PeakSpeakers            int     `json:"peak_speakers"`
	AddressableFraction     float64 `json:"addressable_fraction"`
	TotalDERErrorSec        float64 `json:"total_der_error_sec"`
	OverlapDERErrorSec      float64 `json:"overlap_der_error_sec"`
	BaseCostPerAudioHourUSD float64 `json:"base_cost_per_audio_hour_usd"`
	SepCostFactor           float64 `json:"sep_cost_factor"`
	TargetedExtraUSD        float64 `json:"targeted_extra_usd"`
	TargetedExtraPct        float64 `json:"targeted_extra_pct"`
	BlanketExtraUSD         float64 `json:"blanket_extra_usd"`
	BlanketExtraPct         float64 `json:"blanket_extra_pct"`
	SavingsFactor           float64 `json:"savings_factor"`
}

// BenchRepairAttemptResult is the B7 attempt+measure report (§10.4): the REAL
// accuracy-vs-cost answer for transcription repair, measured on the benchmark
// reference rather than on confidence. RunID is the baseline; AltRunID is the
// second run whose different decode supplies the re-decoded words. Negative deltas
// are improvements. GatePass is whether the dry-run beat do-nothing efficiently
// enough (accepted-per-dollar, negative-rate) to justify a production repair (B8) —
// it is never auto-applied here, this writes no transcript.
type BenchRepairAttemptResult struct {
	SchemaVersion     string   `json:"schema_version"`
	RunID             string   `json:"run_id"`
	AltRunID          string   `json:"alt_run_id"`
	Method            string   `json:"method"`
	MeetingsAttempted int      `json:"meetings_attempted"`
	SpansAttempted    int      `json:"spans_attempted"`
	SpansDiffered     int      `json:"spans_differed"`
	AcceptedRepairs   int      `json:"accepted_repairs"`
	NegativeRepairs   int      `json:"negative_repairs"`
	CostUSD           float64  `json:"cost_usd"`
	AcceptedPerUSD    float64  `json:"accepted_per_usd"`
	NegativeRate      float64  `json:"negative_rate"`
	NetWERDelta       float64  `json:"net_wer_delta"`
	NetCpWERDelta     float64  `json:"net_cpwer_delta"`
	CeilingWERDelta   float64  `json:"ceiling_wer_delta"`
	CeilingAccepted   int      `json:"ceiling_accepted"`
	AcceptedSec       float64  `json:"accepted_sec"`
	GatePass          bool     `json:"gate_pass"`
	GateReasons       []string `json:"gate_reasons,omitempty"`
}

// BenchClient methods for measurement spine.
type BenchClient interface {
	// BenchInsights returns one run's full weighted-KPI snapshot (winner when runID empty).
	BenchInsights(ctx context.Context, runID string) (BenchInsightsResult, error)
	// BenchRepair returns a run's B7 dry-run repair preview (candidates + cost).
	BenchRepair(ctx context.Context, runID string) (BenchRepairResult, error)
	// BenchCalibration scores a run's word-confidence calibration (B6).
	BenchCalibration(ctx context.Context, runID string) (BenchCalibrationResult, error)
	// BenchOverlap analyzes a run's hard-overlap cost + addressable diarization error.
	BenchOverlap(ctx context.Context, runID string) (BenchOverlapResult, error)
	// BenchRepairAttempt re-decodes a run's low-confidence spans from a second run and
	// measures the benchmark accuracy delta + B7 gate (the real with/without-repair KPI).
	BenchRepairAttempt(ctx context.Context, runID, altRunID string) (BenchRepairAttemptResult, error)
	BenchEstimate(ctx context.Context, req BenchEstimateRequest) (BenchEstimateResult, error)
	BenchPreflight(ctx context.Context, req BenchPreflightRequest) (BenchPreflightResult, error)
	BenchRun(ctx context.Context, req BenchRunRequest) (BenchRunResult, error)
	BenchCompare(ctx context.Context, req BenchCompareRequest) (BenchCompareResult, error)
	BenchLedgerWinners(ctx context.Context, opts BenchLedgerWinnersOpts) (BenchLedgerWinnersResult, error)
	BenchLedgerAppend(ctx context.Context, req BenchLedgerAppendRequest) (BenchLedgerAppendResult, error)
	BenchDatasetList(ctx context.Context) (BenchDatasetListResult, error)
	BenchAudit(ctx context.Context, runID string) (BenchAuditResult, error)
	BenchRetrace(ctx context.Context, runID string) (BenchAuditResult, error)
	BenchScale(ctx context.Context, req BenchScaleRequest) (BenchScaleResult, error)
}

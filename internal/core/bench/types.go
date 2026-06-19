package bench

// Decision is the adoption outcome for a benchmark compare.
type Decision string

const (
	DecisionAdopt        Decision = "adopt"
	DecisionReject       Decision = "reject"
	DecisionRetry        Decision = "retry"
	DecisionNeedsConfirm Decision = "needs_confirm"
)

// RunManifest is bench_run_manifest.v1 (subset used for comparability).
type RunManifest struct {
	SchemaVersion    string            `json:"schema_version"`
	RunID            string            `json:"run_id"`
	ExecutionProfile string            `json:"execution_profile"`
	OperatingMode    string            `json:"operating_mode"`
	SuiteID          string            `json:"suite_id"`
	SplitID          string            `json:"split_id,omitempty"`
	HypothesisID     string            `json:"hypothesis_id,omitempty"`
	ProgramStep      string            `json:"program_step,omitempty"`
	Tier             string            `json:"tier"`
	IntegrationOnly  bool              `json:"integration_only"`
	AgentID          string            `json:"agent_id,omitempty"`
	BudgetUSDCap     float64           `json:"budget_usd_cap"`
	Knobs            map[string]string `json:"knobs"`
	BenchStack       string            `json:"bench_stack,omitempty"`
	GitCommit        string            `json:"git_commit,omitempty"`
	Comparability    ManifestCompat    `json:"comparability"`
}

type ManifestCompat struct {
	TargetConcurrency int    `json:"target_concurrency"`
	CacheState        string `json:"cache_state"`
	TraceMode         string `json:"trace_mode"`
}

// TraceSummary is trace_summary.v1 (fields needed for compare + audit).
type TraceSummary struct {
	SchemaVersion      string            `json:"schema_version"`
	RunID              string            `json:"run_id"`
	TraceMode          string            `json:"trace_mode,omitempty"`
	TraceOverheadPct   float64           `json:"trace_overhead_pct,omitempty"`
	DroppedTraceEvents int               `json:"dropped_trace_events,omitempty"`
	CostWaterfall      CostWaterfall     `json:"cost_waterfall"`
	ComputeByStage     []StageCost       `json:"compute_by_stage"`
	ComputeBySystem    []SystemCost      `json:"compute_by_system,omitempty"`
	PerMeeting         []PerMeetingTrace `json:"per_meeting,omitempty"`
	GPU                *GPUUtilization   `json:"gpu,omitempty"`
}

// GPUUtilization summarizes how well a run kept the GPU saturated.
//
// Utilization itself is diagnostic only for adoption (§4.2 — a high-util run
// that costs more or regresses quality is still rejected). What makes it a
// decision-grade KPI is IdleCostUSD: the dollars billed while the card was
// *not* busy. That idle is pure waste and the single biggest addressable gap
// between the current ~$0.0194/audio-hr anchor and the ~$0.01 stretch target,
// so IdleCostPerAudioHourUSD reads as the headroom still on the table.
type GPUUtilization struct {
	MeanUtilizationPct float64 `json:"mean_utilization_pct,omitempty"`
	PeakUtilizationPct float64 `json:"peak_utilization_pct,omitempty"`
	BusyPct            float64 `json:"busy_pct,omitempty"`
	PeakVRAMMB         float64 `json:"peak_vram_mb,omitempty"`
	HourlyUSD          float64 `json:"hourly_usd,omitempty"`
	// GPUCostUSD is the GPU-only portion of the bill (hourly × wall hours).
	GPUCostUSD float64 `json:"gpu_cost_usd,omitempty"`
	// IdleCostUSD is GPUCostUSD billed while the GPU was below the busy
	// threshold — the addressable waste.
	IdleCostUSD float64 `json:"idle_cost_usd,omitempty"`
	// IdleCostPerAudioHourUSD expresses that waste in the headline denominator.
	IdleCostPerAudioHourUSD float64 `json:"idle_cost_per_audio_hour_usd,omitempty"`
}

type CostWaterfall struct {
	TotalUSD         float64 `json:"total_usd"`
	ColdStartUSD     float64 `json:"cold_start_usd,omitempty"`
	ModelLoadUSD     float64 `json:"model_load_usd,omitempty"`
	WarmIdleUSD      float64 `json:"warm_idle_usd,omitempty"`
	AudioStageUSD    float64 `json:"audio_stage_usd,omitempty"`
	ComputeUSD       float64 `json:"compute_usd"`
	RetryUSD         float64 `json:"retry_usd,omitempty"`
	CrashBilledUSD   float64 `json:"crash_billed_usd,omitempty"`
	CancelBilledUSD  float64 `json:"cancel_billed_usd,omitempty"`
	TraceOverheadUSD float64 `json:"trace_overhead_usd,omitempty"`
	UnattributedUSD  float64 `json:"unattributed_usd,omitempty"`
	UnattributedPct  float64 `json:"unattributed_pct"`
}

type StageCost struct {
	Stage        string  `json:"stage"`
	System       string  `json:"system,omitempty"`
	WallMS       float64 `json:"wall_ms,omitempty"`
	USD          float64 `json:"usd"`
	PctOfCompute float64 `json:"pct_of_compute,omitempty"`
}

type SystemCost struct {
	System     string  `json:"system"`
	USD        float64 `json:"usd"`
	PctOfTotal float64 `json:"pct_of_total,omitempty"`
}

type PerMeetingTrace struct {
	MeetingID        string             `json:"meeting_id"`
	AudioSec         float64            `json:"audio_sec,omitempty"`
	SpeechSec        float64            `json:"speech_sec,omitempty"`
	WallMS           float64            `json:"wall_ms,omitempty"`
	USD              float64            `json:"usd,omitempty"`
	OverlapMS        float64            `json:"overlap_ms,omitempty"`
	AllocationMethod string             `json:"allocation_method,omitempty"`
	USDByStage       map[string]float64 `json:"usd_by_stage,omitempty"`
	Stages           map[string]float64 `json:"stages,omitempty"`
}

// MetricsFile is metrics.v1 aggregate quality record.
type MetricsFile struct {
	SchemaVersion string             `json:"schema_version"`
	RunID         string             `json:"run_id"`
	Aggregate     map[string]float64 `json:"aggregate"`
	Slices        map[string]any     `json:"slices,omitempty"`
	Denominators  map[string]any     `json:"denominators,omitempty"`
}

// CompareResult is compare.v1 output.
type CompareResult struct {
	SchemaVersion       string          `json:"schema_version"`
	BaselineRunID       string          `json:"baseline_run_id"`
	CandidateRunID      string          `json:"candidate_run_id"`
	Comparable          bool            `json:"comparable"`
	IncomparableReason  string          `json:"incomparable_reason,omitempty"`
	HypothesisID        string          `json:"hypothesis_id,omitempty"`
	ProgramStep         string          `json:"program_step,omitempty"`
	Decision            Decision        `json:"decision"`
	Cost                CompareCost     `json:"cost,omitempty"`
	Quality             map[string]any  `json:"quality,omitempty"`
	Slices              map[string]any  `json:"slices,omitempty"`
	GuardrailsFailed    []string        `json:"guardrails_failed,omitempty"`
	GuardrailsSkipped   []string        `json:"guardrails_skipped,omitempty"`
	ComplexityDelta     int             `json:"complexity_delta,omitempty"`
	NextExperiment      string          `json:"next_experiment,omitempty"`
	ManifestDiff        *ManifestDiff   `json:"manifest_diff,omitempty"`
	Intermediates       map[string]any  `json:"intermediates,omitempty"`
	SignatureCheck      *SignatureCheck `json:"signature_check,omitempty"`
	AttributionInsights []string        `json:"attribution_insights,omitempty"`
	GPU                 *CompareGPU     `json:"gpu,omitempty"`
	Scale               *CompareScale   `json:"scale,omitempty"`
}

// CompareScale contrasts the asymptotic cost FLOOR — the per-audio-hour marginal
// rate each run converges to at large scale, from its measured GPU idle
// decomposition (scale.go). It is the decision-relevant cost number for the $0.01
// target: scaling audio only amortizes per-run fixed overhead, so a lever that
// lowers the marginal floor is what actually moves the achievable $/audio-hr,
// while one that only trims idle lowers fixed overhead without moving the floor.
type CompareScale struct {
	BaselineFloorUSDPerAudioHour  float64 `json:"baseline_floor_usd_per_audio_hour"`
	CandidateFloorUSDPerAudioHour float64 `json:"candidate_floor_usd_per_audio_hour"`
	DeltaFloorUSDPerAudioHour     float64 `json:"delta_floor_usd_per_audio_hour"`
	BaselineFixedUSD              float64 `json:"baseline_fixed_usd,omitempty"`
	CandidateFixedUSD             float64 `json:"candidate_fixed_usd,omitempty"`
	TargetReachableByScale        bool    `json:"target_reachable_by_scale"`
}

// CompareGPU contrasts GPU efficiency between baseline and candidate. BusyPct is
// diagnostic; the decision-relevant number is idle cost per audio-hour — the
// addressable waste — and whether the candidate moved it down (toward the $0.01
// stretch target) or up.
type CompareGPU struct {
	BaselineBusyPct                  float64 `json:"baseline_busy_pct,omitempty"`
	CandidateBusyPct                 float64 `json:"candidate_busy_pct,omitempty"`
	BaselineIdleCostPerAudioHourUSD  float64 `json:"baseline_idle_cost_per_audio_hour_usd,omitempty"`
	CandidateIdleCostPerAudioHourUSD float64 `json:"candidate_idle_cost_per_audio_hour_usd,omitempty"`
	DeltaIdleCostPerAudioHourUSD     float64 `json:"delta_idle_cost_per_audio_hour_usd,omitempty"`
}

type CompareCost struct {
	BaselineCostPerProcessedAudioHourUSD  float64            `json:"baseline_cost_per_processed_audio_hour_usd"`
	CandidateCostPerProcessedAudioHourUSD float64            `json:"candidate_cost_per_processed_audio_hour_usd"`
	DeltaPct                              float64            `json:"delta_pct"`
	MeetsMDE                              bool               `json:"meets_mde"`
	WaterfallDeltasUSD                    map[string]float64 `json:"waterfall_deltas_usd,omitempty"`
}

type ManifestDiff struct {
	ChangedKnobs   []string          `json:"changed_knobs"`
	BaselineKnobs  map[string]string `json:"baseline_knobs"`
	CandidateKnobs map[string]string `json:"candidate_knobs"`
}

type SignatureCheck struct {
	HypothesisID   string           `json:"hypothesis_id"`
	Pass           bool             `json:"pass"`
	RequiredMovers []SignatureMover `json:"required_movers,omitempty"`
	ForbiddenHits  []string         `json:"forbidden_hits,omitempty"`
}

type SignatureMover struct {
	Field     string  `json:"field"`
	Direction string  `json:"direction"`
	MinPct    float64 `json:"min_pct,omitempty"`
	MinUSD    float64 `json:"min_usd,omitempty"`
	ActualPct float64 `json:"actual_pct,omitempty"`
	Actual    float64 `json:"actual,omitempty"`
	Pass      bool    `json:"pass"`
}

// RunBundle is everything needed for compare from disk.
type RunBundle struct {
	Manifest             RunManifest
	CostPerProcessedHour float64
	TraceSummary         TraceSummary
	Metrics              MetricsFile
}

const (
	SchemaTraceSummaryV1 = "trace_summary.v1"
	SchemaCompareV1      = "compare.v1"
	SchemaMetricsV1      = "metrics.v1"
	SchemaManifestV1     = "bench_run_manifest.v1"
)

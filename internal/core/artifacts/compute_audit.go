package artifacts

import (
	"strings"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

const SchemaComputeAuditV1 = "compute_audit.v1"

// ComputeAudit is the top-level run record for benchmark and (later) production jobs.
// Stage detail lives in trace_summary.json; this carries headline scalars + pointer.
type ComputeAudit struct {
	SchemaVersion    string        `json:"schema_version"`
	RunID            string        `json:"run_id"`
	ExecutionProfile string        `json:"execution_profile"`
	OperatingMode    string        `json:"operating_mode"`
	Dataset          AuditDataset  `json:"dataset"`
	Git              AuditGit      `json:"git"`
	Hardware         AuditHardware `json:"hardware"`
	Cost             AuditCost     `json:"cost"`
	ColdStartMS      float64       `json:"cold_start_ms,omitempty"`
	ModelLoadMS      float64       `json:"model_load_ms,omitempty"`
	WarmIdleMS       float64       `json:"warm_idle_ms,omitempty"`
	TraceSummaryRef  string        `json:"trace_summary_ref,omitempty"`
	TraceOverheadPct float64       `json:"trace_overhead_pct,omitempty"`
}

type AuditDataset struct {
	SuiteID    string  `json:"suite_id,omitempty"`
	Version    string  `json:"version,omitempty"`
	AudioHours float64 `json:"audio_hours,omitempty"`
}

type AuditGit struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

type AuditHardware struct {
	GPU       string  `json:"gpu"`
	CPUCores  float64 `json:"cpu_cores"`
	MemoryGiB float64 `json:"memory_gib"`
}

type AuditCost struct {
	EstimatedCostUSD             float64 `json:"estimated_cost_usd"`
	CostPerProcessedAudioHourUSD float64 `json:"cost_per_processed_audio_hour_usd"`
	CostPerSpeechHourUSD         float64 `json:"cost_per_speech_hour_usd,omitempty"`
	UnattributedPct              float64 `json:"unattributed_pct,omitempty"`
	// Legacy alias from modal_benchmark.py summary.json
	CostPerAudioHourUSD float64 `json:"cost_per_audio_hour_usd,omitempty"`
}

func (c *ComputeAudit) Kind() ArtifactKind {
	return ArtifactKind("compute_audit")
}

func (c *ComputeAudit) Version() string {
	return c.SchemaVersion
}

// NormalizeAliases maps legacy Python field names to canonical KPI fields.
func (c *ComputeAudit) NormalizeAliases() {
	if c.Cost.CostPerProcessedAudioHourUSD == 0 && c.Cost.CostPerAudioHourUSD > 0 {
		c.Cost.CostPerProcessedAudioHourUSD = c.Cost.CostPerAudioHourUSD
	}
}

func (c *ComputeAudit) Validate() *notoerr.Error {
	if c == nil {
		return notoerr.New("invalid_artifact", "compute audit is nil", nil)
	}
	if c.SchemaVersion != SchemaComputeAuditV1 {
		return notoerr.New("invalid_artifact", "unsupported compute audit schema", map[string]any{
			"schema_version": c.SchemaVersion,
			"expected":       SchemaComputeAuditV1,
		})
	}
	if strings.TrimSpace(c.RunID) == "" {
		return notoerr.New("invalid_artifact", "run_id is required", nil)
	}
	if strings.TrimSpace(c.ExecutionProfile) == "" {
		return notoerr.New("invalid_artifact", "execution_profile is required", nil)
	}
	if strings.TrimSpace(c.OperatingMode) == "" {
		return notoerr.New("invalid_artifact", "operating_mode is required", nil)
	}
	c.NormalizeAliases()
	if c.Cost.EstimatedCostUSD <= 0 {
		return notoerr.New("invalid_artifact", "cost.estimated_cost_usd is required", nil)
	}
	if c.Cost.CostPerProcessedAudioHourUSD <= 0 {
		return notoerr.New("invalid_artifact", "cost_per_processed_audio_hour_usd is required", nil)
	}
	if c.Cost.UnattributedPct > 2 {
		return notoerr.New("invalid_artifact", "unattributed_pct exceeds 2% gate", map[string]any{
			"unattributed_pct": c.Cost.UnattributedPct,
		})
	}
	return nil
}

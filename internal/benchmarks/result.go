package benchmarks

import "github.com/lukasstrickler/noto/internal/notoerr"

type Result struct {
	SchemaVersion string          `json:"schema_version"`
	RunID         string          `json:"run_id"`
	Dataset       string          `json:"dataset"`
	SampleID      string          `json:"sample_id"`
	ProviderID    string          `json:"provider_id"`
	ModelID       string          `json:"model_id"`
	Metrics       Metrics         `json:"metrics"`
	Validation    ValidationState `json:"validation"`
}

type Metrics struct {
	WER                  *float64 `json:"wer"`
	DER                  *float64 `json:"der"`
	JER                  *float64 `json:"jer"`
	LatencySeconds       float64  `json:"latency_seconds"`
	CostUSD              float64  `json:"cost_usd"`
	AudioSeconds         float64  `json:"audio_seconds"`
	SourceRolesPreserved bool     `json:"source_roles_preserved"`
}

type ValidationState struct {
	SchemaValid bool     `json:"schema_valid"`
	Errors      []string `json:"errors"`
}

func ValidateResult(result Result) error {
	if result.SchemaVersion != "benchmark-result.v1" {
		return notoerr.New("schema_validation_failed", "Benchmark result schema_version must be benchmark-result.v1.", map[string]any{"schema_version": result.SchemaVersion})
	}
	if result.RunID == "" || result.Dataset == "" || result.ProviderID == "" || result.ModelID == "" {
		return notoerr.New("schema_validation_failed", "Benchmark result requires run_id, dataset, provider_id, and model_id.", nil)
	}
	if result.Metrics.AudioSeconds < 0 || result.Metrics.LatencySeconds < 0 || result.Metrics.CostUSD < 0 {
		return notoerr.New("schema_validation_failed", "Benchmark metrics cannot be negative.", nil)
	}
	return nil
}

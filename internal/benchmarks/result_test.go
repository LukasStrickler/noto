package benchmarks

import "testing"

func TestValidateResultAcceptsProviderBenchmarkShape(t *testing.T) {
	wer := 0.032
	der := 0.12
	result := Result{
		SchemaVersion: "benchmark-result.v1",
		RunID:         "run_test",
		Dataset:       "ami",
		SampleID:      "ES2004a",
		ProviderID:    "assemblyai",
		ModelID:       "universal-3-pro",
		Metrics: Metrics{
			WER:                  &wer,
			DER:                  &der,
			LatencySeconds:       42,
			CostUSD:              0.08,
			AudioSeconds:         120,
			SourceRolesPreserved: true,
		},
		Validation: ValidationState{SchemaValid: true},
	}
	if err := ValidateResult(result); err != nil {
		t.Fatalf("ValidateResult returned error: %v", err)
	}
}

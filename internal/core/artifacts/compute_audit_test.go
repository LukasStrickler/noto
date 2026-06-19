package artifacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func computeAuditFixture(t *testing.T) []byte {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "bench", "compute_audit_v1.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestComputeAudit_ValidateGoldenFixture(t *testing.T) {
	var audit ComputeAudit
	if err := json.Unmarshal(computeAuditFixture(t), &audit); err != nil {
		t.Fatal(err)
	}
	if verr := audit.Validate(); verr != nil {
		t.Fatalf("validate: %v", verr)
	}
}

func TestComputeAudit_RejectMissingCost(t *testing.T) {
	var audit ComputeAudit
	if err := json.Unmarshal(computeAuditFixture(t), &audit); err != nil {
		t.Fatal(err)
	}
	audit.Cost.EstimatedCostUSD = 0
	if verr := audit.Validate(); verr == nil {
		t.Fatal("expected error for missing estimated_cost_usd")
	}
}

func TestComputeAudit_RejectUnattributedOver2Pct(t *testing.T) {
	var audit ComputeAudit
	if err := json.Unmarshal(computeAuditFixture(t), &audit); err != nil {
		t.Fatal(err)
	}
	audit.Cost.UnattributedPct = 2.1
	if verr := audit.Validate(); verr == nil {
		t.Fatal("expected error for unattributed_pct > 2%")
	}
}

func TestComputeAudit_NormalizeLegacyCostField(t *testing.T) {
	audit := ComputeAudit{
		SchemaVersion:    SchemaComputeAuditV1,
		RunID:            "r1",
		ExecutionProfile: "modal_cuda",
		OperatingMode:    "batch_queue",
		Cost: AuditCost{
			EstimatedCostUSD:    1.0,
			CostPerAudioHourUSD: 0.0194,
		},
	}
	audit.NormalizeAliases()
	if audit.Cost.CostPerProcessedAudioHourUSD != 0.0194 {
		t.Fatalf("alias normalize: got %v", audit.Cost.CostPerProcessedAudioHourUSD)
	}
}

package bench_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/bench"
)

func TestSchema_AllFixtures(t *testing.T) {
	root := bench.FixtureRoot()
	files := []string{
		"compute_audit_v1.json",
		"pyannote_stage_map_v1.json",
	}
	for _, name := range files {
		t.Run(name, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(b) {
				t.Fatal("invalid json")
			}
		})
	}
	var audit artifacts.ComputeAudit
	if err := json.Unmarshal(mustRead(t, filepath.Join(root, "compute_audit_v1.json")), &audit); err != nil {
		t.Fatal(err)
	}
	if verr := audit.Validate(); verr != nil {
		t.Fatal(verr)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

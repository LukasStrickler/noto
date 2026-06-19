package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

// Store persists benchmark run artifacts under ConfigDir/benchmarks/.
type Store struct {
	Root string
}

func NewStore(configDir string) *Store {
	return &Store{Root: filepath.Join(configDir, "benchmarks")}
}

func (s *Store) RunDir(runID string) string {
	return filepath.Join(s.Root, "runs", runID)
}

// RequiredArtifacts for ledger append (§22.15).
var RequiredArtifacts = []string{
	"manifest.json",
	"compute_audit.json",
	"metrics.json",
	"trace_summary.json",
	"compare.json",
}

func (s *Store) ValidateArtifactTree(runID string) error {
	return s.ValidateArtifactTreeExcept(runID)
}

// ValidateArtifactTreeExcept checks the required artifact tree but tolerates the
// named files being absent. Used to seed the first baseline winner, which has no
// compare.json (nothing to compare against) yet must still carry the rest of the
// audited tree.
func (s *Store) ValidateArtifactTreeExcept(runID string, skip ...string) error {
	skipSet := make(map[string]bool, len(skip))
	for _, n := range skip {
		skipSet[n] = true
	}
	dir := s.RunDir(runID)
	for _, name := range RequiredArtifacts {
		if skipSet[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("missing %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) WriteJSON(runID, name string, v any) error {
	dir := s.RunDir(runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o600)
}

func (s *Store) LoadRunBundle(runID string) (corebench.RunBundle, error) {
	dir := s.RunDir(runID)
	var bundle corebench.RunBundle

	if err := readJSON(filepath.Join(dir, "manifest.json"), &bundle.Manifest); err != nil {
		return bundle, err
	}
	var audit artifacts.ComputeAudit
	if err := readJSON(filepath.Join(dir, "compute_audit.json"), &audit); err != nil {
		return bundle, err
	}
	audit.NormalizeAliases()
	bundle.CostPerProcessedHour = audit.Cost.CostPerProcessedAudioHourUSD
	if err := readJSON(filepath.Join(dir, "trace_summary.json"), &bundle.TraceSummary); err != nil {
		return bundle, err
	}
	if err := readJSON(filepath.Join(dir, "metrics.json"), &bundle.Metrics); err != nil {
		return bundle, err
	}
	return bundle, nil
}

func (s *Store) LoadCompare(runID string) (corebench.CompareResult, error) {
	var cmp corebench.CompareResult
	err := readJSON(filepath.Join(s.RunDir(runID), "compare.json"), &cmp)
	return cmp, err
}

func (s *Store) LoadTraceSummary(runID string) (corebench.TraceSummary, error) {
	var trace corebench.TraceSummary
	err := readJSON(filepath.Join(s.RunDir(runID), "trace_summary.json"), &trace)
	return trace, err
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// WriteCompare saves compare.json for a run.
func (s *Store) WriteCompare(runID string, cmp corebench.CompareResult) error {
	return s.WriteJSON(runID, "compare.json", cmp)
}

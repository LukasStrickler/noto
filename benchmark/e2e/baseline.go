package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Baseline is the pinned set of per-stage metrics the regression gate guards.
// Each metric is "lower is better" (WER/DER/cpWER/SA-WER and RTF), so a run
// regresses when its value exceeds the pinned value plus tolerance.
//
// The committed baseline is re-pinned whenever the local runtime or a processing
// step changes (set BENCH_REPIN=1 to rewrite it from the current run). Runtime /
// Compute record what produced the numbers so a Mac/GPU run is never compared
// against a CPU pin by accident.
type Baseline struct {
	Runtime string            `json:"runtime"`
	Compute string            `json:"compute"`
	Pinned  string            `json:"pinned"`
	Note    string            `json:"note,omitempty"`
	Metrics map[string]Metric `json:"metrics"`
}

// Metric is a pinned value and the slack allowed before it counts as a regression.
type Metric struct {
	Value     float64 `json:"value"`
	Tolerance float64 `json:"tolerance"`
}

// Delta is one metric's baseline-vs-current comparison.
type Delta struct {
	Name      string
	Baseline  float64
	Current   float64
	Tolerance float64
	Missing   bool // metric pinned but not produced by the run
	Regressed bool
}

// LoadBaseline reads a pinned baseline from disk.
func LoadBaseline(path string) (*Baseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("parse baseline %s: %w", path, err)
	}
	return &b, nil
}

// Compare checks current metrics against the baseline, returning one Delta per
// pinned metric (sorted by name). A metric is regressed when current exceeds
// value+tolerance, or missing from the run entirely.
func (b *Baseline) Compare(current map[string]float64) []Delta {
	names := make([]string, 0, len(b.Metrics))
	for n := range b.Metrics {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Delta, 0, len(names))
	for _, n := range names {
		m := b.Metrics[n]
		cur, ok := current[n]
		d := Delta{Name: n, Baseline: m.Value, Current: cur, Tolerance: m.Tolerance}
		if !ok {
			d.Missing = true
			d.Regressed = true
		} else if cur > m.Value+m.Tolerance {
			d.Regressed = true
		}
		out = append(out, d)
	}
	return out
}

// Regressed reports whether any delta is a regression.
func Regressed(deltas []Delta) bool {
	for _, d := range deltas {
		if d.Regressed {
			return true
		}
	}
	return false
}

// Repin returns a copy of the baseline with each metric's Value set to the
// current run (tolerances preserved; metrics absent from the run dropped) and the
// Pinned date updated. Write it back to re-pin.
func (b *Baseline) Repin(current map[string]float64, date string) *Baseline {
	next := &Baseline{
		Runtime: b.Runtime,
		Compute: b.Compute,
		Pinned:  date,
		Note:    b.Note,
		Metrics: map[string]Metric{},
	}
	for name, cur := range current {
		tol := 0.0
		if old, ok := b.Metrics[name]; ok {
			tol = old.Tolerance
		}
		next.Metrics[name] = Metric{Value: cur, Tolerance: tol}
	}
	return next
}

// Write persists a baseline as indented JSON.
func (b *Baseline) Write(path string) error {
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

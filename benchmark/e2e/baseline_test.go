package e2e

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/bench"
	"github.com/lukasstrickler/noto/benchmark/metrics"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
)

const (
	baselinePath = "baseline.json"
	runsLog      = "runs.jsonl" // local run history (gitignored); trend source
)

// currentMetrics runs the deterministic oracle pipeline over a meeting and
// returns the per-stage scores keyed the way baseline.json pins them: the STT
// atomic WER, the diarization atomic DER, and the chained cpWER / SA-WER.
func currentMetrics(t *testing.T, m dataset.Meeting) map[string]float64 {
	t.Helper()
	ctx := context.Background()

	// STT atomic
	localSTT := stt.NewLocalSTT(OracleSTT{Words: m.Words})
	tr, err := localSTT.Transcribe(ctx, nil, stt.TranscribeOptions{MeetingID: m.ID})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	var hypText []string
	for _, w := range tr.Words {
		hypText = append(hypText, w.Text)
	}
	wer := metrics.WER(m.Reference(), metrics.Normalize(strings.Join(hypText, " ")))

	// Diarization atomic
	localDiar := diarize.NewLocalDiarizer(OracleDiarizer{Turns: m.Turns})
	turns, err := localDiar.Diarize(ctx, nil, diarize.DiarizeOptions{MeetingID: m.ID})
	if err != nil {
		t.Fatalf("diarize: %v", err)
	}
	hypSegs := make([]metrics.Segment, len(turns))
	for i, tn := range turns {
		hypSegs[i] = metrics.Segment{Speaker: tn.Speaker, Start: tn.StartSeconds, End: tn.EndSeconds}
	}
	der := metrics.DER(m.Segments(), hypSegs, metrics.DefaultDEROptions())

	// Chained cpWER / SA-WER
	merged, _, err := RunChain(ctx, nil, m.ID, OracleSTT{Words: m.Words}, OracleDiarizer{Turns: m.Turns})
	if err != nil {
		t.Fatalf("chain: %v", err)
	}
	hyp := hypothesisBySpeaker(merged)
	cp := metrics.CpWER(m.ReferenceBySpeaker(), hyp)
	sa := metrics.SAWER(m.ReferenceBySpeaker(), hyp, identityMapping(m))

	return map[string]float64{
		"stt.wer":   wer.Rate,
		"diar.der":  der.Rate,
		"e2e.cpwer": cp.Rate,
		"e2e.sawer": sa.Rate,
	}
}

// TestBaselineCompare unit-tests the gate logic with no assets: within tolerance
// passes, beyond it regresses, and a missing metric is a regression.
func TestBaselineCompare(t *testing.T) {
	b := &Baseline{Metrics: map[string]Metric{
		"stt.wer":  {Value: 0.10, Tolerance: 0.02},
		"diar.der": {Value: 0.15, Tolerance: 0.0},
	}}
	deltas := b.Compare(map[string]float64{
		"stt.wer":  0.11, // within tolerance → ok
		"diar.der": 0.16, // beyond tolerance → regress
		// (no missing here)
	})
	got := map[string]bool{}
	for _, d := range deltas {
		got[d.Name] = d.Regressed
	}
	if got["stt.wer"] {
		t.Error("stt.wer within tolerance should not regress")
	}
	if !got["diar.der"] {
		t.Error("diar.der beyond tolerance should regress")
	}
	if !Regressed(deltas) {
		t.Error("Regressed() should be true")
	}

	// a pinned metric missing from the run is a regression
	missing := b.Compare(map[string]float64{"stt.wer": 0.10})
	for _, d := range missing {
		if d.Name == "diar.der" && (!d.Missing || !d.Regressed) {
			t.Errorf("missing metric should regress: %+v", d)
		}
	}
}

// TestRepin round-trips the re-pin transform without touching disk.
func TestRepin(t *testing.T) {
	b := &Baseline{
		Runtime: "oracle", Compute: "cpu", Pinned: "2026-06-10",
		Metrics: map[string]Metric{"stt.wer": {Value: 0.0, Tolerance: 0.01}},
	}
	next := b.Repin(map[string]float64{"stt.wer": 0.12}, "2026-07-01")
	if next.Metrics["stt.wer"].Value != 0.12 || next.Metrics["stt.wer"].Tolerance != 0.01 {
		t.Errorf("repin = %+v, want value 0.12 tol 0.01", next.Metrics["stt.wer"])
	}
	if next.Pinned != "2026-07-01" || next.Runtime != "oracle" {
		t.Errorf("repin metadata = %+v", next)
	}
}

// TestBaselineRegression is the live gate (BENCH_DEEP=1): it loads the committed
// baseline, runs the deterministic oracle pipeline, logs an accuracy delta table,
// and fails on any regression. With BENCH_REPIN=1 it rewrites baseline.json from
// the current run instead of asserting.
func TestBaselineRegression(t *testing.T) {
	if os.Getenv("BENCH_DEEP") != "1" {
		t.Skip("set BENCH_DEEP=1 to run the regression gate")
	}
	base, err := LoadBaseline(baselinePath)
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	current := currentMetrics(t, fixture())

	if os.Getenv("BENCH_REPIN") == "1" {
		next := base.Repin(current, "2026-06-10")
		if err := next.Write(baselinePath); err != nil {
			t.Fatalf("repin write: %v", err)
		}
		t.Logf("re-pinned %s (runtime=%s compute=%s)", baselinePath, next.Runtime, next.Compute)
		return
	}

	t.Logf("baseline runtime=%s compute=%s pinned=%s", base.Runtime, base.Compute, base.Pinned)
	t.Logf("%-12s %10s %10s %10s %s", "metric", "baseline", "current", "tol", "status")
	for _, d := range base.Compare(current) {
		status := "ok"
		switch {
		case d.Missing:
			status = "MISSING"
		case d.Regressed:
			status = "REGRESSED"
		}
		t.Logf("%-12s %10.3f %10.3f %10.3f %s", d.Name, d.Baseline, d.Current, d.Tolerance, status)
	}

	// Record this run and show the trend vs the previous one — "are we improving
	// or degrading over time", independent of the fixed baseline.
	bench.Track(t, runsLog, "e2e", base.Runtime, current)

	if deltas := base.Compare(current); Regressed(deltas) {
		t.Fatalf("regression vs %s — see table above", baselinePath)
	}
}

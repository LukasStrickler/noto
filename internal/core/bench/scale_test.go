package bench_test

import (
	"math"
	"strings"
	"testing"

	corebench "github.com/lukasstrickler/noto/internal/core/bench"
)

func approxUSD(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-4 {
		t.Fatalf("%s = %v, want ~%v", name, got, want)
	}
}

// $/audio-hr must fall toward the marginal floor as scale grows — the whole point
// of the model. With $0.05 fixed and $0.012/hr marginal: 1h reads 0.062, 10h
// reads 0.017, and the floor is 0.012.
func TestScaleModel_PerHourFallsTowardFloor(t *testing.T) {
	m := corebench.ScaleModel{FixedUSD: 0.05, MarginalUSDPerAudioHour: 0.012}
	approxUSD(t, "1h", m.CostPerAudioHour(1), 0.062)
	approxUSD(t, "10h", m.CostPerAudioHour(10), 0.017)
	approxUSD(t, "100h", m.CostPerAudioHour(100), 0.0125)
	approxUSD(t, "floor", m.Floor(), 0.012)
	// Per-hour is monotonically decreasing in scale.
	if !(m.CostPerAudioHour(1) > m.CostPerAudioHour(10) && m.CostPerAudioHour(10) > m.CostPerAudioHour(100)) {
		t.Fatal("$/hr must decrease as scale grows")
	}
}

func TestScaleModel_HoursToReach(t *testing.T) {
	m := corebench.ScaleModel{FixedUSD: 0.05, MarginalUSDPerAudioHour: 0.008}
	// target 0.01 > floor 0.008 → reachable. h = fixed/(target-marginal) =
	// 0.05/0.002 = 25h.
	h, ok := m.HoursToReach(0.01)
	if !ok {
		t.Fatal("0.01 must be reachable above a 0.008 floor")
	}
	approxUSD(t, "hours-to-0.01", h, 25)
	approxUSD(t, "cost-per-hr at that scale", m.CostPerAudioHour(h), 0.01)

	// A target below the floor is unreachable by scale.
	if _, ok := m.HoursToReach(0.005); ok {
		t.Fatal("target below the marginal floor must be unreachable by scale")
	}
}

// Fitting two runs at different scales recovers the fixed/marginal split exactly.
func TestFitScaleModel_RecoversFixedAndMarginal(t *testing.T) {
	// True model: fixed 0.04, marginal 0.01/hr.
	pts := []corebench.ScalePoint{
		{AudioHours: 2, CostUSD: 0.04 + 0.01*2},   // 0.06
		{AudioHours: 10, CostUSD: 0.04 + 0.01*10}, // 0.14
	}
	m, ok := corebench.FitScaleModel(pts)
	if !ok {
		t.Fatal("two distinct-scale points must fit")
	}
	approxUSD(t, "fixed", m.FixedUSD, 0.04)
	approxUSD(t, "marginal", m.MarginalUSDPerAudioHour, 0.01)
}

func TestFitScaleModel_RejectsDegenerate(t *testing.T) {
	if _, ok := corebench.FitScaleModel([]corebench.ScalePoint{{AudioHours: 5, CostUSD: 0.1}}); ok {
		t.Fatal("single point cannot separate fixed from marginal")
	}
	same := []corebench.ScalePoint{{AudioHours: 5, CostUSD: 0.1}, {AudioHours: 5, CostUSD: 0.12}}
	if _, ok := corebench.FitScaleModel(same); ok {
		t.Fatal("points at one scale cannot determine the slope")
	}
}

// The single-run idle decomposition: idle dollars are the fixed proxy, busy
// dollars are marginal. total 0.16, idle 0.04, 9.5h → fixed 0.04, marginal
// (0.16-0.04)/9.5 ≈ 0.01263/hr.
func TestScaleModelFromIdle(t *testing.T) {
	m, ok := corebench.ScaleModelFromIdle(0.16, 0.04, 9.5)
	if !ok {
		t.Fatal("valid single run must produce a model")
	}
	approxUSD(t, "fixed", m.FixedUSD, 0.04)
	approxUSD(t, "marginal", m.MarginalUSDPerAudioHour, 0.0126)
	if _, ok := corebench.ScaleModelFromIdle(0.16, 0.04, 0); ok {
		t.Fatal("zero audio hours must be rejected")
	}
}

func TestProjectScale_ReachableTargetReportsCrossing(t *testing.T) {
	m := corebench.ScaleModel{FixedUSD: 0.05, MarginalUSDPerAudioHour: 0.008}
	pr := corebench.ProjectScale(m, 0.01, nil) // default scales 1/10/100
	if pr.SchemaVersion != corebench.SchemaScaleProjectionV1 {
		t.Fatalf("schema=%q", pr.SchemaVersion)
	}
	if len(pr.Points) != 3 || pr.Points[0].AudioHours != 1 || pr.Points[2].AudioHours != 100 {
		t.Fatalf("default scale points wrong: %+v", pr.Points)
	}
	if !pr.TargetReachableByScale {
		t.Fatal("0.01 above a 0.008 floor must be reachable")
	}
	approxUSD(t, "crossing hours", pr.HoursToReachTarget, 25)
	if pr.FloorUSDPerAudioHour != 0.008 {
		t.Fatalf("floor=%v", pr.FloorUSDPerAudioHour)
	}
}

func TestEvaluateScaleReadiness_AllGatesMustPass(t *testing.T) {
	th := corebench.DefaultScaleReadinessThresholds() // busy≥70, 10h cost ≤ anchor 0.0194
	// A subsample that is busy, accurate, and cheap at 10h → ready.
	good := corebench.ScaleModel{FixedUSD: 0.05, MarginalUSDPerAudioHour: 0.012} // 10h → 0.017 ≤ 0.0194
	if r := corebench.EvaluateScaleReadiness(82, true, good, th); !r.Ready {
		t.Fatalf("busy+accurate+on-track must be ready, reasons=%v", r.Reasons)
	}

	// Quality failing alone blocks it.
	if r := corebench.EvaluateScaleReadiness(82, false, good, th); r.Ready || !hasReason(r.Reasons, "quality") {
		t.Fatalf("bad quality must block: %+v", r)
	}
	// Low utilization alone blocks it.
	if r := corebench.EvaluateScaleReadiness(45, true, good, th); r.Ready || !hasReason(r.Reasons, "utilization") {
		t.Fatalf("low busy%% must block: %+v", r)
	}
	// On-track quality+busy but the 10h cost is above the ceiling → blocked.
	pricey := corebench.ScaleModel{FixedUSD: 0.20, MarginalUSDPerAudioHour: 0.018} // 10h → 0.038 > 0.0194
	if r := corebench.EvaluateScaleReadiness(82, true, pricey, th); r.Ready || !hasReason(r.Reasons, "10h") {
		t.Fatalf("10h cost over ceiling must block: %+v", r)
	}
}

func TestEvaluateScaleReadiness_ReadsCostAtReferenceScale(t *testing.T) {
	m := corebench.ScaleModel{FixedUSD: 0.05, MarginalUSDPerAudioHour: 0.012}
	r := corebench.EvaluateScaleReadiness(90, true, m, corebench.DefaultScaleReadinessThresholds())
	// fixed/10 + marginal = 0.005 + 0.012 = 0.017.
	approxUSD(t, "cost@10h", r.CostPerAudioHourAt10hUSD, 0.017)
	if r.ReferenceScaleHours != 10 || r.SchemaVersion != corebench.SchemaScaleReadinessV1 {
		t.Fatalf("bad metadata: %+v", r)
	}
}

func hasReason(reasons []string, substr string) bool {
	for _, r := range reasons {
		if strings.Contains(r, substr) {
			return true
		}
	}
	return false
}

func TestProjectScale_UnreachableTarget(t *testing.T) {
	// Marginal floor 0.012 already above the 0.01 target — no scale gets there.
	m := corebench.ScaleModel{FixedUSD: 0.05, MarginalUSDPerAudioHour: 0.012}
	pr := corebench.ProjectScale(m, 0.01, []float64{10})
	if pr.TargetReachableByScale {
		t.Fatal("target below the floor must be unreachable")
	}
	if pr.HoursToReachTarget != 0 {
		t.Fatalf("unreachable target must report 0 hours, got %v", pr.HoursToReachTarget)
	}
	if len(pr.Notes) == 0 {
		t.Fatal("unreachable target must carry an explanatory note (needs a rate win, not scale)")
	}
}

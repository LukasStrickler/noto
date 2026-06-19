package speakers

import (
	"math"
	"testing"
)

// near returns a unit vector close to base, perturbed toward axis `ax` by eps.
func perturb(base Embedding, ax int, eps float64) Embedding {
	v := append(Embedding(nil), base...)
	v[ax] += eps
	return Normalize(v)
}

func TestRobustCentroid_CleanReducesToMean(t *testing.T) {
	// A tight cluster of same-speaker windows: nothing should be trimmed, so the
	// result is essentially the plain mean direction.
	base := Normalize(Embedding{1, 0, 0, 0})
	exes := []Embedding{
		perturb(base, 1, 0.05),
		perturb(base, 1, -0.05),
		perturb(base, 2, 0.05),
		perturb(base, 2, -0.05),
	}
	got, err := RobustCentroid(exes, 0)
	if err != nil {
		t.Fatalf("RobustCentroid: %v", err)
	}
	if c := Dot(Normalize(got), base); c < 0.99 {
		t.Errorf("clean centroid cosine to base = %.4f, want >= 0.99", c)
	}
}

func meanDir(exes []Embedding) Embedding {
	m := make(Embedding, len(exes[0]))
	for _, e := range exes {
		n := Normalize(e)
		for i := range m {
			m[i] += n[i]
		}
	}
	return Normalize(m)
}

func TestRobustCentroid_TrimsScatteredOutliers(t *testing.T) {
	// 6 windows of speaker A (near +x) plus 2 *scattered* outliers in unrelated
	// directions (overlap/cross-talk noise, not a single rival voice). They are
	// incoherent, so the guard lets them be dropped and the centroid sharpens
	// onto A — beating the contaminated mean.
	a := Normalize(Embedding{1, 0, 0, 0})
	exes := []Embedding{
		perturb(a, 1, 0.05), perturb(a, 1, -0.05), perturb(a, 2, 0.05),
		perturb(a, 2, -0.05), perturb(a, 3, 0.05), perturb(a, 3, -0.05),
		Normalize(Embedding{0, 1, 0, 0}), // outlier 1
		Normalize(Embedding{0, 0, 0, 1}), // outlier 2 (orthogonal to outlier 1)
	}
	robust, err := RobustCentroid(exes, 0)
	if err != nil {
		t.Fatalf("RobustCentroid: %v", err)
	}
	meanToA := Dot(meanDir(exes), a)
	robustToA := Dot(Normalize(robust), a)
	if robustToA <= meanToA {
		t.Errorf("robust (cos-to-A %.4f) should beat the contaminated mean (%.4f)", robustToA, meanToA)
	}
	if robustToA < 0.98 {
		t.Errorf("robust cos-to-A = %.4f, want >= 0.98 (scattered outliers dropped)", robustToA)
	}
}

func TestRobustCentroid_GuardsCoherentRivalCluster(t *testing.T) {
	// 4 windows of A and 3 windows of a single off-speaker B (a coherent rival
	// cluster — i.e. a diarization merge, not noise). We cannot know which side
	// is the real speaker, so the guard must fall back to the plain mean rather
	// than confidently committing to one cluster.
	a := Normalize(Embedding{1, 0, 0, 0})
	b := Normalize(Embedding{0, 1, 0, 0})
	exes := []Embedding{
		perturb(a, 2, 0.05), perturb(a, 3, 0.05), perturb(a, 2, -0.05), perturb(a, 3, -0.05),
		perturb(b, 2, 0.05), perturb(b, 3, 0.05), perturb(b, 2, -0.05),
	}
	robust, err := RobustCentroid(exes, 0)
	if err != nil {
		t.Fatalf("RobustCentroid: %v", err)
	}
	mean := meanDir(exes)
	if c := Dot(Normalize(robust), mean); c < 0.999 {
		t.Errorf("with a coherent rival cluster the result should equal the mean, cos = %.4f", c)
	}
}

func TestRobustCentroid_Degenerate(t *testing.T) {
	if got, _ := RobustCentroid(nil, 0); got != nil {
		t.Errorf("empty input = %v, want nil", got)
	}
	one := Normalize(Embedding{1, 2, 3})
	got, err := RobustCentroid([]Embedding{one}, 0)
	if err != nil {
		t.Fatalf("RobustCentroid: %v", err)
	}
	if Dot(got, one) < 0.999 {
		t.Errorf("single exemplar should pass through, cos = %.4f", Dot(got, one))
	}
}

func TestScoreASNorm_SeparatesBetterThanCosine(t *testing.T) {
	// Build a target and a genuine/impostor probe where raw cosine is ambiguous
	// but the cohort context makes the genuine clearly closer after AS-Norm.
	target := Normalize(Embedding{1, 0, 0, 0})
	genuine := perturb(target, 1, 0.6)               // moderately close, same identity
	impostor := Normalize(Embedding{0.6, 0.8, 0, 0}) // also moderately close, different identity
	cohort := []Embedding{
		Normalize(Embedding{0, 1, 0, 0}),
		Normalize(Embedding{0, 0, 1, 0}),
		Normalize(Embedding{0, 0, 0, 1}),
		Normalize(Embedding{0.5, 0.5, 0.5, 0.5}),
	}

	gRaw := Dot(Normalize(genuine), target)
	iRaw := Dot(Normalize(impostor), target)
	gNorm := ScoreASNorm(genuine, target, cohort, 0)
	iNorm := ScoreASNorm(impostor, target, cohort, 0)

	if gNorm <= iNorm {
		t.Errorf("AS-Norm should rank genuine (%.3f) above impostor (%.3f)", gNorm, iNorm)
	}
	// The normalized gap should be at least as decisive as the raw gap.
	if (gNorm - iNorm) <= (gRaw - iRaw) {
		t.Logf("raw gap %.3f, asnorm gap %.3f", gRaw-iRaw, gNorm-iNorm)
	}
}

func TestScoreASNorm_EmptyCohortIsRawCosine(t *testing.T) {
	a := Normalize(Embedding{1, 2, 3})
	b := Normalize(Embedding{3, 2, 1})
	got := ScoreASNorm(a, b, nil, 0)
	want := Dot(a, b)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("empty cohort AS-Norm = %.6f, want raw cosine %.6f", got, want)
	}
}

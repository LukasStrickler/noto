package speakers

import (
	"math"
	"sort"
)

// DefaultKeepCos is the cosine-to-medoid floor below which an exemplar is
// treated as an outlier and dropped from the robust centroid. Same-speaker
// windows within a recording sit well above this; a turn a diarizer misassigned
// to this speaker lands near a *different* voice and falls below it.
const DefaultKeepCos = 0.5

// Bimodality guard tunables. When the dropped exemplars form a coherent rival
// cluster (a sizable, internally-similar group) we cannot tell which cluster is
// the real speaker — that is a diarization error, not stray noise — so we refuse
// to commit and fall back to the plain mean. This keeps RobustCentroid from ever
// being worse than the mean while still tightening clean sets and shedding
// scattered outliers.
const (
	bimodalMinFrac  = 0.20 // dropped set must be at least this fraction of n to be "rival-sized"
	bimodalCohesion = 0.45 // ...and this internally similar (mean cosine to its own medoid) to count as a cluster
)

// RobustCentroid aggregates per-window/exemplar embeddings into one profile
// vector, rejecting outliers (e.g. turns a diarizer misassigned to this
// speaker) before averaging.
//
// It anchors on the medoid — the exemplar most central by summed cosine — and
// keeps exemplars within keepCos of it. Three regimes:
//
//   - clean / scattered outliers: the dropped exemplars are few or incoherent
//     (overlap, silence, stray cross-talk) → drop them and average the rest,
//     which tightens the profile.
//   - coherent rival cluster: the dropped exemplars are sizable AND internally
//     similar → a second voice the diarizer merged in; we can't know which side
//     is the real speaker, so we fall back to the plain mean rather than risk
//     committing to the wrong cluster.
//
// keepCos<=0 selects DefaultKeepCos. The medoid is always retained, so a
// degenerate set never returns empty.
func RobustCentroid(exemplars []Embedding, keepCos float64) (Embedding, error) {
	if len(exemplars) == 0 {
		return nil, nil
	}
	if keepCos <= 0 {
		keepCos = DefaultKeepCos
	}
	dim := len(exemplars[0])
	norm := make([]Embedding, len(exemplars))
	for i, e := range exemplars {
		if len(e) != dim {
			return nil, DimError(dim, len(e))
		}
		norm[i] = Normalize(e)
	}
	if len(norm) <= 2 {
		return meanDirection(norm), nil
	}

	medoid := medoidOf(norm)
	var kept, dropped []Embedding
	for i := range norm {
		if Dot(norm[i], medoid) >= keepCos {
			kept = append(kept, norm[i])
		} else {
			dropped = append(dropped, norm[i])
		}
	}
	if len(kept) == 0 {
		return meanDirection(norm), nil
	}
	// Bimodality guard: a sizable, cohesive dropped cluster is a rival speaker,
	// not noise — don't gamble on which side is real.
	if len(dropped) >= 2 &&
		float64(len(dropped)) >= bimodalMinFrac*float64(len(norm)) &&
		meanPairwiseCos(dropped) >= bimodalCohesion {
		return meanDirection(norm), nil
	}
	return meanDirection(kept), nil
}

// meanPairwiseCos is the average cosine over all unordered pairs — a measure of
// how tightly a set clusters (high = one coherent voice, low = a scatter of
// unrelated windows).
func meanPairwiseCos(v []Embedding) float64 {
	if len(v) < 2 {
		return 1
	}
	var sum float64
	var n int
	for i := 0; i < len(v); i++ {
		for j := i + 1; j < len(v); j++ {
			sum += Dot(v[i], v[j])
			n++
		}
	}
	return sum / float64(n)
}

// medoidOf returns the exemplar maximizing summed cosine to the others. O(n^2)
// over a handful of windows, so the cost is negligible.
func medoidOf(v []Embedding) Embedding {
	best, bestSum := 0, math.Inf(-1)
	for i := range v {
		var s float64
		for j := range v {
			if i != j {
				s += Dot(v[i], v[j])
			}
		}
		if s > bestSum {
			bestSum, best = s, i
		}
	}
	return v[best]
}

// meanDirection returns the L2-normalized mean of unit vectors.
func meanDirection(v []Embedding) Embedding {
	if len(v) == 0 {
		return nil
	}
	sum := make([]float64, len(v[0]))
	for _, e := range v {
		for i, x := range e {
			sum[i] += x
		}
	}
	return Normalize(sum)
}

// DefaultCohortTopK is the number of nearest cohort speakers used to estimate
// the impostor score statistics in AS-Norm.
const DefaultCohortTopK = 200

// ScoreASNorm returns the adaptive-symmetric-normalized similarity between
// query and target given a background cohort of impostor embeddings.
//
// A raw cosine is not calibrated: a "generic" voice scores moderately against
// many people, and a channel shift (e.g. telephone band) moves every score, so
// a single global accept/reject threshold is fragile (this is what blows the
// stranger false-accept rate up under channel mismatch). AS-Norm rescales the
// raw score into a z-score against the query's and the target's own top-K
// cohort similarities and averages the two, making the decision boundary stable
// across speakers and conditions. With an empty cohort it returns the raw
// cosine. topK<=0 selects DefaultCohortTopK.
func ScoreASNorm(query, target Embedding, cohort []Embedding, topK int) float64 {
	q := Normalize(query)
	t := Normalize(target)
	raw := Dot(q, t)
	if len(cohort) == 0 {
		return raw
	}
	qs := make([]float64, 0, len(cohort))
	ts := make([]float64, 0, len(cohort))
	for _, c := range cohort {
		cn := Normalize(c)
		qs = append(qs, Dot(q, cn))
		ts = append(ts, Dot(t, cn))
	}
	muQ, sdQ := topKMeanStd(qs, topK)
	muT, sdT := topKMeanStd(ts, topK)
	return 0.5 * ((raw-muQ)/sdQ + (raw-muT)/sdT)
}

// topKMeanStd returns the mean and (population) standard deviation of the top-k
// largest values in sims, with a small floor on the deviation to avoid a
// divide-by-zero. It sorts a copy so the caller's slice is untouched.
func topKMeanStd(sims []float64, k int) (mu, sd float64) {
	if k <= 0 || k > len(sims) {
		k = len(sims)
	}
	cp := append([]float64(nil), sims...)
	sort.Sort(sort.Reverse(sort.Float64Slice(cp)))
	cp = cp[:k]
	for _, s := range cp {
		mu += s
	}
	mu /= float64(k)
	for _, s := range cp {
		sd += (s - mu) * (s - mu)
	}
	return mu, math.Sqrt(sd/float64(k)) + 1e-9
}

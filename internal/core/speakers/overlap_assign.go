package speakers

import "math"

// --- Overlap stream → speaker assignment (ADR 0007) ---
//
// The overlap refinement pass separates an overlap region into per-speaker audio
// streams and transcribes each, but separation does not name the speakers. Before
// the transcript merge (providers/merge.MergeOverlapStreams) each stream must be
// attributed to one of the meeting's actual speakers — and that cannot be done
// from text (both streams are the same words' region); it needs ACOUSTIC identity.
// AssignStreams embeds-then-matches: given each separated stream's ECAPA embedding
// and the candidate speakers' reference embeddings (their meeting centroids), it
// returns the speaker id for each stream.
//
// Forward infra: the embeddings come from the (gated, remote-compute) separation
// pass; this pure assignment is tested now and called once that pass is wired.

// SpeakerCandidate is one speaker an overlap stream may belong to: their id and a
// reference embedding (typically the speaker's meeting centroid).
type SpeakerCandidate struct {
	ID        string
	Embedding Embedding
}

// maxAssignPerm guards the factorial one-to-one search; overlap regions have very
// few simultaneous speakers (2, occasionally 3-4), so this is never hit in practice.
const maxAssignPerm = 6

// AssignStreams maps each separated stream (by index) to one candidate speaker by
// acoustic identity (ECAPA cosine similarity). For EQUAL counts — the common
// 2-speaker overlap — it returns the one-to-one assignment maximizing total cosine
// similarity, so the streams get DISTINCT speakers (a greedy per-stream argmax could
// send both to the same dominant voice). With unequal counts, or too many speakers
// to permute, it falls back to each stream's single best candidate. A stream whose
// dimension mismatches every candidate, or when there are no candidates, gets "".
func AssignStreams(streams []Embedding, candidates []SpeakerCandidate) []string {
	out := make([]string, len(streams))
	if len(candidates) == 0 || len(streams) == 0 {
		return out
	}

	sim := func(s Embedding, c SpeakerCandidate) (float64, bool) {
		v, err := CosineSimilarity(s, c.Embedding)
		if err != nil {
			return 0, false
		}
		return v, true
	}

	if len(streams) == len(candidates) && len(streams) <= maxAssignPerm {
		idx := make([]int, len(streams))
		for i := range idx {
			idx[i] = i
		}
		best := math.Inf(-1)
		var bestPerm []int
		permuteInts(idx, 0, func(p []int) {
			total, ok := 0.0, true
			for i, ci := range p {
				v, valid := sim(streams[i], candidates[ci])
				if !valid {
					ok = false
					break
				}
				total += v
			}
			if ok && total > best {
				best = total
				bestPerm = append(bestPerm[:0:0], p...)
			}
		})
		if bestPerm != nil {
			for i, ci := range bestPerm {
				out[i] = candidates[ci].ID
			}
			return out
		}
		// no valid permutation (all dim mismatches) → greedy fallback below
	}

	for i, s := range streams {
		best := math.Inf(-1)
		for _, c := range candidates {
			if v, ok := sim(s, c); ok && v > best {
				best, out[i] = v, c.ID
			}
		}
	}
	return out
}

// permuteInts calls fn for every permutation of a (in place; fn must copy if it
// keeps the slice). Heap-free swap recursion; fine for the tiny n here.
func permuteInts(a []int, k int, fn func([]int)) {
	if k == len(a) {
		fn(a)
		return
	}
	for i := k; i < len(a); i++ {
		a[k], a[i] = a[i], a[k]
		permuteInts(a, k+1, fn)
		a[k], a[i] = a[i], a[k]
	}
}

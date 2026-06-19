package metrics

import "sort"

// CpWER computes the concatenated minimum-permutation word error rate: the joint
// "who said what" metric. Each speaker's tokens are concatenated into one stream,
// then hypothesis speakers are assigned to reference speakers to MINIMIZE the
// total word errors (a min-cost assignment, not a factorial search). cpWER is
// therefore permutation-agnostic — swapped anonymous diarization labels are
// forgiven — while still penalizing transcription errors and split/merged
// speakers (an extra or missing hypothesis speaker becomes all-insertions /
// all-deletions). The returned Result aggregates errors over the chosen
// assignment; RefLen is the total reference token count.
func CpWER(refBySpk, hypBySpk map[string][]string) Result {
	refLabels := sortedKeys(refBySpk)
	hypLabels := sortedKeys(hypBySpk)
	r, h := len(refLabels), len(hypLabels)

	totalRef := 0
	for _, toks := range refBySpk {
		totalRef += len(toks)
	}

	if r == 0 && h == 0 {
		return Result{}
	}

	// Square cost matrix: real/real = edit distance; an unmatched reference
	// speaker costs all deletions, an unmatched hypothesis speaker all
	// insertions; dummy/dummy = 0.
	n := max(r, h)
	cost := make([][]float64, n)
	for i := 0; i < n; i++ {
		cost[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			switch {
			case i < r && j < h:
				cost[i][j] = float64(editDistance(refBySpk[refLabels[i]], hypBySpk[hypLabels[j]]))
			case i < r: // reference speaker unmatched → deletions
				cost[i][j] = float64(len(refBySpk[refLabels[i]]))
			case j < h: // hypothesis speaker unmatched → insertions
				cost[i][j] = float64(len(hypBySpk[hypLabels[j]]))
			}
		}
	}

	assign := hungarian(cost)

	// Recompute the per-pair breakdown for the chosen assignment.
	res := Result{RefLen: totalRef}
	for i := 0; i < n; i++ {
		j := assign[i]
		switch {
		case i < r && j < h:
			w := WER(refBySpk[refLabels[i]], hypBySpk[hypLabels[j]])
			res.Sub += w.Sub
			res.Del += w.Del
			res.Ins += w.Ins
		case i < r:
			res.Del += len(refBySpk[refLabels[i]])
		case j < h:
			res.Ins += len(hypBySpk[hypLabels[j]])
		}
	}
	res.Rate = rate(res.Errors(), totalRef)
	return res
}

// SAWER computes the speaker-attributed WER under a FIXED hypothesis→reference
// speaker mapping (mapping[hypLabel] = refLabel). Unlike cpWER there is no
// permutation search: a hypothesis turn attributed to the wrong reference
// speaker is scored against that speaker's words and so is penalized. A
// hypothesis speaker mapped to "" or to a reference label not present becomes
// pure insertions; a reference speaker with no hypothesis words becomes pure
// deletions. Use this once identity has assigned stable speakers.
func SAWER(refBySpk, hypBySpk map[string][]string, mapping map[string]string) Result {
	// Gather hypothesis tokens per reference speaker (concatenate the hyp
	// speakers mapped to it, in sorted label order for determinism).
	hypForRef := make(map[string][]string)
	var unattributed []string
	for _, hl := range sortedKeys(hypBySpk) {
		rl, ok := mapping[hl]
		if !ok || rl == "" {
			unattributed = append(unattributed, hypBySpk[hl]...)
			continue
		}
		if _, present := refBySpk[rl]; !present {
			unattributed = append(unattributed, hypBySpk[hl]...)
			continue
		}
		hypForRef[rl] = append(hypForRef[rl], hypBySpk[hl]...)
	}

	res := Result{}
	totalRef := 0
	for _, rl := range sortedKeys(refBySpk) {
		ref := refBySpk[rl]
		totalRef += len(ref)
		w := WER(ref, hypForRef[rl])
		res.Sub += w.Sub
		res.Del += w.Del
		res.Ins += w.Ins
	}
	res.Ins += len(unattributed)
	res.RefLen = totalRef
	res.Rate = rate(res.Errors(), totalRef)
	return res
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func rate(errors, refLen int) float64 {
	switch {
	case refLen > 0:
		return float64(errors) / float64(refLen)
	case errors > 0:
		return 1.0
	default:
		return 0
	}
}

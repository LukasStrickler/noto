package bench

import "sort"

// repair_ceiling.go holds the one reference-guided oracle-ceiling algorithm the two
// repair-attempt loops share. The transcription loop (repair_attempt.go, WER over
// HypWords) and the diarization loop (repair_diar_attempt.go, DER over HypTurns) both
// answer the same question — "what is the MOST this alternate could buy if span
// selection were perfect?" — and computed it with byte-identical greedy logic that
// differed only in how an edit is applied and how a candidate set is scored. That
// duplication is the kind of thing that drifts: a change to the keep-rule (strictly-
// improves, a tolerance, ≤ vs <) had to be made twice. It now lives here once; the two
// domains pass their own apply/score and keep their domain-named wrappers.

// greedyCeiling commits candidate edits onto base — strongest local improvement first —
// keeping each only when it strictly lowers the whole-meeting score. It returns that
// best subset's score delta (≤ 0; round4'd) and the number of edits committed: the
// reference-guided UPPER BOUND on what the alternate could buy with perfect selection,
// with seam cost neutralized (a locally-good edit that shifts boundaries and hurts the
// whole-meeting score is dropped). It is a CEILING, not the production number —
// production must approximate this selection from confidence, not the reference.
//
// E is one candidate edit, S the running hypothesis state (a word list or a turn list).
// localDelta orders the greedy commit (most-negative = strongest local win first); apply
// splices an edit onto the running state; score is the whole-meeting WER/DER of a state.
func greedyCeiling[E any, S any](
	base S,
	cands []E,
	localDelta func(E) float64,
	apply func(S, E) S,
	score func(S) float64,
) (float64, int) {
	ordered := append([]E(nil), cands...)
	sort.SliceStable(ordered, func(i, j int) bool { return localDelta(ordered[i]) < localDelta(ordered[j]) })

	baseScore := score(base)
	running := base
	cur := baseScore
	committed := 0
	for _, c := range ordered {
		trial := apply(running, c)
		if s := score(trial); s < cur {
			running = trial
			cur = s
			committed++
		}
	}
	return round4(cur - baseScore), committed
}

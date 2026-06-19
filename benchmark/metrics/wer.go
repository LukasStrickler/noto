package metrics

// Result is a token-level error-rate breakdown shared by WER, cpWER, and SA-WER.
type Result struct {
	Sub    int     // substitutions
	Del    int     // deletions: reference tokens missing from the hypothesis
	Ins    int     // insertions: hypothesis tokens with no reference token
	RefLen int     // number of reference tokens (the denominator)
	Rate   float64 // (Sub+Del+Ins) / RefLen
}

// Errors returns the total edit count, Sub+Del+Ins.
func (r Result) Errors() int { return r.Sub + r.Del + r.Ins }

// WER computes the token-level word error rate of hyp against ref via Levenshtein
// edit distance, splitting the errors into substitutions, deletions, and
// insertions through a backtrace. Tokens are compared with ==; normalize them
// with Normalize first if needed.
//
// Convention for an empty reference: Rate is 0 when hyp is also empty, otherwise
// 1.0 (there is nothing to divide by, so a non-empty hypothesis is treated as
// fully in error). Errors() still reports the raw insertion count in that case.
func WER(ref, hyp []string) Result {
	n, m := len(ref), len(hyp)

	// dp[i][j] = edit distance between ref[:i] and hyp[:j].
	dp := make([][]int, n+1)
	for i := 0; i <= n; i++ {
		dp[i] = make([]int, m+1)
		dp[i][0] = i
	}
	for j := 0; j <= m; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if ref[i-1] == hyp[j-1] {
				dp[i][j] = dp[i-1][j-1]
				continue
			}
			dp[i][j] = min(dp[i-1][j-1], dp[i-1][j], dp[i][j-1]) + 1
		}
	}

	// Backtrace, preferring match > substitution > deletion > insertion on ties
	// so the split is deterministic.
	res := Result{RefLen: n}
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && ref[i-1] == hyp[j-1] && dp[i][j] == dp[i-1][j-1]:
			i, j = i-1, j-1
		case i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+1:
			res.Sub++
			i, j = i-1, j-1
		case i > 0 && dp[i][j] == dp[i-1][j]+1:
			res.Del++
			i--
		default: // insertion
			res.Ins++
			j--
		}
	}

	switch {
	case n > 0:
		res.Rate = float64(res.Errors()) / float64(n)
	case m > 0:
		res.Rate = 1.0
	}
	return res
}

// WERText normalizes ref and hyp with Normalize and returns WER over the tokens.
func WERText(ref, hyp string) Result {
	return WER(Normalize(ref), Normalize(hyp))
}

// AlignHyp returns one correctness label per HYPOTHESIS token, in hyp order:
// true when that token matched a reference token in the optimal alignment,
// false when it was a substitution or an insertion (both are hyp words the
// reference does not back). Deletions consume only a reference token and so
// produce no entry — the result always has len == len(hyp).
//
// This is the per-word ground truth the B6 calibration scorers pair with each
// word's model confidence: a well-calibrated model should assign these
// false-labelled words lower confidence than the true-labelled ones. It reuses
// the SAME edit-distance backtrace as WER (match > substitution > deletion >
// insertion on ties), so the labels are consistent with the reported WER split.
func AlignHyp(ref, hyp []string) []bool {
	n, m := len(ref), len(hyp)
	correct := make([]bool, m)
	if m == 0 {
		return correct
	}

	dp := make([][]int, n+1)
	for i := 0; i <= n; i++ {
		dp[i] = make([]int, m+1)
		dp[i][0] = i
	}
	for j := 0; j <= m; j++ {
		dp[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if ref[i-1] == hyp[j-1] {
				dp[i][j] = dp[i-1][j-1]
				continue
			}
			dp[i][j] = min(dp[i-1][j-1], dp[i-1][j], dp[i][j-1]) + 1
		}
	}

	// Same tie order as WER's backtrace; label hyp token j-1 on each step that
	// consumes it (match -> true, substitution/insertion -> false).
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && ref[i-1] == hyp[j-1] && dp[i][j] == dp[i-1][j-1]:
			correct[j-1] = true
			i, j = i-1, j-1
		case i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+1:
			correct[j-1] = false // substitution
			i, j = i-1, j-1
		case i > 0 && dp[i][j] == dp[i-1][j]+1:
			i-- // deletion: no hyp token consumed
		default:
			correct[j-1] = false // insertion
			j--
		}
	}
	return correct
}

// editDistance is the bare Levenshtein distance (no breakdown), used as the cost
// for speaker assignment in cpWER. Uses a rolling row for O(min) memory.
func editDistance(a, b []string) int {
	n, m := len(a), len(b)
	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}
	prev := make([]int, m+1)
	for j := 0; j <= m; j++ {
		prev[j] = j
	}
	cur := make([]int, m+1)
	for i := 1; i <= n; i++ {
		cur[0] = i
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1]
				continue
			}
			cur[j] = min(prev[j-1], prev[j], cur[j-1]) + 1
		}
		prev, cur = cur, prev
	}
	return prev[m]
}

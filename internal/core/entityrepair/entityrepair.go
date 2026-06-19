// Package entityrepair is a deterministic transcription repair technique: it snaps
// transcript words that are CLOSE-BUT-NOT-EXACT matches of known glossary entities to
// the canonical entity spelling. Entities (participant names, product/company terms,
// jargon) are exactly what a general ASR mis-recognizes, and they dominate the
// product-critical "who/what" accuracy (cpWER) — yet the recognizer has no way to know
// the meeting's vocabulary. This pass injects that knowledge AFTER decoding, with no GPU
// and no model.
//
// It is deliberately CONSERVATIVE, mirroring the benchmark repair gate's "never make it
// worse" discipline but applied online (no reference to score against):
//
//   - an EXACT normalized match is left alone (already correct);
//   - a match must clear a high similarity bar (MinSimilarity) AND the term must be
//     distinctive (MinTermLen normalized chars) — so common short words near an entity
//     ("mark" vs "Marc") are not rewritten by default;
//   - if the caller supplies calibrated confidence it can require the word to be
//     UNCERTAIN (LowConfidence), so confident-correct words are never touched. Confidence
//     is OPTIONAL because some recognizers (NeMo TDT) emit an entropy score that is good
//     for ranking but not an absolute probability — the similarity bar is the primary
//     guard, not the confidence value.
//
// Only equal-token-count windows are replaced, so word timestamps and IDs stay aligned.
package entityrepair

import "strings"

// Options tunes how aggressively the repair fires. The zero value is intentionally inert
// (MinSimilarity 0 would match everything) — always construct via DefaultOptions and
// adjust, so a forgotten field can't silently rewrite the whole transcript.
type Options struct {
	// MinSimilarity is the normalized edit similarity (0..1) a window must reach to be
	// snapped to a term. Exact matches are excluded separately, so this governs only
	// near-misses. Higher = more conservative.
	MinSimilarity float64
	// MinTermLen is the minimum number of normalized characters a glossary term must have
	// to be eligible — short terms produce too many coincidental near-matches.
	MinTermLen int
	// LowConfidence, when non-nil, must return true for a word before it may be repaired.
	// Pass nil to gate on similarity alone (provider-agnostic default).
	LowConfidence func(confidence *float64) bool
}

// DefaultOptions is the conservative production default: an 0.80 similarity bar on
// distinctive (≥4-char) terms, gating on similarity alone (no confidence requirement).
func DefaultOptions() Options {
	return Options{MinSimilarity: 0.80, MinTermLen: 4, LowConfidence: nil}
}

// Word is the minimal per-word input: the recognized text and optional confidence.
type Word struct {
	Text       string
	Confidence *float64
}

// Repair records one applied correction, for provenance/logging.
type Repair struct {
	Index      int     // index of the first replaced word
	Length     int     // number of words replaced
	From       string  // original window text (space-joined)
	To         string  // canonical glossary term written
	Similarity float64 // normalized similarity of the match
}

// Apply returns the corrected word texts (same length and order as in) and the repairs
// made. terms is the meeting glossary; order matters only for tie-breaking (first term
// wins a window). A window already matching a term exactly, or matching none above the
// bar, is left untouched.
func Apply(in []Word, terms []string, opts Options) (out []string, repairs []Repair) {
	out = make([]string, len(in))
	for i, w := range in {
		out[i] = w.Text
	}
	if len(in) == 0 || len(terms) == 0 || opts.MinSimilarity <= 0 {
		return out, nil
	}

	prepared := prepareTerms(terms, opts.MinTermLen)
	if len(prepared) == 0 {
		return out, nil
	}

	i := 0
	for i < len(in) {
		best, bestSim, bestLen := -1, 0.0, 0
		for ti, t := range prepared {
			n := len(t.tokens)
			if i+n > len(in) {
				continue
			}
			if opts.LowConfidence != nil && !windowUncertain(in[i:i+n], opts.LowConfidence) {
				continue
			}
			window := wordTexts(in[i : i+n])
			sim, exact := windowSimilarity(window, t.tokens)
			if exact || sim < opts.MinSimilarity {
				continue
			}
			if sim > bestSim {
				best, bestSim, bestLen = ti, sim, n
			}
		}
		if best < 0 {
			i++
			continue
		}
		t := prepared[best]
		from := strings.Join(wordTexts(in[i:i+bestLen]), " ")
		for k, tok := range t.display {
			out[i+k] = tok
		}
		repairs = append(repairs, Repair{Index: i, Length: bestLen, From: from, To: t.canonical, Similarity: round2(bestSim)})
		i += bestLen
	}
	return out, repairs
}

type preparedTerm struct {
	canonical string   // original glossary string (for provenance)
	display   []string // tokens to write into the transcript
	tokens    []string // normalized tokens for comparison
}

func prepareTerms(terms []string, minLen int) []preparedTerm {
	var out []preparedTerm
	for _, raw := range terms {
		display := strings.Fields(raw)
		if len(display) == 0 {
			continue
		}
		var toks []string
		total := 0
		for _, d := range display {
			n := normalize(d)
			if n == "" {
				toks = nil
				break
			}
			toks = append(toks, n)
			total += len(n)
		}
		if len(toks) != len(display) || total < minLen {
			continue
		}
		out = append(out, preparedTerm{canonical: strings.TrimSpace(raw), display: display, tokens: toks})
	}
	return out
}

func wordTexts(ws []Word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Text
	}
	return out
}

func windowUncertain(ws []Word, low func(*float64) bool) bool {
	for _, w := range ws {
		if low(w.Confidence) {
			return true
		}
	}
	return false
}

// windowSimilarity is the min per-token normalized similarity between a window and a
// term's tokens (already equal length). exact reports whether every token matches
// exactly after normalization (so an already-correct phrase is skipped).
func windowSimilarity(window, termTokens []string) (sim float64, exact bool) {
	sim = 1.0
	exact = true
	for i, tok := range termTokens {
		a, b := normalize(window[i]), tok
		if a != b {
			exact = false
		}
		s := similarity(a, b)
		if s < sim {
			sim = s
		}
	}
	return sim, exact
}

// similarity is 1 - levenshtein/maxlen on two normalized strings (1.0 = identical).
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}
	d := levenshtein(a, b)
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	return 1.0 - float64(d)/float64(max)
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// normalize lowercases and keeps only letters/digits, so comparison ignores case and
// punctuation ("O'Brien," vs "obrien").
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

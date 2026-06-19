package metrics

import (
	"strings"
	"unicode"
)

// Normalize lowercases s and splits it into alphanumeric tokens, treating every
// run of non-alphanumeric characters as a token boundary. It mirrors
// normalizeForMatch in internal/core/artifacts so WER tokenization tolerates the
// same punctuation, capitalization, and whitespace differences the rest of noto
// does: Normalize("Ship v1!") == []string{"ship", "v1"}.
//
// An empty or all-punctuation string yields a nil slice (zero tokens).
func Normalize(s string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

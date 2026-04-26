package search

import (
	"strings"
	"unicode"
)

func NormalizeText(text string) string {
	text = strings.ToLower(text)
	text = strings.TrimSpace(text)
	text = collapseWhitespace(text)
	return text
}

func collapseWhitespace(text string) string {
	var result []rune
	var lastSpace bool
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !lastSpace && len(result) > 0 {
				result = append(result, ' ')
				lastSpace = true
			}
		} else {
			result = append(result, r)
			lastSpace = false
		}
	}
	if len(result) > 0 && result[len(result)-1] == ' ' {
		result = result[:len(result)-1]
	}
	return string(result)
}

func TruncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	truncated := text[:maxLen]
	if idx := strings.LastIndex(truncated, " "); idx > 0 {
		truncated = truncated[:idx]
	}
	return truncated + "..."
}

func ExtractKeywords(text string) []string {
	text = NormalizeText(text)
	words := strings.Fields(text)

	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"is": true, "are": true, "was": true, "were": true, "be": true,
		"been": true, "being": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true, "must": true,
		"it": true, "its": true, "this": true, "that": true, "these": true,
		"those": true, "i": true, "you": true, "he": true, "she": true,
		"we": true, "they": true, "what": true, "which": true, "who": true,
		"when": true, "where": true, "why": true, "how": true, "all": true,
		"each": true, "every": true, "both": true, "few": true, "more": true,
		"most": true, "other": true, "some": true, "such": true, "no": true,
		"nor": true, "not": true, "only": true, "own": true, "same": true,
		"so": true, "than": true, "too": true, "very": true, "just": true,
	}

	var keywords []string
	for _, word := range words {
		if len(word) > 2 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}
	return keywords
}

func HighlightMatches(text, query string) string {
	keywords := ExtractKeywords(query)
	if len(keywords) == 0 {
		return text
	}

	result := text
	for _, keyword := range keywords {
		result = strings.Replace(result, keyword, "**"+keyword+"**", -1)
	}
	return result
}

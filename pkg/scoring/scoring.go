// Package scoring provides reusable text-scoring primitives used by
// search, indexing, and tool packages across RagCode.
package scoring

import "strings"

// FilterTokens removes very short tokens (≤2 chars) and trims whitespace.
func FilterTokens(tokens []string) []string {
	filtered := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if len(tok) > 2 {
			filtered = append(filtered, tok)
		}
	}
	return filtered
}

// LexicalMatchScore counts total token occurrences in content (frequency-weighted).
// Returns the raw count sum — higher means more matches.
func LexicalMatchScore(content string, tokens []string) float64 {
	score := 0.0
	for _, token := range tokens {
		score += float64(strings.Count(content, token))
	}
	return score
}

// TokenMatchRatio returns the fraction of tokens found in text [0, 1].
func TokenMatchRatio(text string, tokens []string) float64 {
	if len(tokens) == 0 {
		return 0
	}
	matched := 0
	for _, tok := range tokens {
		if strings.Contains(text, tok) {
			matched++
		}
	}
	return float64(matched) / float64(len(tokens))
}

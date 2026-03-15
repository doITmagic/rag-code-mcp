package scoring

import "strings"

// ─── Match Reason Annotation ─────────────────────────────────────────────────

// MatchReasons describes which payload fields of a search result matched the query.
// Useful for AI agents to understand WHY a result was returned and decide
// whether to request full content or treat the result as high/low confidence.
type MatchReasons struct {
	SymbolName bool `json:"symbol_name"` // query token found in symbol name
	Signature  bool `json:"signature"`   // query token found in function signature
	Content    bool `json:"content"`     // query token found in code body
	Docstring  bool `json:"docstring"`   // query token found in docstring/comments
}

// DetectMatchReasons returns which fields of a search result contain the query tokens.
// It uses simple case-insensitive substring matching — the same heuristic used
// by the fallback lexical scorer, intentionally kept fast and allocation-light.
//
// query is the original search query (will be lowercased internally).
// name, signature, content, docstring are the corresponding payload fields.
func DetectMatchReasons(query, name, signature, content, docstring string) MatchReasons {
	lower := strings.ToLower(query)
	tokens := FilterTokens(strings.Fields(lower))
	if len(tokens) == 0 {
		return MatchReasons{}
	}

	containsAny := func(text string) bool {
		t := strings.ToLower(text)
		for _, tok := range tokens {
			if strings.Contains(t, tok) {
				return true
			}
		}
		return false
	}

	return MatchReasons{
		SymbolName: containsAny(name),
		Signature:  containsAny(signature),
		Content:    containsAny(content),
		Docstring:  containsAny(docstring),
	}
}

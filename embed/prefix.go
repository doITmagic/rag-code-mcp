package embed

import (
	"path"
	"strings"
)

// qwen3Instruct is Qwen3-Embedding's query instruction; documents get none.
const qwen3Instruct = "Instruct: Given a description of what a piece of code does, retrieve the code that implements it\nQuery: "

// QueryText formats a search query the way model's authors recommend.
// Measured on 2,054 Go queries: +2 to +3 points top-5 for both models below.
func QueryText(model, q string) string {
	switch family(model) {
	case "embeddinggemma":
		return "task: code retrieval | query: " + q
	case "qwen3-embedding":
		return qwen3Instruct + q
	}
	return q
}

// DocText formats an indexed text for model. Changing it for a model already
// in use needs a reindex, since stored vectors were made without it.
func DocText(model, d string) string {
	if family(model) == "embeddinggemma" {
		return "title: none | text: " + d
	}
	return d
}

// family maps "hf.co/google/EmbeddingGemma-2:latest" to "embeddinggemma".
func family(model string) string {
	m := strings.ToLower(path.Base(model))
	m, _, _ = strings.Cut(m, ":")
	for _, f := range []string{"embeddinggemma", "qwen3-embedding"} {
		if strings.HasPrefix(m, f) {
			return f
		}
	}
	return ""
}

package tools

import (
	"sort"

	"github.com/doITmagic/rag-code-mcp/pkg/scoring"
)

// applyPathScoping adjusts scores based on path proximity and re-sorts.
// This is a thin adapter between pkg/scoring (pure path logic) and
// the tools-specific mergedResult type.
func applyPathScoping(merged []mergedResult, scopePath string) []mergedResult {
	if scopePath == "" || len(merged) == 0 {
		return merged
	}

	for i := range merged {
		multiplier := scoring.PathProximity(merged[i].filePath, scopePath)
		merged[i].score = float32(float64(merged[i].score) * multiplier)
	}

	sort.Slice(merged, func(i, j int) bool {
		return merged[i].score > merged[j].score
	})

	return merged
}

package tools

import (
	"path/filepath"
	"sort"
	"strings"
)

// ─── Path-Scoped Search (Proposal #1) ────────────────────────────────────────
//
// When the agent provides a file_path, results from the same directory subtree
// get a score boost, while results from distant subtrees get a penalty.
// This keeps the search focused on the area the agent is working in.

const (
	// pathBoostSameDir boosts results in the exact same directory.
	pathBoostSameDir = 1.15

	// pathBoostSameSubtree boosts results under the same parent subtree.
	pathBoostSameSubtree = 1.05

	// pathPenaltyDistant penalizes results from unrelated subtrees.
	pathPenaltyDistant = 0.80
)

// scopeDir extracts the reference directory from a file_path.
// If the path points to a file, returns its parent directory.
// Returns empty string if path is empty.
func scopeDir(filePath string) string {
	if filePath == "" {
		return ""
	}
	clean := filepath.Clean(filePath)
	// If it looks like a file (has extension), use parent
	if filepath.Ext(clean) != "" {
		return filepath.Dir(clean)
	}
	return clean
}

// pathProximity computes a score multiplier based on how close a result's
// file_path is to the scope directory.
//
// Returns:
//   - pathBoostSameDir     (1.15) — result is in the exact same directory
//   - pathBoostSameSubtree (1.05) — result is under the same subtree
//   - 1.0                        — result is in an adjacent subtree (neutral)
//   - pathPenaltyDistant   (0.80) — result is from an unrelated subtree
func pathProximity(resultPath, scopePath string) float64 {
	if scopePath == "" || resultPath == "" {
		return 1.0
	}

	resultDir := filepath.Dir(filepath.Clean(resultPath))
	scopeClean := filepath.Clean(scopePath)

	// Exact same directory
	if resultDir == scopeClean {
		return pathBoostSameDir
	}

	// Result is under the scope subtree (scope is parent)
	scopePrefix := scopeClean + string(filepath.Separator)
	if strings.HasPrefix(resultDir, scopePrefix) {
		return pathBoostSameSubtree
	}

	// Scope is under the result's subtree (result is parent)
	resultPrefix := resultDir + string(filepath.Separator)
	if strings.HasPrefix(scopeClean, resultPrefix) {
		return pathBoostSameSubtree
	}

	// Share a common ancestor — check depth of divergence
	common := longestCommonPath(resultDir, scopeClean)
	if common == "" {
		return pathPenaltyDistant
	}

	// Count how many levels each is from the common ancestor
	resultRel := strings.TrimPrefix(resultDir, common)
	scopeRel := strings.TrimPrefix(scopeClean, common)
	resultDepth := countSeparators(resultRel)
	scopeDepth := countSeparators(scopeRel)

	// Close siblings (both 1-2 levels from common) — neutral
	if resultDepth <= 2 && scopeDepth <= 2 {
		return 1.0
	}

	// Far apart
	return pathPenaltyDistant
}

// applyPathScoping adjusts scores based on path proximity and re-sorts.
// Returns the same slice, modified in place.
func applyPathScoping(merged []mergedResult, scopePath string) []mergedResult {
	if scopePath == "" || len(merged) == 0 {
		return merged
	}

	for i := range merged {
		multiplier := pathProximity(merged[i].filePath, scopePath)
		merged[i].score = float32(float64(merged[i].score) * multiplier)
	}

	// Re-sort after score adjustment
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].score > merged[j].score
	})

	return merged
}

// longestCommonPath returns the longest shared directory prefix of two paths.
func longestCommonPath(a, b string) string {
	aParts := strings.Split(filepath.Clean(a), string(filepath.Separator))
	bParts := strings.Split(filepath.Clean(b), string(filepath.Separator))

	n := len(aParts)
	if len(bParts) < n {
		n = len(bParts)
	}

	var common []string
	for i := 0; i < n; i++ {
		if aParts[i] != bParts[i] {
			break
		}
		common = append(common, aParts[i])
	}

	if len(common) == 0 {
		return ""
	}
	return strings.Join(common, string(filepath.Separator))
}

// countSeparators returns the number of path separators in a string.
func countSeparators(s string) int {
	return strings.Count(s, string(filepath.Separator))
}

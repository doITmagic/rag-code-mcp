package scoring

import (
	"path/filepath"
	"strings"
)

// ─── Path Proximity Scoring ──────────────────────────────────────────────────
//
// Computes how "close" a file path is to a reference scope directory,
// returning a multiplier to boost/penalize search result scores.

const (
	// BoostSameDir boosts results in the exact same directory.
	BoostSameDir = 1.15

	// BoostSameSubtree boosts results under the same parent subtree.
	BoostSameSubtree = 1.05

	// PenaltyDistant penalizes results from unrelated subtrees.
	PenaltyDistant = 0.80
)

// ScopeDir extracts the reference directory from a file path.
// If the path points to a file (has extension), returns its parent directory.
// Returns empty string if path is empty.
func ScopeDir(filePath string) string {
	if filePath == "" {
		return ""
	}
	clean := filepath.Clean(filePath)
	if filepath.Ext(clean) != "" {
		return filepath.Dir(clean)
	}
	return clean
}

// PathProximity computes a score multiplier based on how close a result's
// file path is to the scope directory.
//
// Returns:
//   - BoostSameDir     (1.15) — result is in the exact same directory
//   - BoostSameSubtree (1.05) — result is under the same subtree
//   - 1.0                     — result is in an adjacent subtree (neutral)
//   - PenaltyDistant   (0.80) — result is from an unrelated subtree
func PathProximity(resultPath, scopePath string) float64 {
	if scopePath == "" || resultPath == "" {
		return 1.0
	}

	resultDir := filepath.Dir(filepath.Clean(resultPath))
	scopeClean := filepath.Clean(scopePath)

	// Exact same directory
	if resultDir == scopeClean {
		return BoostSameDir
	}

	// Result is under the scope subtree (scope is parent)
	scopePrefix := scopeClean + string(filepath.Separator)
	if strings.HasPrefix(resultDir, scopePrefix) {
		return BoostSameSubtree
	}

	// Scope is under the result's subtree (result is parent)
	resultPrefix := resultDir + string(filepath.Separator)
	if strings.HasPrefix(scopeClean, resultPrefix) {
		return BoostSameSubtree
	}

	// Share a common ancestor — check depth of divergence
	common := LongestCommonPath(resultDir, scopeClean)
	if common == "" {
		return PenaltyDistant
	}

	resultRel := strings.TrimPrefix(resultDir, common)
	scopeRel := strings.TrimPrefix(scopeClean, common)
	resultDepth := countSeparators(resultRel)
	scopeDepth := countSeparators(scopeRel)

	// Close siblings (both 1-2 levels from common) — neutral
	if resultDepth <= 2 && scopeDepth <= 2 {
		return 1.0
	}

	return PenaltyDistant
}

// LongestCommonPath returns the longest shared directory prefix of two paths.
func LongestCommonPath(a, b string) string {
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

package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/scoring"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// ─── Fallback Direct Search ──────────────────────────────────────────────────
//
// When no Qdrant collections exist (or vector search returns zero results),
// this module provides a direct AST-based search across the workspace filesystem.
//
// How it works:
//   1. WalkDir the workspace (same exclusion rules as indexer)
//   2. Parse each file with the registered parser (same parsers used for indexing)
//   3. Score each symbol against the query using lexical fuzzy matching
//   4. Return results as storage.SearchResult — fully compatible with SmartSearch
//
// No embeddings. No Qdrant. No Ollama. Pure filesystem + AST + lexical scoring.
// Results are tagged with _source: "fallback" in the payload so the caller can
// distinguish them from vector-based results.

const (
	// fallbackMaxFiles caps the number of files parsed during fallback to keep
	// response times bounded (~2-3s for typical projects).
	fallbackMaxFiles = 200

	// fallbackTimeout is the hard time limit for the entire fallback scan.
	fallbackTimeout = 5 * time.Second

	// fallbackMinScore discards symbols with very low relevance.
	fallbackMinScore = 0.05
)

// FallbackDirectSearch walks the workspace, parses files with the AST parsers,
// and scores symbols against the query using lexical fuzzy matching.
// Returns results compatible with storage.SearchResult, sorted by score descending.
//
// This is a zero-infrastructure fallback: no Qdrant, no embeddings, no Ollama.
// The caller should prefer vector results when available.
func (e *Engine) FallbackDirectSearch(ctx context.Context, workspaceRoot, query string, limit int) []storage.SearchResult {
	ctx, cancel := context.WithTimeout(ctx, fallbackTimeout)
	defer cancel()

	t0 := time.Now()

	// Tokenize query for scoring
	lowerQuery := strings.ToLower(query)
	tokens := scoring.FilterTokens(strings.Fields(lowerQuery))
	if len(tokens) == 0 {
		return nil
	}

	// Collect exclude patterns from config
	var excludePatterns []string
	if e.config != nil {
		excludePatterns = e.config.Workspace.ExcludePatterns
	}

	// Walk + parse + score
	type scored struct {
		result storage.SearchResult
		score  float64
	}
	var candidates []scored
	var filesScanned int

	_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		// Respect context cancellation (timeout)
		select {
		case <-ctx.Done():
			return filepath.SkipAll
		default:
		}

		if err != nil {
			return nil
		}

		// Directory exclusion — same rules as indexer
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			for _, p := range excludePatterns {
				if name == p {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Cap file count
		if filesScanned >= fallbackMaxFiles {
			return filepath.SkipAll
		}

		// Only parse files with a registered parser
		analyzer := parser.GetByFile(path)
		if analyzer == nil {
			return nil
		}

		filesScanned++

		// Parse the file — extract symbols via AST
		result, parseErr := analyzer.Analyze(ctx, path)
		if parseErr != nil || result == nil {
			return nil
		}

		// Score each symbol against the query
		for _, sym := range result.Symbols {
			score := fallbackScoreSymbol(sym, lowerQuery, tokens)
			if score < fallbackMinScore {
				continue
			}

			candidates = append(candidates, scored{
				result: symbolToSearchResult(sym, float32(score)),
				score:  score,
			})
		}

		return nil
	})

	if len(candidates) == 0 {
		logger.Instance.Debug("[FALLBACK] ws=%s query=%q — 0 results from %d files in %v",
			filepath.Base(workspaceRoot), query, filesScanned, time.Since(t0))
		return nil
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Cap to limit
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}

	results := make([]storage.SearchResult, len(candidates))
	for i, c := range candidates {
		results[i] = c.result
	}

	logger.Instance.Info("[FALLBACK] ws=%s query=%q → %d results from %d files in %v",
		filepath.Base(workspaceRoot), query, len(results), filesScanned, time.Since(t0))

	return results
}

// ─── Scoring ─────────────────────────────────────────────────────────────────

// fallbackScoreSymbol computes a relevance score [0, 1] for a symbol against the query.
// It combines multiple signals with weights:
//   - Name match (exact, prefix, contains)   — 40%
//   - Signature match                        — 20%
//   - Content/body lexical match             — 25%
//   - Docstring match                        — 15%
func fallbackScoreSymbol(sym parser.Symbol, lowerQuery string, tokens []string) float64 {
	lowerName := strings.ToLower(sym.Name)
	lowerSig := strings.ToLower(sym.Signature)
	lowerContent := strings.ToLower(sym.Content)
	lowerDoc := strings.ToLower(sym.Docstring)

	// 1. Name scoring — highest weight, structured matching
	nameScore := 0.0
	if lowerName == lowerQuery {
		nameScore = 1.0 // exact match
	} else if strings.HasPrefix(lowerName, lowerQuery) {
		nameScore = 0.8
	} else if strings.Contains(lowerName, lowerQuery) {
		nameScore = 0.6
	} else {
		// Token-based: how many query tokens appear in the name
		nameScore = scoring.TokenMatchRatio(lowerName, tokens) * 0.5
	}

	// 2. Signature scoring
	sigScore := scoring.TokenMatchRatio(lowerSig, tokens) * 0.8
	// Bonus: full query substring match in signature
	if strings.Contains(lowerSig, lowerQuery) {
		sigScore = math.Max(sigScore, 0.9)
	}

	// 3. Content/body lexical scoring
	contentScore := 0.0
	if len(lowerContent) > 0 {
		rawScore := scoring.LexicalMatchScore(lowerContent, tokens)
		// Normalize: log-scale to handle long files vs short functions
		contentScore = math.Min(rawScore/math.Max(1, float64(len(tokens))*2), 1.0)
	}

	// 4. Docstring scoring
	docScore := 0.0
	if len(lowerDoc) > 0 {
		docScore = scoring.TokenMatchRatio(lowerDoc, tokens) * 0.9
		if strings.Contains(lowerDoc, lowerQuery) {
			docScore = math.Max(docScore, 0.95)
		}
	}

	// Weighted combination
	combined := nameScore*0.40 + sigScore*0.20 + contentScore*0.25 + docScore*0.15

	return combined
}

// fallbackTokenMatchRatio, fallbackLexicalScore, fallbackFilterTokens
// are now consolidated in pkg/scoring.

// ─── Conversion ──────────────────────────────────────────────────────────────

// symbolToSearchResult converts a parser.Symbol into a storage.SearchResult
// with the same payload structure that the indexer would produce.
// Tagged with _source: "fallback" so callers can identify non-vector results.
func symbolToSearchResult(sym parser.Symbol, score float32) storage.SearchResult {
	idKey := fmt.Sprintf("%s:%s:%d:%d", sym.FilePath, sym.Name, sym.StartLine, sym.EndLine)
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(idKey)))[:32]

	payload := map[string]interface{}{
		"name":       sym.Name,
		"type":       string(sym.Type),
		"package":    sym.Package,
		"content":    sym.Content,
		"signature":  sym.Signature,
		"docstring":  sym.Docstring,
		"file_path":  sym.FilePath,
		"start_line": sym.StartLine,
		"end_line":   sym.EndLine,
		"language":   sym.Language,
		"is_public":  sym.IsPublic,
		"_source":    "fallback",
	}

	return storage.SearchResult{
		Point: storage.Point{
			ID:      id,
			Payload: payload,
		},
		Score: score,
	}
}

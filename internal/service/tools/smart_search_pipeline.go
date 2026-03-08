package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/scoring"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
)

// ─── Input Normalization ─────────────────────────────────────────────────────

// normalizeInput validates the search input and applies defaults.
func normalizeInput(input SmartSearchInput, defaultLimit int) (string, int, SmartSearchInput, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", 0, input, fmt.Errorf("query parameter is required")
	}
	limit := defaultLimit
	if input.Limit > 0 {
		limit = input.Limit
	}
	if input.Mode == "strict_docs" {
		input.IncludeDocs = true
	}
	// Clamp min_score to valid range [0.0, 1.0]
	if input.MinScore < 0 {
		input.MinScore = 0
	} else if input.MinScore > 1.0 {
		input.MinScore = 1.0
	}
	return query, limit, input, nil
}

// ─── Parallel Search ─────────────────────────────────────────────────────────

// searchMetadata holds workspace context extracted from search results.
type searchMetadata struct {
	workspaceRoot   string
	workspaceID     string
	collection      string
	language        string
	detectionSource string
	mismatchRisk    string
}

// parallelSearchResult holds the output of runParallelSearch.
type parallelSearchResult struct {
	semantic *engine.SearchCodeResult
	hybrid   *engine.SearchCodeResult
	meta     searchMetadata
	err      error
}

// runParallelSearch executes semantic and hybrid searches concurrently,
// collects results, and extracts workspace metadata.
func (t *SmartSearchTool) runParallelSearch(ctx context.Context, filePath, query string, limit int, includeDocs bool) parallelSearchResult {
	type searchResult struct {
		label   string
		result  *engine.SearchCodeResult
		err     error
		elapsed time.Duration
	}

	results := make(chan searchResult, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		res, err := t.engine.SearchCode(ctx, filePath, query, limit, includeDocs)
		results <- searchResult{label: "semantic", result: res, err: err, elapsed: time.Since(t0)}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		res, err := t.engine.HybridSearchCode(ctx, filePath, query, limit)
		results <- searchResult{label: "hybrid", result: res, err: err, elapsed: time.Since(t0)}
	}()

	go func() { wg.Wait(); close(results) }()

	var psr parallelSearchResult
	for sr := range results {
		logger.Instance.Debug("rag_search %s: elapsed=%v err=%v results=%d",
			sr.label, sr.elapsed, sr.err,
			func() int {
				if sr.result != nil {
					return len(sr.result.Results)
				}
				return 0
			}())

		if sr.err != nil {
			if psr.err == nil {
				psr.err = sr.err
			}
			continue
		}

		if psr.meta.workspaceRoot == "" && sr.result != nil {
			psr.meta = searchMetadata{
				workspaceRoot:   sr.result.WorkspaceRoot,
				workspaceID:     sr.result.WorkspaceID,
				collection:      sr.result.Collection,
				language:        sr.result.Language,
				detectionSource: sr.result.DetectionSource,
				mismatchRisk:    sr.result.MismatchRisk,
			}
		}

		switch sr.label {
		case "semantic":
			psr.semantic = sr.result
		case "hybrid":
			psr.hybrid = sr.result
		}
	}
	return psr
}

// ─── Filtering Pipeline ──────────────────────────────────────────────────────

// filterConfig holds parameters for the post-processing pipeline.
type filterConfig struct {
	Mode     string
	MinScore float32
	FilePath string
}

// applyFilters runs the full post-processing pipeline on merged results:
// mode filtering → path scoping → score threshold → doc grouping.
func (t *SmartSearchTool) applyFilters(merged []mergedResult, cfg filterConfig) []mergedResult {
	merged = applyModeFilter(merged, cfg.Mode)
	merged = applyPathScoping(merged, scoring.ScopeDir(cfg.FilePath))
	merged = applyScoreFilter(merged, cfg.MinScore)
	merged = t.groupDocsByTree(merged)
	return merged
}

// applyModeFilter removes results based on strict_code / strict_docs mode.
func applyModeFilter(merged []mergedResult, mode string) []mergedResult {
	if mode != "strict_code" && mode != "strict_docs" {
		return merged
	}
	var filtered []mergedResult
	for _, m := range merged {
		isDoc := isDocSymbolType(m.symbolType) || isDocExtension(m.filePath)
		if mode == "strict_code" && isDoc {
			continue
		}
		if mode == "strict_docs" && !isDoc {
			continue
		}
		filtered = append(filtered, m)
	}
	return filtered
}

// applyScoreFilter removes results below the effective minimum score.
// If minScore is specified, use it directly. Otherwise, apply auto-threshold:
// when top score > 0.70, prune results below 40% of top score.
func applyScoreFilter(merged []mergedResult, minScore float32) []mergedResult {
	if len(merged) == 0 {
		return merged
	}

	effective := minScore
	if effective <= 0 && merged[0].score > autoScoreThresholdTrigger {
		effective = merged[0].score * autoScoreThresholdRatio
	}
	if effective <= 0 {
		return merged
	}

	var filtered []mergedResult
	for _, m := range merged {
		if m.score >= effective {
			filtered = append(filtered, m)
		}
	}
	nDropped := len(merged) - len(filtered)
	if nDropped > 0 {
		logger.Instance.Debug("rag_search: filtered %d results below min_score=%.2f (effective)", nDropped, effective)
	}
	return filtered
}

// ─── Response Building ───────────────────────────────────────────────────────

// buildResponseMeta constructs the ToolResponse shell with metadata, warnings,
// and messaging based on whether results are from fallback or vector search.
func (t *SmartSearchTool) buildResponseMeta(meta searchMetadata) ToolResponse {
	isFallback := meta.collection == "fallback"

	idxProgress := BuildIndexingProgress(t.engine, meta.workspaceID, meta.workspaceRoot)
	if isFallback && idxProgress == nil {
		idxProgress = &IndexingProgressSummary{
			State:   "starting",
			Elapsed: "0s",
		}
	}

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			WorkspaceRoot:    meta.workspaceRoot,
			DetectionSource:  meta.detectionSource,
			Language:         meta.language,
			Collection:       meta.collection,
			IndexingProgress: idxProgress,
			SessionMetrics:   telemetry.ReadAggregatedMetrics(meta.workspaceRoot),
		},
	}

	if meta.mismatchRisk != "" && meta.mismatchRisk != "low" {
		response.Warning = fmt.Sprintf("Branch mismatch risk: %s — results may be from a different branch.", meta.mismatchRisk)
	}

	if meta.detectionSource == "nested_workspace_override" {
		overrideNote := fmt.Sprintf(
			"ℹ️ Nested workspace detected: your file_path resolved to a subdirectory of an already-registered workspace. "+
				"Using parent workspace root '%s' instead. Results are from the parent project index.",
			meta.workspaceRoot,
		)
		if response.Warning != "" {
			response.Warning += " | " + overrideNote
		} else {
			response.Warning = overrideNote
		}
	}

	if isFallback {
		fallbackNote := buildFallbackNote(idxProgress)
		if response.Warning != "" {
			response.Warning += " | " + fallbackNote
		} else {
			response.Warning = fallbackNote
		}
	}

	return response
}

// buildFallbackNote constructs a dynamic fallback warning that includes
// live indexing progress data (per-language %, elapsed, ready languages).
func buildFallbackNote(progress *IndexingProgressSummary) string {
	var sb strings.Builder
	sb.WriteString("⚡ Fallback results (AST/lexical, not vector). ")

	if progress != nil && progress.Elapsed != "" && progress.Elapsed != "0s" {
		sb.WriteString(fmt.Sprintf("Indexing elapsed: %s. ", progress.Elapsed))
	}

	// Report per-language progress and collect fully-indexed langs
	var readyLangs []string
	if progress != nil && len(progress.Languages) > 0 {
		var langParts []string
		for lang, lp := range progress.Languages {
			if lp.TotalFiles == 0 {
				continue
			}
			entry := fmt.Sprintf("%s %d%%", lang, lp.Percent)
			if lp.Percent == 100 {
				entry += " ✓"
				readyLangs = append(readyLangs, lang)
			}
			langParts = append(langParts, entry)
		}
		if len(langParts) > 0 {
			sb.WriteString("Progress: ")
			sb.WriteString(strings.Join(langParts, " · "))
			sb.WriteString(". ")
		}
	}

	// Actionable hint: tell agent which langs can use vector search now
	if len(readyLangs) > 0 {
		sb.WriteString(fmt.Sprintf("Vector search ready for: %s — retry for higher-quality results on those files. ",
			strings.Join(readyLangs, ", ")))
	} else {
		sb.WriteString("Indexing in background — retry shortly for semantic vector results. ")
	}

	sb.WriteString("Current results may miss semantically related code.")
	return sb.String()
}

// ─── Result Serialization ────────────────────────────────────────────────────

// resultToMap converts a mergedResult to the output map format.
// includeContent controls whether the full source code is included.
// When includeReasons is true, a match_reasons field is added explaining
// which payload fields (symbol_name, signature, content, docstring) matched the query.
func resultToMap(m mergedResult, includeContent bool, query string, includeReasons bool) map[string]any {
	item := map[string]any{
		"score":      m.score,
		"file_path":  m.filePath,
		"name":       m.name,
		"type":       m.symbolType,
		"signature":  m.signature,
		"package":    m.pkg,
		"start_line": m.startLine,
		"end_line":   m.endLine,
	}
	if includeContent {
		item["content"] = m.content
	}
	if m.docstring != "" {
		item["docstring"] = m.docstring
	}
	if m.source != "" {
		item["_source"] = m.source
	}
	if includeReasons && query != "" {
		item["match_reasons"] = scoring.DetectMatchReasons(query, m.name, m.signature, m.content, m.docstring)
	}
	return item
}

// buildResultsMessage returns the appropriate message based on mode and fallback state.
func buildResultsMessage(count int, useCompact, isFallback bool) string {
	if useCompact {
		if isFallback {
			return fmt.Sprintf("⚡ Found %d results via AST fallback (compact view). Indexing in progress — results will improve. Use rag_read_file_context for full source.", count)
		}
		return fmt.Sprintf("📋 Found %d results (compact view). Use rag_read_file_context to get full source for specific results.", count)
	}
	if isFallback {
		return fmt.Sprintf("⚡ Found %d results via AST fallback with full source code. Indexing in progress — results will improve.", count)
	}
	return fmt.Sprintf("🎯 Found %d high-confidence results with full source code.", count)
}

// serializeResults populates the ToolResponse with either compact or full result data,
// calculates telemetry savings, and detects stale indexed files.
// Stale results (files that no longer exist on disk) are EXCLUDED from the response data.
// Returns the list of stale file paths for async cleanup by the caller.
// query and includeReasons control the optional match_reasons annotation per result.
func serializeResults(response *ToolResponse, merged []mergedResult, useCompact, isFallback bool, query string, includeReasons bool) []string {
	var validResults []map[string]any
	var baselineBytes, actualBytes int64
	seenFiles := make(map[string]bool)
	var staleFiles []string

	for _, m := range merged {
		// Stale file tracking: check if file still exists on disk
		if !seenFiles[m.filePath] {
			seenFiles[m.filePath] = true
			if _, err := os.Stat(m.filePath); os.IsNotExist(err) {
				staleFiles = append(staleFiles, m.filePath)
			}
		}

		// Skip results from stale files — don't include in response
		isStale := false
		for _, sf := range staleFiles {
			if m.filePath == sf {
				isStale = true
				break
			}
		}
		if isStale {
			continue
		}

		validResults = append(validResults, resultToMap(m, !useCompact, query, includeReasons))

		if !useCompact {
			actualBytes += int64(len(m.content))
		}

		// Telemetry: only count existing files
		if info, err := os.Stat(m.filePath); err == nil {
			baselineBytes += info.Size()
		}
	}

	response.Message = buildResultsMessage(len(validResults), useCompact, isFallback)
	response.Data = validResults

	// Proactive stale index warning (now with auto-cleanup note)
	if len(staleFiles) > 0 {
		staleWarning := fmt.Sprintf(
			"🧹 %d stale file(s) detected and filtered out (no longer on disk). Auto-cleanup triggered. Files: %s",
			len(staleFiles), strings.Join(staleFiles, ", "),
		)
		if response.Warning != "" {
			response.Warning += " | " + staleWarning
		} else {
			response.Warning = staleWarning
		}
	}

	response.Context.Telemetry = telemetry.CalculateSavings(baselineBytes, actualBytes)
	return staleFiles
}

// noResultsResponse returns a "no results" JSON response.
func noResultsResponse(query string, meta searchMetadata) (string, error) {
	response := ToolResponse{
		Status:  "no_results",
		Message: fmt.Sprintf("🔍 No code results found for query: '%s'", query),
		Context: ContextMetadata{
			WorkspaceRoot:   meta.workspaceRoot,
			DetectionSource: meta.detectionSource,
			Language:        meta.language,
			Collection:      meta.collection,
		},
	}
	return response.JSON()
}

// recordSearchMetric maps pipeline data to a telemetry.SearchMetric and appends to JSONL.
func recordSearchMetric(meta searchMetadata, query string, merged []mergedResult, isFallback bool, savings *telemetry.Savings, start time.Time) {
	source := "vector"
	if isFallback {
		source = "fallback"
	}

	var topScore float32
	if len(merged) > 0 {
		topScore = merged[0].score
	}

	var bytesSaved, tokensSaved int64
	if savings != nil {
		bytesSaved = savings.BytesAvoided
		tokensSaved = savings.TokensSaved
	}

	telemetry.AppendSearchMetric(meta.workspaceRoot, telemetry.SearchMetric{
		Tool:        "rag_search",
		Query:       query,
		ResultCount: len(merged),
		TopScore:    topScore,
		Source:      source,
		BytesSaved:  bytesSaved,
		TokensSaved: tokensSaved,
		ResponseMs:  time.Since(start).Milliseconds(),
	})
}

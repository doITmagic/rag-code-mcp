package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SmartSearchTool implements the rag_search MCP tool.
// It runs both semantic (discovery) and hybrid (exact) searches in parallel,
// merges results by score, and returns adaptive output (compact vs full)
// based on result confidence — no manual mode selection needed.
type SmartSearchTool struct {
	engine      *engine.Engine
	searchLimit int
}

// NewSmartSearchTool creates a new smart search tool.
func NewSmartSearchTool(eng *engine.Engine) *SmartSearchTool {
	return &SmartSearchTool{
		engine:      eng,
		searchLimit: 10,
	}
}

func (t *SmartSearchTool) Name() string { return "rag_search" }
func (t *SmartSearchTool) Description() string {
	return "Intelligent code search that automatically determines the best search strategy. " +
		"Simply provide your query — the tool runs both semantic and exact searches in parallel, " +
		"merges results by relevance score, and adapts the response format automatically: " +
		"high-confidence matches return full source code, exploratory results return compact summaries. " +
		"No need to choose a search mode. Provide 'file_path' for faster workspace detection, or omit it for Auto-Discovery."
}

type SmartSearchInput struct {
	Query    string `json:"query"`
	FilePath string `json:"file_path,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// highConfidenceThreshold: if top result score exceeds this, return full content.
const highConfidenceThreshold = 0.85

// compactResultCap: max results above which we always go compact.
const compactResultCap = 4

func (t *SmartSearchTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SmartSearchInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		logger.Instance.Highlight("rag_search: '%s' (context: %s)", input.Query, input.FilePath)

		result, err := t.Execute(ctx, input)
		if err != nil {
			logger.Instance.Error("rag_search failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		logger.Instance.Info("rag_search completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *SmartSearchTool) Execute(ctx context.Context, input SmartSearchInput) (string, error) {
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	limit := t.searchLimit
	if input.Limit > 0 {
		limit = input.Limit
	}

	// Run both search strategies in parallel
	type searchResult struct {
		label   string
		result  *engine.SearchCodeResult
		err     error
		elapsed time.Duration
	}

	results := make(chan searchResult, 2)
	var wg sync.WaitGroup

	// Goroutine 1: Semantic (discovery) search
	wg.Add(1)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		res, err := t.engine.SearchCode(ctx, input.FilePath, query, limit, false)
		results <- searchResult{label: "semantic", result: res, err: err, elapsed: time.Since(t0)}
	}()

	// Goroutine 2: Hybrid (exact) search
	wg.Add(1)
	go func() {
		defer wg.Done()
		t0 := time.Now()
		res, err := t.engine.HybridSearchCode(ctx, input.FilePath, query, limit)
		results <- searchResult{label: "hybrid", result: res, err: err, elapsed: time.Since(t0)}
	}()

	go func() { wg.Wait(); close(results) }()

	// Collect results from both strategies
	var semanticRes, hybridRes *engine.SearchCodeResult
	var firstErr error
	var workspaceRoot, workspaceID, collection, language, detectionSource, mismatchRisk string

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
			if firstErr == nil {
				firstErr = sr.err
			}
			continue
		}

		// Capture workspace metadata from the first successful result
		if workspaceRoot == "" && sr.result != nil {
			workspaceRoot = sr.result.WorkspaceRoot
			workspaceID = sr.result.WorkspaceID
			collection = sr.result.Collection
			language = sr.result.Language
			detectionSource = sr.result.DetectionSource
			mismatchRisk = sr.result.MismatchRisk
		}

		switch sr.label {
		case "semantic":
			semanticRes = sr.result
		case "hybrid":
			hybridRes = sr.result
		}
	}

	// Handle error cases (same as existing tool)
	if semanticRes == nil && hybridRes == nil {
		return t.handleSearchError(firstErr, workspaceRoot, workspaceID)
	}

	// Merge and deduplicate results from both strategies
	merged := t.mergeResults(semanticRes, hybridRes, limit)

	if len(merged) == 0 {
		response := ToolResponse{
			Status:  "no_results",
			Message: fmt.Sprintf("🔍 No code results found for query: '%s'", query),
			Context: ContextMetadata{
				WorkspaceRoot:   workspaceRoot,
				DetectionSource: detectionSource,
				Language:        language,
				Collection:      collection,
			},
		}
		return response.JSON()
	}

	// Determine response mode based on result confidence
	topScore := merged[0].score
	useCompact := len(merged) > compactResultCap || topScore < highConfidenceThreshold

	// Build response
	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			WorkspaceRoot:    workspaceRoot,
			DetectionSource:  detectionSource,
			Language:         language,
			Collection:       collection,
			IndexingProgress: BuildIndexingProgress(t.engine, workspaceID),
		},
	}

	if mismatchRisk != "" && mismatchRisk != "low" {
		response.Warning = fmt.Sprintf("Branch mismatch risk: %s — results may be from a different branch.", mismatchRisk)
	}

	// Calculate telemetry
	baselineBytes := 0
	actualBytes := 0
	seenFiles := make(map[string]bool)

	if useCompact {
		// COMPACT MODE: return only metadata, no source code
		response.Message = fmt.Sprintf("📋 Found %d results (compact view). Use rag_read_file_context to get full source for specific results.", len(merged))
		compactData := make([]map[string]any, 0, len(merged))
		for _, m := range merged {
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
			if m.docstring != "" {
				item["docstring"] = m.docstring
			}
			if m.source != "" {
				item["_source"] = m.source // "semantic", "hybrid", or "both"
			}
			compactData = append(compactData, item)

			// Telemetry: count full file sizes but 0 actual bytes sent
			if !seenFiles[m.filePath] {
				seenFiles[m.filePath] = true
				if info, err := os.Stat(m.filePath); err == nil {
					baselineBytes += int(info.Size())
				}
			}
		}
		response.Data = compactData
	} else {
		// FULL MODE: return complete content (high confidence, few results)
		response.Message = fmt.Sprintf("🎯 Found %d high-confidence results with full source code.", len(merged))
		fullData := make([]map[string]any, 0, len(merged))
		for _, m := range merged {
			item := map[string]any{
				"score":      m.score,
				"file_path":  m.filePath,
				"name":       m.name,
				"type":       m.symbolType,
				"signature":  m.signature,
				"package":    m.pkg,
				"start_line": m.startLine,
				"end_line":   m.endLine,
				"content":    m.content,
			}
			if m.docstring != "" {
				item["docstring"] = m.docstring
			}
			if m.source != "" {
				item["_source"] = m.source
			}
			fullData = append(fullData, item)

			actualBytes += len(m.content)
			if !seenFiles[m.filePath] {
				seenFiles[m.filePath] = true
				if info, err := os.Stat(m.filePath); err == nil {
					baselineBytes += int(info.Size())
				}
			}
		}
		response.Data = fullData
	}

	response.Context.Telemetry = telemetry.CalculateSavings(baselineBytes, actualBytes)
	return response.JSON()
}

// mergedResult holds a deduplicated result with metadata extracted from payload.
type mergedResult struct {
	id         string
	score      float32
	filePath   string
	name       string
	symbolType string
	signature  string
	pkg        string
	docstring  string
	content    string
	startLine  int
	endLine    int
	source     string // "semantic", "hybrid", "both"
}

// mergeResults combines semantic and hybrid search results, deduplicates by ID,
// and returns sorted by score descending, capped at limit.
func (t *SmartSearchTool) mergeResults(semantic, hybrid *engine.SearchCodeResult, limit int) []mergedResult {
	seen := make(map[string]*mergedResult)
	var all []*mergedResult

	addResults := func(results []storage.SearchResult, source string) {
		for _, r := range results {
			id := r.Point.ID
			if existing, ok := seen[id]; ok {
				// Already seen — mark as "both" and keep higher score
				existing.source = "both"
				if r.Score > existing.score {
					existing.score = r.Score
				}
				continue
			}

			m := &mergedResult{
				id:     id,
				score:  r.Score,
				source: source,
			}

			// Extract payload fields
			if v, ok := r.Point.Payload["file_path"].(string); ok {
				m.filePath = v
			}
			if v, ok := r.Point.Payload["name"].(string); ok {
				m.name = v
			}
			if v, ok := r.Point.Payload["type"].(string); ok {
				m.symbolType = v
			}
			if v, ok := r.Point.Payload["signature"].(string); ok {
				m.signature = v
			}
			if v, ok := r.Point.Payload["package"].(string); ok {
				m.pkg = v
			}
			if v, ok := r.Point.Payload["docstring"].(string); ok {
				m.docstring = v
			}
			if v, ok := r.Point.Payload["content"].(string); ok {
				m.content = v
			}
			if v, ok := r.Point.Payload["start_line"].(float64); ok {
				m.startLine = int(v)
			}
			if v, ok := r.Point.Payload["end_line"].(float64); ok {
				m.endLine = int(v)
			}

			seen[id] = m
			all = append(all, m)
		}
	}

	if semantic != nil {
		addResults(semantic.Results, "semantic")
	}
	if hybrid != nil {
		addResults(hybrid.Results, "hybrid")
	}

	// Sort by score descending
	sort.Slice(all, func(i, j int) bool {
		return all[i].score > all[j].score
	})

	// Cap to limit
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}

	// Convert to value slice
	result := make([]mergedResult, len(all))
	for i, m := range all {
		result[i] = *m
	}
	return result
}

// handleSearchError handles indexing/workspace errors consistently.
func (t *SmartSearchTool) handleSearchError(err error, workspaceRoot, workspaceID string) (string, error) {
	if err == nil {
		response := ToolResponse{
			Status:  "no_results",
			Message: "No results from either search strategy.",
		}
		return response.JSON()
	}

	response := ToolResponse{}

	if strings.Contains(err.Error(), "No workspace detected") {
		response.Status = "error"
		response.Error = err.Error()
		return response.JSON()
	}

	var indexingStarted *engine.ErrIndexingStarted
	var indexingInProgress *engine.ErrIndexingInProgress
	var notIndexed *engine.ErrNotIndexed

	if errors.As(err, &indexingStarted) {
		response.Status = "indexing_started"
		response.Message = fmt.Sprintf("🚀 Workspace '%s' was not indexed. Background indexing has been STARTED automatically. Please wait a few moments and try your search again.", indexingStarted.WorkspaceRoot)
		response.Context.WorkspaceRoot = indexingStarted.WorkspaceRoot
		if indexingStarted.WorkspaceID != "" {
			response.Context.IndexingProgress = BuildIndexingProgress(t.engine, indexingStarted.WorkspaceID)
		}
		return response.JSON()
	}

	if errors.As(err, &indexingInProgress) {
		response.Status = "indexing_in_progress"
		response.Message = fmt.Sprintf("⏳ Workspace '%s' is currently being indexed.", indexingInProgress.WorkspaceRoot)
		response.Context.WorkspaceRoot = indexingInProgress.WorkspaceRoot
		if indexingInProgress.WorkspaceID != "" {
			response.Context.IndexingProgress = BuildIndexingProgress(t.engine, indexingInProgress.WorkspaceID)
		}
		return response.JSON()
	}

	if errors.As(err, &notIndexed) {
		response.Status = "error"
		response.Error = fmt.Sprintf("❌ Workspace '%s' is not indexed yet.", notIndexed.WorkspaceRoot)
		response.Context.WorkspaceRoot = notIndexed.WorkspaceRoot
		return response.JSON()
	}

	response.Status = "error"
	response.Error = fmt.Sprintf("search failed: %v", err)
	return response.JSON()
}

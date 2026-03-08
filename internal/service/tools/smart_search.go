package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		"No need to choose a search mode. Provide 'file_path' for faster workspace detection, or omit it for Auto-Discovery. " +
		"Set 'include_full_content' to true to force full source code in all results, overriding compact mode. " +
		"Set 'include_docs' to true to also search project documentation (README, guides, Markdown files) alongside code. " +
		"Use 'mode'=\"strict_code\" when you ONLY want to see implementation logic exactly (Go, Python, etc) and strictly ignore documentation. " +
		"Use 'mode'=\"strict_docs\" when searching for architectural plans or summaries. " +
		"Use 'mode'=\"all\" or omit for broad scans."
}

type SmartSearchInput struct {
	Query              string `json:"query"`
	FilePath           string `json:"file_path,omitempty"`
	Limit              int    `json:"limit,omitempty"`
	IncludeFullContent bool   `json:"include_full_content,omitempty"`
	IncludeDocs        bool   `json:"include_docs,omitempty"`
	Mode               string `json:"mode,omitempty"`
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

	// When strict_docs mode is requested, automatically enable docs search
	// so that the semantic engine actually fetches documentation results.
	if input.Mode == "strict_docs" {
		input.IncludeDocs = true
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
		res, err := t.engine.SearchCode(ctx, input.FilePath, query, limit, input.IncludeDocs)
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

	// Apply mode filtering
	var filtered []mergedResult
	for _, m := range merged {
		isDoc := isDocSymbolType(m.symbolType) || isDocExtension(m.filePath)
		// Strict code mode: ignore completely any documentation type or doc file
		if input.Mode == "strict_code" && isDoc {
			continue
		}
		// Strict docs mode: ignore anything that isn't documentation
		if input.Mode == "strict_docs" && !isDoc {
			continue
		}
		filtered = append(filtered, m)
	}
	merged = filtered

	// Apply tree-based grouping for documentation chunks
	merged = t.groupDocsByTree(merged)

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

	// Override: when the agent explicitly requests full content, skip compact mode
	if input.IncludeFullContent {
		useCompact = false
	}

	// Detect if results came from fallback AST search
	isFallback := collection == "fallback"

	// Build response
	idxProgress := BuildIndexingProgress(t.engine, workspaceID, workspaceRoot)

	// If we're serving fallback results and indexing just started (no progress tracked yet),
	// synthesize a minimal progress indicator so the agent knows indexing is happening.
	if isFallback && idxProgress == nil {
		idxProgress = &IndexingProgressSummary{
			State:   "starting",
			Elapsed: "0s",
		}
	}

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			WorkspaceRoot:    workspaceRoot,
			DetectionSource:  detectionSource,
			Language:         language,
			Collection:       collection,
			IndexingProgress: idxProgress,
		},
	}

	if mismatchRisk != "" && mismatchRisk != "low" {
		response.Warning = fmt.Sprintf("Branch mismatch risk: %s — results may be from a different branch.", mismatchRisk)
	}

	// Explicit fallback notice for AI agents
	if isFallback {
		fallbackNote := "⚡ NOTE: These results are from a fast AST-based fallback search (no vector index available yet). " +
			"Indexing is running in the background — subsequent searches will use semantic vector matching for better accuracy. " +
			"Current results are based on lexical/structural matching and may miss semantically related code."
		if response.Warning != "" {
			response.Warning += " | " + fallbackNote
		} else {
			response.Warning = fallbackNote
		}
	}

	// Calculate telemetry
	baselineBytes := int64(0)
	actualBytes := int64(0)
	seenFiles := make(map[string]bool)
	var staleFiles []string // files referenced in index but no longer on disk

	if useCompact {
		// COMPACT MODE: return only metadata, no source code
		if isFallback {
			response.Message = fmt.Sprintf("⚡ Found %d results via AST fallback (compact view). Indexing in progress — results will improve. Use rag_read_file_context for full source.", len(merged))
		} else {
			response.Message = fmt.Sprintf("📋 Found %d results (compact view). Use rag_read_file_context to get full source for specific results.", len(merged))
		}
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
					baselineBytes += info.Size()
				} else if os.IsNotExist(err) {
					staleFiles = append(staleFiles, m.filePath)
				}
			}
		}
		response.Data = compactData
	} else {
		// FULL MODE: return complete content (high confidence, few results)
		if isFallback {
			response.Message = fmt.Sprintf("⚡ Found %d results via AST fallback with full source code. Indexing in progress — results will improve.", len(merged))
		} else {
			response.Message = fmt.Sprintf("🎯 Found %d high-confidence results with full source code.", len(merged))
		}
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

			actualBytes += int64(len(m.content))
			if !seenFiles[m.filePath] {
				seenFiles[m.filePath] = true
				if info, err := os.Stat(m.filePath); err == nil {
					baselineBytes += info.Size()
				} else if os.IsNotExist(err) {
					staleFiles = append(staleFiles, m.filePath)
				}
			}
		}
		response.Data = fullData
	}

	// Proactive stale index warning
	if len(staleFiles) > 0 {
		staleWarning := fmt.Sprintf(
			"⚠️ %d indexed file(s) no longer exist on disk (stale index). Consider re-indexing. Missing: %s",
			len(staleFiles), strings.Join(staleFiles, ", "),
		)
		if response.Warning != "" {
			response.Warning += " | " + staleWarning
		} else {
			response.Warning = staleWarning
		}
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
		response.Context.WorkspaceRoot = indexingStarted.WorkspaceRoot
		if indexingStarted.WorkspaceID != "" {
			response.Context.IndexingProgress = BuildIndexingProgress(t.engine, indexingStarted.WorkspaceID, indexingStarted.WorkspaceRoot)
		}
		response.Message = buildIndexingMessage("🚀", indexingStarted.WorkspaceRoot, response.Context.IndexingProgress)
		return response.JSON()
	}

	if errors.As(err, &indexingInProgress) {
		response.Status = "indexing_in_progress"
		response.Context.WorkspaceRoot = indexingInProgress.WorkspaceRoot
		if indexingInProgress.WorkspaceID != "" {
			response.Context.IndexingProgress = BuildIndexingProgress(t.engine, indexingInProgress.WorkspaceID, indexingInProgress.WorkspaceRoot)
		}
		response.Message = buildIndexingMessage("⏳", indexingInProgress.WorkspaceRoot, response.Context.IndexingProgress)
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

// isDocSymbolType returns true if the symbol type represents documentation content.
func isDocSymbolType(symbolType string) bool {
	return symbolType == "documentation" || symbolType == "code_block" || symbolType == "markdown"
}

// isDocExtension returns true if the file path has a documentation or structured text extension.
func isDocExtension(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".md", ".markdown", ".html", ".htm",
		".yaml", ".yml", ".json", ".xml",
		".toml", ".rst":
		return true
	}
	return false
}

// readLines reads a specific range of lines from a file using a buffered scanner
// to avoid loading the entire file into memory. Lines are 1-indexed.
func readLines(filePath string, startLine, endLine int) (string, error) {
	if startLine < 1 || endLine < startLine {
		return "", fmt.Errorf("invalid line range %d-%d", startLine, endLine)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var collected []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum > endLine {
			break
		}
		if lineNum >= startLine {
			collected = append(collected, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if len(collected) == 0 {
		return "", fmt.Errorf("invalid line range: file has %d lines, requested %d-%d", lineNum, startLine, endLine)
	}

	return strings.Join(collected, "\n"), nil
}

// groupDocsByTree aggregates "documentation" and "code_block" chunks
// from the same file and AST Signature (Markdown heading) into single unified blocks,
// fetching the continuous text from disk to prevent Frankenstein gaps.
func (t *SmartSearchTool) groupDocsByTree(results []mergedResult) []mergedResult {
	if len(results) == 0 {
		return results
	}

	var out []mergedResult

	type groupKey struct {
		filePath  string
		signature string
	}

	type docGroup struct {
		key      groupKey
		items    []*mergedResult
		maxScore float32
		minLine  int
		maxLine  int
		source   string
	}

	groupsMap := make(map[groupKey]*docGroup)
	var orderedGroups []groupKey // keep track of the first time we see a group to maintain rough sorting

	for i := range results {
		res := &results[i]

		// Only group documentation types and documentation/structured text files
		if !isDocSymbolType(res.symbolType) || !isDocExtension(res.filePath) || res.signature == "" {
			// Pass-through code or items without signature
			out = append(out, *res)
			continue
		}

		key := groupKey{filePath: res.filePath, signature: res.signature}
		if g, exists := groupsMap[key]; exists {
			g.items = append(g.items, res)
			if res.score > g.maxScore {
				g.maxScore = res.score
			}
			if res.startLine > 0 && (g.minLine == 0 || res.startLine < g.minLine) {
				g.minLine = res.startLine
			}
			if res.endLine > 0 && res.endLine > g.maxLine {
				g.maxLine = res.endLine
			}
			if g.source != "both" && g.source != res.source {
				g.source = "both"
			}
		} else {
			minL := res.startLine
			if minL == 0 {
				minL = 1
			}
			maxL := res.endLine
			if maxL == 0 {
				maxL = 1
			}
			groupsMap[key] = &docGroup{
				key:      key,
				items:    []*mergedResult{res},
				maxScore: res.score,
				minLine:  minL,
				maxLine:  maxL,
				source:   res.source,
			}
			orderedGroups = append(orderedGroups, key)
		}
	}

	// Reconstruct the grouped items
	for _, key := range orderedGroups {
		g := groupsMap[key]

		if len(g.items) == 1 {
			// Nothing to merge, just append
			out = append(out, *g.items[0])
			continue
		}

		// Multiple chunks in this group. Let's merge them!
		// Attempt to read the full continuous block from the file
		fullContent := ""
		if g.minLine > 0 && g.maxLine >= g.minLine {
			content, err := readLines(g.key.filePath, g.minLine, g.maxLine)
			if err == nil {
				fullContent = content
			}
		}

		// If reading from disk failed, append the contents manually with an ellipsis
		if fullContent == "" {
			var contents []string
			// Sort items by line number
			sortedItems := make([]*mergedResult, len(g.items))
			copy(sortedItems, g.items)
			sort.Slice(sortedItems, func(i, j int) bool {
				return sortedItems[i].startLine < sortedItems[j].startLine
			})
			for _, item := range sortedItems {
				contents = append(contents, strings.TrimSpace(item.content))
			}
			fullContent = strings.Join(contents, "\n\n[...]\n\n")
		}

		baseItem := g.items[0] // take the first item as a prototype
		merged := mergedResult{
			id:         fmt.Sprintf("merged_%s_%d_%d", baseItem.id, g.minLine, g.maxLine),
			score:      g.maxScore,
			filePath:   g.key.filePath,
			name:       baseItem.name,
			symbolType: "documentation_merged",
			signature:  g.key.signature,
			pkg:        baseItem.pkg,
			docstring:  fmt.Sprintf("Merged %d chunks spanning %d lines.", len(g.items), g.maxLine-g.minLine+1),
			content:    fullContent,
			startLine:  g.minLine,
			endLine:    g.maxLine,
			source:     g.source,
		}
		out = append(out, merged)
	}

	// After mixing merged chunks and original unmerged items, we should re-sort by score
	sort.Slice(out, func(i, j int) bool {
		return out[i].score > out[j].score
	})

	return out
}


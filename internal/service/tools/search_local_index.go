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

// SearchLocalIndexTool implements the rag_search_code MCP tool.
type SearchLocalIndexTool struct {
	engine      *engine.Engine
	searchLimit int
}

// NewSearchLocalIndexTool creates a new search tool backed by the Engine.
func NewSearchLocalIndexTool(eng *engine.Engine) *SearchLocalIndexTool {
	return &SearchLocalIndexTool{
		engine:      eng,
		searchLimit: 10,
	}
}

func (t *SearchLocalIndexTool) Name() string { return "rag_search_code" }
func (t *SearchLocalIndexTool) Description() string {
	return "Comprehensive code search for finding logic, symbols, or understanding flow. " +
		"Use 'discovery' mode (default) for conceptual questions and broad exploration (finds by MEANING). " +
		"Use 'exact' mode for precise matches of symbols, specific errors, and variable names (Hybrid search). " +
		"Returns complete source code with file path and line numbers. Supports all registered languages. " +
		"NEW: Automatically performs 'Graph Context Expansion' - it reads AST structurally related dependencies (methods, structs, interfaces) from the code and auto-fetches their definitions in the same response! " +
		"IMPORTANT: Always provide the 'file_path' of the file you are currently working on for better context detection."
}

type SearchLocalIndexInput struct {
	Query       string `json:"query"`
	FilePath    string `json:"file_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Mode        string `json:"mode,omitempty" enum:"discovery,exact"`
	IncludeDocs bool   `json:"include_docs,omitempty"`
}

func (t *SearchLocalIndexTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchLocalIndexInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"query":        input.Query,
			"file_path":    input.FilePath,
			"limit":        input.Limit,
			"mode":         input.Mode,
			"include_docs": input.IncludeDocs,
		}

		logger.Instance.Debug("rag_search_code invoked with params: %+v", args)

		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_search_code failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		logger.Instance.Info("rag_search_code completed in %v", time.Since(start))
		logger.Instance.Debug("rag_search_code result size string (bytes): %d", len(result))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *SearchLocalIndexTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	filePath, _ := params["file_path"].(string)
	mode, _ := params["mode"].(string)
	if mode == "" {
		mode = "discovery"
	}

	logger.Instance.Highlight("rag_search_code (%s): '%s' (context: %s)", mode, query, filePath)
	logger.Instance.Debug("Executing %s search. Query: %q, FilePath: %q", mode, query, filePath)

	limit := t.searchLimit
	if l, ok := params["limit"].(int); ok && l > 0 {
		limit = l
	}

	includeDocs, _ := params["include_docs"].(bool)

	response := ToolResponse{
		Status: "success",
	}

	var result *engine.SearchCodeResult
	var err error

	if mode == "exact" {
		result, err = t.engine.HybridSearchCode(ctx, filePath, query, limit)
	} else {
		result, err = t.engine.SearchCode(ctx, filePath, query, limit, includeDocs)
	}
	if err != nil {
		logger.Instance.Debug("Search engine returned error: %v", err)

		if strings.Contains(err.Error(), "No workspace detected") {
			response.Status = "error"
			response.Error = err.Error()
			return response.JSON()
		}

		var notIndexed *engine.ErrNotIndexed
		var indexingStarted *engine.ErrIndexingStarted
		var indexingInProgress *engine.ErrIndexingInProgress

		if errors.As(err, &indexingStarted) {
			logger.Instance.Debug("Background indexing started fallback trigger")
			response.Status = "indexing_started"
			response.Message = fmt.Sprintf("🚀 Workspace '%s' was not indexed. Background indexing has been STARTED automatically. Please wait a few moments and try your search again.", indexingStarted.WorkspaceRoot)
			response.Context.WorkspaceRoot = indexingStarted.WorkspaceRoot
			response.Context.DetectionSource = "registry_fallback" // Fallback assumed if SearchCode failed with indexing_started on empty path
			if indexingStarted.WorkspaceID != "" {
				if p := t.engine.GetIndexProgress(indexingStarted.WorkspaceID, indexingStarted.WorkspaceRoot); p != nil {
					response.Data = map[string]any{"indexing": p}
				}
			}
			return response.JSON()
		}

		if errors.As(err, &indexingInProgress) {
			logger.Instance.Debug("Indexing currently in progress for %s", indexingInProgress.WorkspaceRoot)
			response.Status = "indexing_in_progress"
			response.Message = fmt.Sprintf("⏳ Workspace '%s' is currently being indexed. Search results will be available once indexing completes.", indexingInProgress.WorkspaceRoot)
			response.Context.WorkspaceRoot = indexingInProgress.WorkspaceRoot
			if indexingInProgress.WorkspaceID != "" {
				if p := t.engine.GetIndexProgress(indexingInProgress.WorkspaceID, indexingInProgress.WorkspaceRoot); p != nil {
					response.Data = map[string]any{"indexing": p}
				}
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

	logger.Instance.Debug("Search successful. Found %d results. Risk mismatch: %s", len(result.Results), result.MismatchRisk)

	response.Context = ContextMetadata{
		WorkspaceRoot:    result.WorkspaceRoot,
		Collection:       result.Collection,
		Language:         result.Language,
		DetectionSource:  result.DetectionSource,
		IndexingProgress: BuildIndexingProgress(t.engine, result.WorkspaceID, result.WorkspaceRoot),
	}

	if result.MismatchRisk != "" && result.MismatchRisk != "low" {
		response.Warning = fmt.Sprintf("Branch mismatch risk: %s — results may be from a different branch.", result.MismatchRisk)
	}

	if len(result.Results) == 0 {
		response.Status = "no_results"
		response.Message = fmt.Sprintf("🔍 No code results found for query: '%s'", query)
		return response.JSON()
	}

	// Extract standard descriptors
	descriptors := make([]map[string]any, 0, len(result.Results))
	seenIDs := make(map[string]bool)
	seenFiles := make(map[string]bool)
	baselineBytes := 0
	actualBytes := 0
	var staleFiles []string // files referenced in index but no longer on disk

	for _, r := range result.Results {
		if seenIDs[r.Point.ID] {
			continue
		}
		seenIDs[r.Point.ID] = true

		if content, ok := r.Point.Payload["content"].(string); ok {
			actualBytes += len(content)
		}

		if path, ok := r.Point.Payload["file_path"].(string); ok {
			if !seenFiles[path] {
				seenFiles[path] = true
				if info, statErr := os.Stat(path); statErr == nil {
					baselineBytes += int(info.Size())
				} else if os.IsNotExist(statErr) {
					staleFiles = append(staleFiles, path)
				}
			}
		}

		desc := make(map[string]any)
		for k, v := range r.Point.Payload {
			desc[k] = v
		}
		desc["score"] = r.Score
		desc["id"] = r.Point.ID
		descriptors = append(descriptors, desc)
	}

	// Proactive stale index warning: inform the AI that some indexed files no longer exist.
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

	// 2. Graph Context Expansion
	// We will look at the 'relations' in the payload of the TOP result
	topResult := result.Results[0]
	relationsRaw, hasRel := topResult.Point.Payload["relations"]

	if hasRel {
		// Depending on the storage, relations might be []interface{}
		if relList, ok := relationsRaw.([]interface{}); ok {
			const maxExpansions = 10
			wsID := result.WorkspaceID

			type subResult struct {
				targetName string
				results    []storage.SearchResult
			}

			subChan := make(chan subResult, maxExpansions)
			var wg sync.WaitGroup
			seenTargets := make(map[string]bool)
			expanded := 0

			for _, relRaw := range relList {
				if expanded >= maxExpansions {
					break
				}
				relMap, ok := relRaw.(map[string]interface{})
				if !ok {
					continue
				}
				targetName, _ := relMap["target_name"].(string)
				if targetName == "" || seenTargets[targetName] {
					continue
				}
				seenTargets[targetName] = true
				expanded++

				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					// ExactSearch only — zero embedding, deterministic.
					// No fallback to embedding search: relation names are often stdlib/external
					// symbols not in the local index, and each embedding call costs ~N seconds.
					res, err := t.engine.SearchByName(ctx, wsID, name, 2)
					if err != nil || len(res) == 0 {
						return
					}
					subChan <- subResult{targetName: name, results: res}
				}(targetName)
			}

			go func() { wg.Wait(); close(subChan) }()

			topName, _ := topResult.Point.Payload["name"].(string)
			var fetchedTargets []string
			for sr := range subChan {
				for _, sub := range sr.results {
					if seenIDs[sub.Point.ID] {
						continue
					}
					seenIDs[sub.Point.ID] = true

					subDesc := make(map[string]any)
					for k, v := range sub.Point.Payload {
						subDesc[k] = v
					}
					subDesc["_graph_expansion"] = fmt.Sprintf("Dependency of %v", topName)
					subDesc["score"] = sub.Score
					subDesc["id"] = sub.Point.ID
					descriptors = append(descriptors, subDesc)
					fetchedTargets = append(fetchedTargets, sr.targetName)
					break // top match per dependency
				}
			}

			if len(fetchedTargets) > 0 {
				response.Message = fmt.Sprintf("Auto-fetched %d related dependencies: %s", len(fetchedTargets), strings.Join(fetchedTargets, ", "))
			}
		}
	}

	// Sort all descriptors (primary + graph expansions) by score descending
	// so the highest-relevance results are always surfaced first in the response.
	sort.Slice(descriptors, func(i, j int) bool {
		si, _ := descriptors[i]["score"].(float32)
		sj, _ := descriptors[j]["score"].(float32)
		return si > sj
	})

	response.Context.Telemetry = telemetry.CalculateSavings(baselineBytes, actualBytes)
	response.Data = descriptors
	return response.JSON()
}

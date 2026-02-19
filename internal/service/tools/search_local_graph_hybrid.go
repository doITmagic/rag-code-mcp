package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SearchLocalGraphHybridTool implements the rag_graph_search MCP tool.
type SearchLocalGraphHybridTool struct {
	engine      *engine.Engine
	searchLimit int
}

// NewSearchLocalGraphHybridTool creates a new graph search tool backed by the Engine.
func NewSearchLocalGraphHybridTool(eng *engine.Engine) *SearchLocalGraphHybridTool {
	return &SearchLocalGraphHybridTool{
		engine:      eng,
		searchLimit: 10,
	}
}

func (t *SearchLocalGraphHybridTool) Name() string { return "rag_graph_search" }
func (t *SearchLocalGraphHybridTool) Description() string {
	return "EXPERIMENTAL: Code search that also resolves deep graph dependencies. " +
		"Use this for obtaining a symbol AND the definitions of the classes/structs it depends on in a single call. " +
		"IMPORTANT: Always provide the 'file_path' of the file you are currently working on for better context detection."
}

type SearchLocalGraphHybridInput struct {
	Query    string `json:"query"`
	FilePath string `json:"file_path,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

func (t *SearchLocalGraphHybridTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input SearchLocalGraphHybridInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"query":     input.Query,
			"file_path": input.FilePath,
			"limit":     input.Limit,
		}

		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_graph_search failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		logger.Instance.Info("rag_graph_search completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *SearchLocalGraphHybridTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	filePath, _ := params["file_path"].(string)

	logger.Instance.Highlight("rag_graph_search: '%s' (context: %s)", query, filePath)

	limit := t.searchLimit
	if l, ok := params["limit"].(int); ok && l > 0 {
		limit = l
	}

	response := ToolResponse{
		Status: "success",
	}

	// 1. Primary Hybrid Search
	result, err := t.engine.HybridSearchCode(ctx, filePath, query, limit)
	if err != nil {
		if strings.Contains(err.Error(), "No workspace detected") {
			response.Status = "error"
			response.Error = err.Error()
			return response.JSON()
		}

		var notIndexed *engine.ErrNotIndexed
		var indexingStarted *engine.ErrIndexingStarted
		var indexingInProgress *engine.ErrIndexingInProgress

		if errors.As(err, &indexingStarted) {
			response.Status = "indexing_started"
			response.Message = fmt.Sprintf("🚀 Workspace '%s' was not indexed. Background indexing has been STARTED automatically. Please wait a few moments and try your search again.", indexingStarted.WorkspaceRoot)
			response.Context.WorkspaceRoot = indexingStarted.WorkspaceRoot
			response.Context.DetectionSource = "registry_fallback"
			return response.JSON()
		}

		if errors.As(err, &indexingInProgress) {
			response.Status = "indexing_in_progress"
			response.Message = fmt.Sprintf("⏳ Workspace '%s' is currently being indexed. Search results will be available once indexing completes.", indexingInProgress.WorkspaceRoot)
			response.Context.WorkspaceRoot = indexingInProgress.WorkspaceRoot
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

	response.Context = ContextMetadata{
		WorkspaceRoot:   result.WorkspaceRoot,
		Collection:      result.Collection,
		Language:        result.Language,
		DetectionSource: result.DetectionSource,
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

	for _, r := range result.Results {
		if seenIDs[r.Point.ID] {
			continue
		}
		seenIDs[r.Point.ID] = true

		desc := make(map[string]any)
		for k, v := range r.Point.Payload {
			desc[k] = v
		}
		desc["score"] = r.Score
		desc["id"] = r.Point.ID
		descriptors = append(descriptors, desc)
	}

	// 2. Graph Context Expansion
	// We will look at the 'Relations' in the payload of the TOP result
	topResult := result.Results[0]
	relationsRaw, hasRel := topResult.Point.Payload["Relations"]
	if !hasRel {
		relationsRaw, hasRel = topResult.Point.Payload["relations"]
	}

	if hasRel {
		// Depending on the storage, relations might be []interface{}
		if relList, ok := relationsRaw.([]interface{}); ok {
			var fetchedTargets []string

			for _, relRaw := range relList {
				relMap, ok := relRaw.(map[string]interface{})
				if !ok {
					continue
				}

				targetName, _ := relMap["target_name"].(string)
				if targetName == "" {
					continue
				}

				// perform a tiny sub-search for this target
				subRes, err := t.engine.SearchCode(ctx, filePath, targetName, 2, false)
				if err == nil && len(subRes.Results) > 0 {
					for _, sub := range subRes.Results {
						if seenIDs[sub.Point.ID] {
							continue
						}
						seenIDs[sub.Point.ID] = true

						subDesc := make(map[string]any)
						for k, v := range sub.Point.Payload {
							subDesc[k] = v
						}
						// Mark this as an expanded node context
						subDesc["_graph_expansion"] = fmt.Sprintf("Dependency of %s", topResult.Point.Payload["name"])
						subDesc["score"] = sub.Score
						subDesc["id"] = sub.Point.ID
						descriptors = append(descriptors, subDesc)
						fetchedTargets = append(fetchedTargets, targetName)
						break // only include top match for the dependency
					}
				}
			}

			if len(fetchedTargets) > 0 {
				response.Message = fmt.Sprintf("Auto-fetched %d related dependencies: %s", len(fetchedTargets), strings.Join(fetchedTargets, ", "))
			}
		}
	}

	response.Data = descriptors
	return response.JSON()
}

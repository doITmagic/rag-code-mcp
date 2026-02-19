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
	return "Semantic code search - finds functions, classes, and methods by MEANING, not just keywords. " +
		"USE THIS FIRST when exploring unfamiliar code. Returns complete source code with file path and line numbers. " +
		"Better than rag_hybrid_search for general exploration; use rag_hybrid_search only when you need EXACT identifier matches. " +
		"Supports Go, PHP, Python, HTML. IMPORTANT: Always provide the 'file_path' of the file you are currently working on for better context detection."
}

type SearchLocalIndexInput struct {
	Query       string `json:"query"`
	FilePath    string `json:"file_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
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
			"include_docs": input.IncludeDocs,
		}

		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_search_code failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		logger.Instance.Info("rag_search_code completed in %v", time.Since(start))
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
	limit := t.searchLimit
	if l, ok := params["limit"].(int); ok && l > 0 {
		limit = l
	}

	includeDocs, _ := params["include_docs"].(bool)

	response := ToolResponse{
		Status: "success",
	}

	result, err := t.engine.SearchCode(ctx, filePath, query, limit, includeDocs)
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
			response.Context.DetectionSource = "registry_fallback" // Fallback assumed if SearchCode failed with indexing_started on empty path
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

		return "", fmt.Errorf("search failed: %w", err)
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

	// Prepare data
	descriptors := make([]map[string]any, 0, len(result.Results))
	for _, r := range result.Results {
		desc := make(map[string]any)
		for k, v := range r.Point.Payload {
			desc[k] = v
		}
		desc["score"] = r.Score
		desc["id"] = r.Point.ID
		descriptors = append(descriptors, desc)
	}

	response.Data = descriptors
	return response.JSON()
}

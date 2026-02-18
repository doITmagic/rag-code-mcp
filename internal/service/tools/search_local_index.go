package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
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
		searchLimit: 5,
	}
}

// SetSearchLimit overrides the default result limit.
func (t *SearchLocalIndexTool) SetSearchLimit(limit int) {
	if limit > 0 {
		t.searchLimit = limit
	}
}

// Name returns the MCP tool name.
func (t *SearchLocalIndexTool) Name() string {
	return "rag_search_code"
}

// Description returns the MCP tool description.
func (t *SearchLocalIndexTool) Description() string {
	return "Semantic code search - finds functions, classes, and methods by MEANING, not just keywords. " +
		"USE THIS FIRST when exploring unfamiliar code. Returns complete source code with file path and line numbers. " +
		"Better than rag_hybrid_search for general exploration; use rag_hybrid_search only when you need EXACT identifier matches. " +
		"Supports Go, PHP, Python, HTML. IMPORTANT: Always provide the 'file_path' of the file you are currently working on for better context detection.\n" +
		"Example: { \"query\": \"auth middleware\", \"file_path\": \"/path/to/project/server.go\" }"
}

// Execute runs the semantic search and returns JSON results.
func (t *SearchLocalIndexTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	query, ok := params["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	// Simplified parameter extraction: strict 'file_path' only.
	filePath, _ := params["file_path"].(string)

	limit := t.searchLimit
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	} else if l, ok := params["limit"].(int); ok {
		limit = l
	}

	includeDocs := false
	if inc, ok := params["include_docs"].(bool); ok {
		includeDocs = inc
	}

	result, err := t.engine.SearchCode(ctx, filePath, query, limit, includeDocs)
	if err != nil {
		var notIndexed *engine.ErrNotIndexed
		var indexingStarted *engine.ErrIndexingStarted
		var indexingInProgress *engine.ErrIndexingInProgress

		if errors.As(err, &indexingStarted) {
			return fmt.Sprintf("🚀 Workspace '%s' was not indexed. Background indexing has been STARTED automatically.\n"+
				"Please wait a few moments and try your search again.\n"+
				"You can continue with other tasks while indexing completes.", indexingStarted.WorkspaceRoot), nil
		}

		if errors.As(err, &indexingInProgress) {
			return fmt.Sprintf("⏳ Workspace '%s' is currently being indexed.\n"+
				"Search results will be available once indexing completes. Please try again shortly.",
				indexingInProgress.WorkspaceRoot), nil
		}

		if errors.As(err, &notIndexed) {
			// Should be rare now with auto-indexing, but good as fallback
			return fmt.Sprintf("❌ Workspace '%s' is not indexed yet.\n"+
				"Please use 'rag_index_workspace' to index it manually.",
				notIndexed.WorkspaceRoot), nil
		}

		return "", fmt.Errorf("search failed: %w", err)
	}

	if len(result.Results) == 0 {
		return fmt.Sprintf(
			"🔍 No code results found for query: '%s'\n"+
				"Workspace: %s\n"+
				"Collection: %s\n"+
				"Make sure the code is indexed and the query is relevant to the codebase.",
			query,
			result.WorkspaceRoot,
			result.Collection,
		), nil
	}

	descriptors := searchResultsToDescriptors(result.Results, result.WorkspaceRoot)

	if result.MismatchRisk != "" && result.MismatchRisk != "low" {
		// Prepend a warning for medium/high mismatch risk
		warning := fmt.Sprintf("⚠️  Branch mismatch risk: %s — results may be from a different branch. Consider re-indexing.\n\n", result.MismatchRisk)
		data, err := json.MarshalIndent(descriptors, "", "  ")
		if err != nil {
			return "", fmt.Errorf("failed to marshal results: %w", err)
		}
		return warning + string(data), nil
	}

	data, err := json.MarshalIndent(descriptors, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal results: %w", err)
	}
	return string(data), nil
}

// searchResultsToDescriptors converts storage.SearchResult slice to a JSON-friendly structure.
func searchResultsToDescriptors(results []storage.SearchResult, workspaceRoot string) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		desc := make(map[string]any)
		// Copy all payload fields directly
		for k, v := range r.Point.Payload {
			desc[k] = v
		}
		desc["score"] = r.Score
		desc["id"] = r.Point.ID
		if workspaceRoot != "" {
			desc["workspace_root"] = workspaceRoot
		}
		out = append(out, desc)
	}
	return out
}

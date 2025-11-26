package tools

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// SearchDocsTool searches the docs (Markdown) vector index
type SearchDocsTool struct {
	longTermMemory   memory.LongTermMemory
	embedder         llm.Provider
	workspaceManager *workspace.Manager
}

// NewSearchDocsTool creates a new search docs tool
func NewSearchDocsTool(ltm memory.LongTermMemory, embedder llm.Provider) *SearchDocsTool {
	return &SearchDocsTool{
		longTermMemory: ltm,
		embedder:       embedder,
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *SearchDocsTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

// Name returns the tool name
func (t *SearchDocsTool) Name() string {
	return "search_docs"
}

// Description returns the tool description
func (t *SearchDocsTool) Description() string {
	return "Search project documentation (README, guides, API docs) - use when you need to understand project setup, architecture decisions, or usage examples. Returns relevant documentation snippets with file paths. Searches Markdown files ONLY, not code - use search_code for code."
}

// Execute executes a search in the docs index
func (t *SearchDocsTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// file_path is required for workspace detection
	filePath := extractFilePathFromParams(params)
	if filePath == "" {
		return "", fmt.Errorf("file_path parameter is required for search_docs. Please provide a file path from your workspace")
	}

	// Try workspace detection
	var searchMemory memory.LongTermMemory
	var workspacePath string
	var collectionName string

	if t.workspaceManager != nil {
		workspaceInfo, err := t.workspaceManager.DetectWorkspace(params)
		if err == nil && workspaceInfo != nil {
			workspacePath = workspaceInfo.Root

			// Detect language from file path or use first detected language
			language := inferLanguageFromPath(filePath)
			if language == "" && len(workspaceInfo.Languages) > 0 {
				language = workspaceInfo.Languages[0]
			}
			if language == "" {
				language = workspaceInfo.ProjectType
			}

			collectionName = workspaceInfo.CollectionNameForLanguage(language)
			mem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
			if err == nil && mem != nil {
				// Check if indexing is in progress
				indexKey := workspaceInfo.ID + "-" + language
				if t.workspaceManager.IsIndexing(indexKey) {
					return fmt.Sprintf("⏳ Workspace '%s' language '%s' is currently being indexed in the background.\n"+
						"Please try again in a few moments.\n"+
						"Workspace: %s\n"+
						"Language: %s\n"+
						"Collection: %s",
						workspaceInfo.Root, language, workspaceInfo.Root, language, collectionName), nil
				}

				// Check if collection exists before proceeding
				if msg, err := CheckCollectionStatus(ctx, mem, collectionName, workspacePath); err != nil || msg != "" {
					if err != nil {
						return "", err
					}
					return msg, nil
				}

				searchMemory = mem
			}
		}
	}

	// Use workspace-specific memory or fallback to default
	if searchMemory == nil {
		searchMemory = t.longTermMemory
	}

	if searchMemory == nil {
		return "Documentation search is not configured. Set docs.collection in config.yaml and rebuild the docs index.", nil
	}

	if t.embedder == nil {
		return "Documentation search is currently unavailable because no embedding provider is configured.", nil
	}

	query, ok := params["query"].(string)
	if !ok {
		return "", fmt.Errorf("query parameter is required")
	}

	limit := 5
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	} else if l, ok := params["limit"].(int); ok {
		limit = l
	}

	// Generate embedding for query
	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	docs, err := searchMemory.Search(ctx, queryEmbedding, limit)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}

	if len(docs) == 0 {
		// Check if this is a workspace search with empty collection
		if workspacePath != "" && collectionName != "" {
			if msg, err := CheckSearchResults(0, collectionName, workspacePath); err != nil || msg != "" {
				if err != nil {
					return "", err
				}
				return msg, nil
			}
			return fmt.Sprintf("No relevant documentation found in workspace '%s'.", workspacePath), nil
		}
		return "No relevant documentation found.", nil
	}

	if workspacePath != "" {
		result := fmt.Sprintf("🔍 Found %d relevant documentation snippets in workspace '%s':\n\n", len(docs), workspacePath)
		for i, doc := range docs {
			result += fmt.Sprintf("--- Result %d ---\n%s\n\n", i+1, doc.Content)
		}
		return result, nil
	}

	result := fmt.Sprintf("Found %d relevant documentation snippets:\n\n", len(docs))
	for i, doc := range docs {
		result += fmt.Sprintf("--- Result %d ---\n%s\n\n", i+1, doc.Content)
	}

	return result, nil
}

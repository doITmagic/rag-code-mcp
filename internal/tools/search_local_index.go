package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/llm"
	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// SearchLocalIndexTool searches the local vector index
type SearchLocalIndexTool struct {
	embedder         llm.Provider
	memories         []memory.LongTermMemory // Fallback memories if workspace detection fails
	workspaceManager *workspace.Manager      // Workspace-aware collection manager
}

// NewSearchLocalIndexTool creates a new search local index tool
func NewSearchLocalIndexTool(ltm memory.LongTermMemory, embedder llm.Provider, additional ...memory.LongTermMemory) *SearchLocalIndexTool {
	memories := make([]memory.LongTermMemory, 0, 1+len(additional))
	if ltm != nil {
		memories = append(memories, ltm)
	}
	memories = append(memories, additional...)
	return &SearchLocalIndexTool{
		embedder: embedder,
		memories: memories,
	}
}

// SetWorkspaceManager sets the workspace manager for workspace-aware searching
func (t *SearchLocalIndexTool) SetWorkspaceManager(wm *workspace.Manager) {
	t.workspaceManager = wm
}

// Name returns the tool name
func (t *SearchLocalIndexTool) Name() string {
	return "search_code"
}

// Description returns the tool description
func (t *SearchLocalIndexTool) Description() string {
	return "Searches the local vector database for relevant documents"
}

// Execute executes a search in the local index
func (t *SearchLocalIndexTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
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

	// Optional output format: json (default) or markdown
	outputFormat := "json"
	if of, ok := params["output_format"].(string); ok && of != "" {
		outputFormat = strings.ToLower(of)
	}

	// Generate embedding for query
	queryEmbedding, err := t.embedder.Embed(ctx, query)
	if err != nil {
		return "", fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// file_path is required for workspace detection
	filePath := extractFilePathFromParams(params)
	if filePath == "" {
		return "", fmt.Errorf("file_path parameter is required for search_code. Please provide a file path from your workspace")
	}

	// Try workspace-aware search first
	if t.workspaceManager != nil {
		workspaceInfo, err := t.workspaceManager.DetectWorkspace(params)
		if err != nil {
			// Workspace detection failed - return helpful message
			return fmt.Sprintf("❌ Could not detect workspace from the provided file path.\n\n"+
				"To enable workspace-aware code search, please provide a valid file_path parameter "+
				"pointing to a file within your workspace.\n\n"+
				"Error: %v", err), nil
		}

		// Detect language from file path or query context
		language := ""
		if filePath := extractFilePathFromParams(params); filePath != "" {
			language = inferLanguageFromPath(filePath)
		}

		// If no language detected from path, use first detected language in workspace
		if language == "" && len(workspaceInfo.Languages) > 0 {
			language = workspaceInfo.Languages[0]
		}

		// Fallback to ProjectType
		if language == "" {
			language = workspaceInfo.ProjectType
		}

		// Get workspace-specific memory for the detected language
		workspaceMem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
		if err != nil {
			// Collection doesn't exist - tell AI to index first
			collectionName := workspaceInfo.CollectionNameForLanguage(language)
			return fmt.Sprintf("❌ Workspace '%s' is not indexed yet.\n\n"+
				"To enable code search, please call the 'index_workspace' tool first with:\n"+
				"{\n"+
				"  \"file_path\": \"%s\"\n"+
				"}\n\n"+
				"Details:\n"+
				"- Workspace: %s\n"+
				"- Language: %s\n"+
				"- Collection: %s (not created yet)\n",
				workspaceInfo.Root,
				workspaceInfo.Root,
				workspaceInfo.Root,
				language,
				collectionName), nil
		}

		// Check if currently indexing
		indexKey := workspaceInfo.ID + "-" + language
		if t.workspaceManager.IsIndexing(indexKey) {
			return fmt.Sprintf("⏳ Workspace '%s' language '%s' is currently being indexed in the background.\n"+
				"Please try again in a few moments.\n"+
				"Workspace: %s\n"+
				"Language: %s\n"+
				"Collection: %s",
				workspaceInfo.Root, language, workspaceInfo.Root, language, workspaceInfo.CollectionNameForLanguage(language)), nil
		}

		// Check if collection exists before searching (if memory supports it)
		collectionName := workspaceInfo.CollectionNameForLanguage(language)

		// Type assertion to check if this memory supports collection existence checking
		type CollectionChecker interface {
			CollectionExists(ctx context.Context, name string) (bool, error)
		}

		if checker, ok := workspaceMem.(CollectionChecker); ok {
			exists, checkErr := checker.CollectionExists(ctx, collectionName)

			if checkErr != nil || !exists {
				// Collection doesn't exist - tell AI to index first
				return fmt.Sprintf("❌ Workspace '%s' is not indexed yet.\n\n"+
					"To enable code search, please call the 'index_workspace' tool first with:\n"+
					"{\n"+
					"  \"file_path\": \"%s\"\n"+
					"}\n\n"+
					"Details:\n"+
					"- Workspace: %s\n"+
					"- Language: %s\n"+
					"- Collection: %s (not created yet)\n",
					workspaceInfo.Root,
					workspaceInfo.Root,
					workspaceInfo.Root,
					language,
					collectionName), nil
			}
		}

		// Search in workspace-specific collection
		docs, err := workspaceMem.Search(ctx, queryEmbedding, limit)

		// If search succeeds but returns no results, check if collection is empty
		if err == nil && len(docs) == 0 {
			// Collection might be empty - tell AI to index
			collectionName := workspaceInfo.CollectionNameForLanguage(language)
			return fmt.Sprintf("❌ Workspace '%s' appears to be empty or not indexed yet.\n\n"+
				"To enable code search, please call the 'index_workspace' tool with:\n"+
				"{\n"+
				"  \"file_path\": \"%s\"\n"+
				"}\n\n"+
				"Details:\n"+
				"- Workspace: %s\n"+
				"- Language: %s\n"+
				"- Collection: %s (exists but may be empty)\n",
				workspaceInfo.Root,
				workspaceInfo.Root,
				workspaceInfo.Root,
				language,
				collectionName), nil
		}

		if err == nil && len(docs) > 0 {
			if outputFormat == "markdown" {
				result := fmt.Sprintf("🔍 Found %d relevant code snippets in workspace '%s':\n\n",
					len(docs), workspaceInfo.Root)
				for i, doc := range docs {
					result += fmt.Sprintf("--- Result %d ---\n%s\n\n", i+1, doc.Content)
				}
				return result, nil
			}

			descriptors := buildSymbolDescriptorsFromDocs(docs)
			data, err := json.MarshalIndent(descriptors, "", "  ")
			if err != nil {
				return "", fmt.Errorf("failed to marshal search_code results: %w", err)
			}
			return string(data), nil
		}
	}

	// Fallback: search in default memories
	if len(t.memories) == 0 {
		return "", fmt.Errorf("no long-term memories configured for search")
	}

	collected := make([]memory.Document, 0)
	remaining := limit

	for _, ltm := range t.memories {
		if remaining <= 0 {
			break
		}
		docs, err := ltm.Search(ctx, queryEmbedding, remaining)
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}
		collected = append(collected, docs...)
		remaining = limit - len(collected)
	}

	if len(collected) == 0 {
		if outputFormat == "markdown" {
			return "No relevant code found.", nil
		}
		// Empty JSON array to indicate no results in a structured way
		return "[]", nil
	}

	if outputFormat == "markdown" {
		result := fmt.Sprintf("Found %d relevant code snippets:\n\n", len(collected))
		for i, doc := range collected {
			result += fmt.Sprintf("--- Result %d ---\n%s\n\n", i+1, doc.Content)
		}
		return result, nil
	}

	descriptors := buildSymbolDescriptorsFromDocs(collected)
	data, err := json.MarshalIndent(descriptors, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal search_code results: %w", err)
	}
	return string(data), nil
}

package tools

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// IndexWorkspaceTool indexes a workspace for code search
type IndexWorkspaceTool struct {
	workspaceManager *workspace.Manager
}

// NewIndexWorkspaceTool creates a new index workspace tool
func NewIndexWorkspaceTool(wm *workspace.Manager) *IndexWorkspaceTool {
	return &IndexWorkspaceTool{
		workspaceManager: wm,
	}
}

// Name returns the tool name
func (t *IndexWorkspaceTool) Name() string {
	return "index_workspace"
}

// Description returns the tool description
func (t *IndexWorkspaceTool) Description() string {
	return "Indexes a workspace to enable code search. Call this when you receive a message that the workspace is not yet indexed. Requires file_path parameter to detect the workspace."
}

// Execute indexes the workspace
func (t *IndexWorkspaceTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.workspaceManager == nil {
		return "", fmt.Errorf("workspace manager not configured")
	}

	// Detect workspace from params
	workspaceInfo, err := t.workspaceManager.DetectWorkspace(params)
	if err != nil {
		return "", fmt.Errorf("failed to detect workspace: %w", err)
	}

	// Optional: allow specifying specific language to index
	language := ""
	if lang, ok := params["language"].(string); ok && lang != "" {
		language = lang
	}

	// If no language specified, index all detected languages
	if language == "" {
		if len(workspaceInfo.Languages) == 0 {
			// Use ProjectType as fallback
			language = workspaceInfo.ProjectType
			if language == "" || language == "unknown" {
				return "", fmt.Errorf("no languages detected in workspace: %s", workspaceInfo.Root)
			}
		}
	}

	// If still no specific language, index all languages
	if language == "" {
		// Index all detected languages
		memories, err := t.workspaceManager.GetMemoriesForAllLanguages(ctx, workspaceInfo)
		if err != nil {
			return "", fmt.Errorf("failed to initialize indexing for workspace: %w", err)
		}

		languageList := ""
		for lang := range memories {
			if languageList != "" {
				languageList += ", "
			}
			languageList += lang
		}

		return fmt.Sprintf("✓ Indexing started for workspace '%s'\n"+
			"Languages: %s\n"+
			"Collections will be created: %s\n"+
			"Indexing is running in the background. You can use search_code immediately - results will appear as indexing progresses.",
			workspaceInfo.Root,
			languageList,
			getCollectionNames(workspaceInfo, memories)), nil
	}

	// Index specific language
	mem, err := t.workspaceManager.GetMemoryForWorkspaceLanguage(ctx, workspaceInfo, language)
	if err != nil {
		return "", fmt.Errorf("failed to initialize indexing for language '%s': %w", language, err)
	}

	collectionName := workspaceInfo.CollectionNameForLanguage(language)

	// SCENARIO 1: Check if currently indexing
	indexKey := workspaceInfo.ID + "-" + language
	if t.workspaceManager.IsIndexing(indexKey) {
		return fmt.Sprintf("⏳ Workspace '%s' language '%s' is already being indexed in the background.\n"+
			"Collection: %s\n"+
			"You can use search_code immediately - results will appear as indexing progresses.",
			workspaceInfo.Root, language, collectionName), nil
	}

	// SCENARIO 2 & 3: Check if collection exists and has data
	if checker, ok := mem.(interface {
		GetCollectionPointCount(ctx context.Context, collectionName string) (uint64, error)
	}); ok {
		pointCount, err := checker.GetCollectionPointCount(ctx, collectionName)
		if err == nil && pointCount > 0 {
			// SCENARIO 3: Collection already indexed - Trigger incremental update
			log.Printf("🔄 Workspace '%s' is already indexed. Triggering incremental update...", workspaceInfo.Root)

			// Continue to StartIndexing which now handles incremental updates
		}
	}

	// SCENARIO 2: Start indexing (collection doesn't exist or is empty)
	// Force indexing to start (or restart if stopped)
	if err := t.workspaceManager.StartIndexing(ctx, workspaceInfo, language); err != nil {
		// If error is "already indexing", that's fine
		if !t.workspaceManager.IsIndexing(indexKey) {
			return "", fmt.Errorf("failed to start indexing: %w", err)
		}
	}

	log.Printf("📦 Tool triggered indexing for workspace: %s, language: %s, collection: %s",
		workspaceInfo.Root, language, collectionName)

	// Explicitly start indexing using StartIndexing method
	if err := t.workspaceManager.StartIndexing(ctx, workspaceInfo, language); err != nil {
		// If error is "already in progress", that's okay (race condition)
		if !strings.Contains(err.Error(), "already in progress") {
			return "", fmt.Errorf("failed to start indexing: %w", err)
		}
	}

	return fmt.Sprintf("✓ Indexing started for workspace '%s'\n"+
		"Language: %s\n"+
		"Collection: %s\n"+
		"Memory instance: %T\n"+
		"Indexing is running in the background. You can use search_code immediately - results will appear as indexing progresses.",
		workspaceInfo.Root,
		language,
		collectionName,
		mem), nil
}

// Helper to get collection names from memories map
func getCollectionNames(info *workspace.Info, memories map[string]memory.LongTermMemory) string {
	result := ""
	for lang := range memories {
		if result != "" {
			result += ", "
		}
		result += info.CollectionNameForLanguage(lang)
	}
	return result
}

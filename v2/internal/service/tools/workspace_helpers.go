package tools

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// CheckCollectionStatus verifies if a collection exists and has data.
// Returns an error message if the collection is missing or empty, nil otherwise.
func CheckCollectionStatus(ctx context.Context, mem memory.LongTermMemory, collectionName, workspacePath string) (string, error) {
	// Type assertion to check if this memory supports collection existence checking
	type CollectionChecker interface {
		CollectionExists(ctx context.Context, name string) (bool, error)
	}

	if checker, ok := mem.(CollectionChecker); ok {
		exists, err := checker.CollectionExists(ctx, collectionName)
		if err != nil {
			return "", fmt.Errorf("failed to check collection status: %w", err)
		}

		if !exists {
			// Collection doesn't exist - tell AI to index first
			return fmt.Sprintf("❌ Workspace '%s' is not indexed yet.\n\n"+
				"To enable this operation, please call the 'rag_index_workspace' tool first with:\n"+
				"{\n"+
				"  \"file_path\": \"%s\"\n"+
				"}\n\n"+
				"Details:\n"+
				"- Workspace: %s\n"+
				"- Collection: %s (not created yet)\n",
				workspacePath,
				workspacePath,
				workspacePath,
				collectionName), nil
		}
	}

	return "", nil
}

// CheckSearchResults verifies if search returned any results.
// Returns an error message if no results found, nil otherwise.
func CheckSearchResults(resultCount int, collectionName, workspacePath string) (string, error) {
	if resultCount == 0 {
		// Collection exists but is empty - tell AI to index
		return fmt.Sprintf("❌ Workspace '%s' appears to be empty or not fully indexed.\n\n"+
			"To enable this operation, please call the 'rag_index_workspace' tool with:\n"+
			"{\n"+
			"  \"file_path\": \"%s\"\n"+
			"}\n\n"+
			"Details:\n"+
			"- Workspace: %s\n"+
			"- Collection: %s (exists but may be empty)\n",
			workspacePath,
			workspacePath,
			workspacePath,
			collectionName), nil
	}

	return "", nil
}

// AttachAIWarning appends a workspace detection warning to the result string if present.
func AttachAIWarning(result string, wsInfo *workspace.Info) string {
	if wsInfo != nil && wsInfo.AIWarning != "" {
		return fmt.Sprintf("%s\n\n⚠️  [WORKSPACE WARNING]: %s", result, wsInfo.AIWarning)
	}
	return result
}

// DetectAndRegisterWorkspace is a convenience helper for tools that want to detect the workspace
// based on params and also register it as an active workspace for future context-less fallback detection.
func DetectAndRegisterWorkspace(wm *workspace.Manager, params map[string]interface{}) (*workspace.Info, error) {
	info, err := wm.DetectWorkspace(params)
	if err == nil && info != nil {
		wm.RegisterWorkspace(info)
	}
	return info, err
}

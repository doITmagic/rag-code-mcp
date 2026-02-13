package tools

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
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
			return fmt.Sprintf("❌ Workspace '%s' is not indexed yet.\n\n"+
				"Please call 'index_workspace' first with {\"file_path\": \"%s\"}.\n"+
				"Collection: %s (not created yet)",
				workspacePath, workspacePath, collectionName), nil
		}
	}

	return "", nil
}

// CheckSearchResults verifies if search returned any results.
// Returns an error message if no results found, nil otherwise.
func CheckSearchResults(resultCount int, collectionName, workspacePath string) (string, error) {
	if resultCount == 0 {
		return fmt.Sprintf("❌ Workspace '%s' appears to be empty or not fully indexed.\n\n"+
			"Please call 'index_workspace' with {\"file_path\": \"%s\"}.\n"+
			"Collection: %s (exists but may be empty)",
			workspacePath, workspacePath, collectionName), nil
	}

	return "", nil
}

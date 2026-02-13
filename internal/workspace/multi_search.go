package workspace

import (
	"context"
	"fmt"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// GetAllIndexedMemories returns all indexed workspace memories
// This is useful for searching across all workspaces when no specific file_path is provided
func (m *Manager) GetAllIndexedMemories() []memory.LongTermMemory {
	m.memoryMu.RLock()
	defer m.memoryMu.RUnlock()

	memories := make([]memory.LongTermMemory, 0, len(m.memories))
	for _, mem := range m.memories {
		memories = append(memories, mem)
	}

	return memories
}

// GetAllIndexedCollectionNames returns the names of all indexed collections
func (m *Manager) GetAllIndexedCollectionNames() []string {
	m.memoryMu.RLock()
	defer m.memoryMu.RUnlock()

	names := make([]string, 0, len(m.memories))
	for name := range m.memories {
		names = append(names, name)
	}

	return names
}

// SearchAllWorkspaces searches across all indexed workspace collections
// Returns aggregated results from all workspaces
func (m *Manager) SearchAllWorkspaces(ctx context.Context, queryEmbedding []float64, limit int) ([]memory.Document, error) {
	m.memoryMu.RLock()
	allMemories := make(map[string]memory.LongTermMemory, len(m.memories))
	for name, mem := range m.memories {
		allMemories[name] = mem
	}
	m.memoryMu.RUnlock()

	if len(allMemories) == 0 {
		return nil, fmt.Errorf("no indexed workspaces available")
	}

	// Search in each workspace and collect results
	allDocs := make([]memory.Document, 0)
	for collectionName, mem := range allMemories {
		docs, err := mem.Search(ctx, queryEmbedding, limit)
		if err != nil {
			// Log error but continue with other workspaces
			continue
		}

		// Tag documents with their collection name for transparency
		for i := range docs {
			if docs[i].Metadata == nil {
				docs[i].Metadata = make(map[string]interface{})
			}
			docs[i].Metadata["collection"] = collectionName
		}

		allDocs = append(allDocs, docs...)
	}

	if len(allDocs) == 0 {
		return nil, fmt.Errorf("no results found in any indexed workspace")
	}

	// Sort by score and limit results
	// TODO: Implement proper score-based sorting if needed
	if len(allDocs) > limit {
		allDocs = allDocs[:limit]
	}

	return allDocs, nil
}

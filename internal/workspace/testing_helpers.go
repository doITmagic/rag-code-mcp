package workspace

import (
	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// NewManagerForTest creates a Manager with a pre-configured cache for testing.
// This avoids needing real Qdrant, LLM, or config dependencies in unit tests.
func NewManagerForTest(cache *Cache) *Manager {
	return &Manager{
		cache:    cache,
		memories: make(map[string]memory.LongTermMemory),
	}
}

package workspace

import (
	"path/filepath"

	"github.com/doITmagic/rag-code-mcp/internal/memory"
)

// NewManagerForTest creates a Manager with a pre-configured cache and registry for testing.
// This avoids needing real Qdrant, LLM, or config dependencies in unit tests.
func NewManagerForTest(cache *Cache, registryDir string) *Manager {
	regPath := ""
	if registryDir != "" {
		regPath = filepath.Join(registryDir, "workspaces.json")
	}
	return &Manager{
		cache:    cache,
		registry: NewRegistry(regPath),
		memories: make(map[string]memory.LongTermMemory),
	}
}

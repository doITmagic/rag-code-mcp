package index

import (
	"path/filepath"

	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

// Dir is where root's index files live: the directory of its index.db under
// the global home, so nothing is written into the project for indexing.
func Dir(root string) string {
	return filepath.Join(config.HomeDir(), "index", store.CollectionName(root))
}

package index

import (
	"path/filepath"
	"strings"

	"github.com/doITmagic/rag-code-mcp/v3/internal/config"
	"github.com/doITmagic/rag-code-mcp/v3/store"
)

// Dir is where root's index files live: the directory of its index.db under
// the global home, so nothing is written into the project for indexing.
func Dir(root string) string {
	return filepath.Join(config.HomeDir(), "index", store.CollectionName(root))
}

// OwnHome is RagCode's home (config.HomeDir), absolute and symlink-resolved.
// Nothing under it is indexed or watched: a home inside a root would index
// its own state files and re-trigger the watcher on every run.
func OwnHome() string {
	h, _ := filepath.Abs(config.HomeDir())
	if r, err := filepath.EvalSymlinks(h); err == nil {
		return r
	}
	return h
}

// InOwnHome reports whether path is home (an OwnHome result) or below it.
func InOwnHome(path, home string) bool {
	return path == home || strings.HasPrefix(path, home+string(filepath.Separator))
}

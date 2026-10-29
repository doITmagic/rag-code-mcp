package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

// With auto_index: false a search must not index, nor register as confirmed,
// a workspace nobody asked to index.
func TestSearchHonoursAutoIndexDisabled(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\nfunc Work() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &testStore{existing: map[string]bool{}}
	eng := NewEngine(indexer.NewService(&mockLLM{}, store), search.NewService(&mockLLM{}, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
	ctx := context.Background()
	_, _ = eng.HybridSearchCode(ctx, path, "work", 5, false)
	_, _ = eng.SearchCode(ctx, path, "work", 5, false)
	eng.Close()
	if s := indexer.LoadIndexStatus(root); s != nil {
		t.Fatalf("search started indexing with auto_index disabled: %+v", s)
	}
	if _, ok := eng.registry.LookupByRoot(root); ok {
		t.Fatal("search registered the workspace with auto_index disabled")
	}
}

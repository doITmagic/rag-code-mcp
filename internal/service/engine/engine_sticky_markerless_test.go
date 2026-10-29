package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/internal/transport"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
)

// After rag_index_workspace(workspace_root=dir) the adapter sends dir as the
// sticky root. A directory without project markers must stay searchable.
func TestStickyRootWithoutMarkersStaysSearchable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package sample\nfunc Work() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &testStore{existing: map[string]bool{}}
	provider := &mockLLM{}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	defer eng.Close()
	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	ctx := transport.WithWorkspaceHint(context.Background(), root)
	wctx, err := eng.DetectContext(ctx, "")
	if err != nil {
		t.Fatalf("confirmed markerless workspace cannot be resolved from the sticky root: %v", err)
	}
	if wctx.Root != root {
		t.Fatalf("sticky root resolved to %q, want %q", wctx.Root, root)
	}
	if _, err := eng.SearchCode(ctx, "", "Work", 5, false); err != nil {
		t.Fatalf("confirmed markerless workspace cannot be searched: %v", err)
	}
}

// A confirmed markerless subdirectory of a project must not resolve to the
// project root that marker detection finds above it.
func TestStickyRootInsideMarkedParentStaysOnConfirmedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sub")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "go.mod"), []byte("module sample\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package sample\nfunc Work() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &testStore{existing: map[string]bool{}}
	provider := &mockLLM{}
	eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
	defer eng.Close()
	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	// Another client's workspace becomes the last active one.
	if err := eng.IndexWorkspace(context.Background(), t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContext(transport.WithWorkspaceHint(context.Background(), root), "")
	if err != nil {
		t.Fatal(err)
	}
	if wctx.Root != root {
		t.Fatalf("sticky root resolved to %q, want the confirmed root %q", wctx.Root, root)
	}
}

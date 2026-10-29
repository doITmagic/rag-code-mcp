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

// A fully indexed workspace with no supported code language (docs only, or
// Rust and C kept as generic text) has no Go collection: searching one made
// every call start another index run and answer indexing_in_progress.
func TestSearchOfWorkspaceWithoutCodeLanguageDoesNotReindex(t *testing.T) {
	for name, files := range map[string][]string{"docs": {"README.md"}, "generic": {"main.rs"}} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range files {
				if err := os.WriteFile(filepath.Join(root, file), []byte("Notes about the parser.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			store := &indexOrderStore{testStore: testStore{existing: map[string]bool{}}}
			cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true}}
			eng := NewEngine(indexer.NewService(&mockLLM{}, store), search.NewService(&mockLLM{}, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
			eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
			t.Cleanup(eng.Close)
			ctx := context.Background()
			wctx, err := eng.DetectContextAsRoot(ctx, root, false)
			if err != nil {
				t.Fatal(err)
			}
			// Connected and auto-indexed already, as after the daemon's first call.
			eng.connectTriggered.Store(wctx.ID, true)
			eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, false)
			waitFor(t, "index did not finish", func() bool {
				s := indexer.LoadIndexStatus(root)
				return len(eng.ActiveIndexingJobs()) == 0 && s != nil && s.EndedAt != ""
			})
			// The search names the workspace directory, not a file.
			path := root
			_, _ = eng.HybridSearchCode(ctx, path, "parser", 5, false)
			_, _ = eng.SearchCode(ctx, path, "parser", 5, false)
			if jobs := eng.ActiveIndexingJobs(); len(jobs) != 0 {
				t.Fatalf("search of an indexed workspace started indexing (collections %v, status %+v)", store.created, indexer.LoadIndexStatus(root))
			}
		})
	}
}

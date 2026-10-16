package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type recoveryStore struct {
	indexOrderStore
	target    string
	recovered chan struct{}
	once      sync.Once
	edited    chan struct{}
	editOnce  sync.Once
}

func (s *recoveryStore) Upsert(ctx context.Context, collection string, points []storage.Point) (*storage.UpdateResult, error) {
	result, err := s.indexOrderStore.Upsert(ctx, collection, points)
	for _, point := range points {
		if point.Payload["file_path"] == s.target {
			s.once.Do(func() { close(s.recovered) })
			if content, _ := point.Payload["content"].(string); strings.Contains(content, "Edited after import") {
				s.editOnce.Do(func() { close(s.edited) })
			}
		}
	}
	return result, err
}

func TestWatcherRecoversFilesCreatedDuringInitialIndex(t *testing.T) {
	for _, tc := range []struct {
		name      string
		async     bool
		directory bool
	}{
		{"synchronous-file", false, false},
		{"background-file", true, false},
		{"synchronous-directory", false, true},
		{"background-directory", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range map[string]string{
				"main.go":   "package main\nfunc Work() {}\n",
				"README.md": "# Existing\nExisting documentation.\n",
			} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			store := &recoveryStore{
				indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
				target:          filepath.Join(root, "late.md"),
				recovered:       make(chan struct{}),
				edited:          make(chan struct{}),
			}
			var staging string
			if tc.directory {
				staging = t.TempDir()
				if err := os.Mkdir(filepath.Join(staging, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(staging, "nested", "late.md"), []byte("# Imported\nSearchable imported document.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				store.target = filepath.Join(root, "newdocs", "nested", "late.md")
			}
			provider := &mockLLM{}
			cfg := &config.Config{Workspace: config.WorkspaceConfig{
				AutoIndex: true, WatchEnabled: true, WatchDebounce: 10 * time.Millisecond,
			}}
			eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
			eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}, Registry: eng.registry}))
			t.Cleanup(eng.Close)
			wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
			if err != nil {
				t.Fatal(err)
			}

			goBlocked := make(chan error, 1)
			releaseGo := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseGo) }) }
			t.Cleanup(release)
			store.onGo = func() {
				if tc.directory {
					goBlocked <- os.Rename(staging, filepath.Join(root, "newdocs"))
				} else {
					goBlocked <- os.WriteFile(store.target, []byte("# Created during indexing\nSearchable new document.\n"), 0o600)
				}
				<-releaseGo
			}
			done := make(chan error, 1)
			if tc.async {
				eng.StartIndexingAsync(root, wctx.ID, nil, false)
			} else {
				go func() { done <- eng.IndexWorkspace(context.Background(), root, false) }()
			}
			select {
			case err := <-goBlocked:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("Go indexing did not start")
			}

			// Let the real watcher deliver its debounced event while Go is
			// blocked. A follow-up must not write collection state concurrently.
			select {
			case <-store.recovered:
				t.Fatal("watcher indexed the new file concurrently with the full run")
			case <-time.After(100 * time.Millisecond):
			}
			release()
			if !tc.async {
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("synchronous indexing did not finish")
				}
			}
			select {
			case <-store.recovered:
			case <-time.After(3 * time.Second):
				t.Fatal("file created during initial indexing was not recovered automatically")
			}
			if tc.directory {
				if err := os.WriteFile(store.target, []byte("# Imported\nEdited after import: the subtree must stay watched.\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				select {
				case <-store.edited:
				case <-time.After(3 * time.Second):
					t.Fatal("nested directory imported during indexing was not watched")
				}
			}
		})
	}
}

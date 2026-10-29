package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/watch"
)

type branchIndexStore struct {
	indexOrderStore
	names map[string][]string
}

func (s *branchIndexStore) Upsert(ctx context.Context, collection string, points []storage.Point) (*storage.UpdateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, point := range points {
		name, _ := point.Payload["name"].(string)
		s.names[collection] = append(s.names[collection], name)
	}
	return nil, nil
}

func TestGitBranchSwitchKeepsWatcherWritesInCurrentCollection(t *testing.T) {
	for _, viaWatch := range []bool{false, true} {
		name := "IndexFiles"
		if viaWatch {
			name = "watchCallback"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, output)
				}
			}
			file := filepath.Join(root, "main.go")
			write := func(content string) {
				t.Helper()
				if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			git("init", "-q", "-b", "baseline")
			write("package main\nfunc BaselineOnly() {}\n")
			git("add", "main.go")
			git("commit", "-qm", "baseline")
			git("switch", "-qc", "variant")
			write("package main\nfunc VariantOnly() {}\n")
			git("commit", "-qam", "variant")
			git("switch", "-q", "baseline")

			store := &branchIndexStore{
				indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
				names:           map[string][]string{},
			}
			provider := &mockLLM{}
			eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), &config.Config{})
			t.Cleanup(eng.Close)
			ctx := context.Background()
			if err := eng.IndexWorkspace(ctx, root, false); err != nil {
				t.Fatal(err)
			}
			for _, branch := range []string{"baseline", "variant", "baseline", "variant"} {
				git("switch", "-q", branch)
				if viaWatch {
					if err := eng.handleWatchChange(ctx, root, []string{file}); err != nil {
						t.Fatal(err)
					}
					waitFor(t, "branch indexing did not complete", func() bool { return len(eng.ActiveIndexingJobs()) == 0 })
				} else if err := eng.IndexFiles(ctx, root, []string{file}); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					wctx, err := eng.DetectContext(ctx, root)
					if err != nil {
						t.Fatal(err)
					}
					if wctx.Branch != branch {
						t.Fatalf("after switching to %s: detected %s", branch, wctx.Branch)
					}
				}
			}
			for branch, expected := range map[string]string{"baseline": "BaselineOnly", "variant": "VariantOnly"} {
				collection := CollectionNameFor(contract.DeriveWorkspaceID(root, branch, root), "go")
				if len(store.names[collection]) == 0 {
					t.Fatalf("no symbols indexed for %s", branch)
				}
				for _, symbol := range store.names[collection] {
					if symbol != expected {
						t.Fatalf("%s collection received symbol %s, want only %s", branch, symbol, expected)
					}
				}
			}
		})
	}
}

func TestWatchedWorkspaceReindexesWhenGitStateChanges(t *testing.T) {
	eng, root := newUnwatchedTestEngine(t)
	eng.watchers.SetFactory(func(string, watch.Options, watch.OnChangeFunc) (watch.Watcher, error) {
		return noopWatcher{}, nil
	})
	if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
		t.Fatal(err)
	}
	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	eng.connectTriggered.Store(wctx.ID, true)
	eng.lastIndexed.Delete(wctx.ID)
	wctx.ReindexRequired = true
	eng.autoTriggerIndex(wctx)
	waitFor(t, "watched branch change did not trigger indexing", func() bool {
		_, completed := eng.lastIndexed.Load(wctx.ID)
		return completed
	})
}

// roundTripLLM runs onEmbed, once set, the first time it embeds the symbol.
type roundTripLLM struct {
	mockLLM
	once    sync.Once
	symbol  string
	onEmbed func()
}

func (p *roundTripLLM) Embed(ctx context.Context, text string) ([]float64, error) {
	if p.onEmbed != nil && strings.Contains(text, p.symbol) {
		p.once.Do(p.onEmbed)
	}
	return p.mockLLM.Embed(ctx, text)
}

func (s *branchIndexStore) DeleteCollection(_ context.Context, collection string) error {
	delete(s.existing, collection)
	return nil
}

// A checkout that interrupts a run leaves the collection partial: it must be
// completed without waiting for a request, and the watcher batch the checkout
// produced must not stand in for the files the interrupted run never reached.
func TestInterruptedIndexIsCompletedWithoutRequest(t *testing.T) {
	for _, autoTriggered := range []bool{false, true} {
		name := "StartIndexingAsync"
		if autoTriggered {
			name = "autoTriggerIndex"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Errorf("git %v: %v: %s", args, err, output)
				}
			}
			write := func(name, symbol string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte("package main\nfunc "+symbol+"() {}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			git("init", "-q", "-b", "baseline")
			write("a.go", "StableFirst")
			write("m.go", "MiddleSecond")
			write("z.go", "BaselineThird")
			git("add", ".")
			git("commit", "-qm", "baseline")
			git("switch", "-qc", "variant")
			write("z.go", "VariantThird")
			git("commit", "-qam", "variant")
			git("switch", "-q", "baseline")

			store := &branchIndexStore{
				indexOrderStore: indexOrderStore{testStore: testStore{existing: map[string]bool{}}},
				names:           map[string][]string{},
			}
			provider := &roundTripLLM{}
			cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: autoTriggered}}
			eng := NewEngine(indexer.NewService(provider, store), search.NewService(provider, store), filepath.Join(t.TempDir(), "registry.json"), cfg)
			t.Cleanup(eng.Close)
			if !autoTriggered {
				if err := eng.IndexWorkspace(context.Background(), root, false); err != nil {
					t.Fatal(err)
				}
			}
			wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
			if err != nil {
				t.Fatal(err)
			}
			collection := wctx.CollectionName("go")
			store.mu.Lock()
			store.names[collection] = nil
			store.mu.Unlock()
			// Interrupt the run in m.go, after a.go is saved; z.go differs between the branches.
			provider.symbol = "MiddleSecond"
			provider.onEmbed = func() {
				git("switch", "-q", "variant")
				git("switch", "-q", "baseline")
				if err := eng.handleWatchChange(context.Background(), root, []string{filepath.Join(root, "z.go")}); err != nil {
					t.Error(err)
				}
			}
			if autoTriggered {
				// The first index a tool call starts on a new workspace.
				wctx.ReindexRequired = false
				eng.autoTriggerIndex(wctx)
			} else {
				// A recreate starts from an empty collection, like rag_index_workspace recreate=true.
				eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, true)
			}
			waitFor(t, "interrupted index was not completed", func() bool {
				store.mu.Lock()
				names := strings.Join(store.names[collection], ",")
				store.mu.Unlock()
				return len(eng.ActiveIndexingJobs()) == 0 && strings.Contains(names, "StableFirst") &&
					strings.Contains(names, "MiddleSecond") && strings.Contains(names, "BaselineThird")
			})
			if s := indexer.LoadIndexStatus(root); s == nil || s.Error != "" || s.EndedAt == "" {
				t.Fatalf("completed index reports %+v", s)
			}
		})
	}
}

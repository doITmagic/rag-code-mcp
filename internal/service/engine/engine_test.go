package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

type batchTrackingLLM struct {
	mockLLM
	batchCalls atomic.Int32
}

func (b *batchTrackingLLM) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	b.batchCalls.Add(1)
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = []float64{0.1}
	}
	return out, nil
}

func TestNewEngineConfiguresEmbedBatchSize(t *testing.T) {
	provider := &batchTrackingLLM{}
	store := &testStore{existing: map[string]bool{}}
	idxSvc := indexer.NewService(provider, store)
	searchSvc := search.NewService(provider, store)
	cfg := &config.Config{
		LLM: config.LLMConfig{
			EmbedBatchSize: 4,
			EmbedWorkers:   2,
		},
	}
	_ = NewEngine(idxSvc, searchSvc, "", cfg)

	symbols := make([]parser.Symbol, 8)
	for i := range symbols {
		symbols[i] = parser.Symbol{ID: fmt.Sprintf("sym-%d", i), Name: fmt.Sprintf("Sym%d", i)}
	}
	if err := idxSvc.IndexItems(context.Background(), "col", symbols); err != nil {
		t.Fatalf("IndexItems failed: %v", err)
	}
	if got := provider.batchCalls.Load(); got != 2 {
		t.Fatalf("expected 2 batch calls for 8 symbols with EmbedBatchSize=4, got %d", got)
	}
}

func TestEngineCloseCancelsLifecycleContext(t *testing.T) {
	store := &testStore{existing: map[string]bool{}}
	eng := newTestEngine(store)

	ctx := eng.bgContext()
	select {
	case <-ctx.Done():
		t.Fatal("expected bgContext to be active before Close()")
	default:
	}

	eng.Close()

	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected bgContext to be cancelled after Close()")
	}

	// Zero-value Engine fallback check
	var zeroEng Engine
	if zeroEng.bgContext() == nil {
		t.Fatal("expected non-nil context from zero-value Engine.bgContext()")
	}
}

type countingCollectionStore struct {
	testStore
	mu          sync.Mutex
	existsCount map[string]int
	deleteCount int
	doneCh      chan struct{}
	targetOps   int
}

func (s *countingCollectionStore) CollectionExists(_ context.Context, name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.existsCount[name]++
	return s.existing[name], nil
}

func (s *countingCollectionStore) DeleteByFilter(_ context.Context, _ string, _ string, _ interface{}) error {
	s.mu.Lock()
	s.deleteCount++
	done := s.deleteCount == s.targetOps
	s.mu.Unlock()
	if done && s.doneCh != nil {
		close(s.doneCh)
	}
	return nil
}

func TestCleanupStaleFilesCachesCollectionExistsAndBoundsCache(t *testing.T) {
	// Clear package-level cache before test
	staleCleanupCache.Range(func(k, _ any) bool {
		staleCleanupCache.Delete(k)
		return true
	})
	staleCleanupCacheCount.Store(0)

	doneCh := make(chan struct{})
	store := &countingCollectionStore{
		testStore: testStore{
			existing: map[string]bool{
				"ragcode-ws-clean-go": true,
			},
		},
		existsCount: make(map[string]int),
		doneCh:      doneCh,
		targetOps:   3,
	}

	llmProvider := &mockLLM{}
	idxSvc := indexer.NewService(llmProvider, store)
	searchSvc := search.NewService(llmProvider, store)
	eng := NewEngine(idxSvc, searchSvc, "", &config.Config{})

	staleFiles := []string{"/ws/a.go", "/ws/b.go", "/ws/c.go"}
	eng.CleanupStaleFiles("ws-clean", staleFiles)

	select {
	case <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for CleanupStaleFiles goroutine")
	}

	store.mu.Lock()
	goExistsCalls := store.existsCount["ragcode-ws-clean-go"]
	store.mu.Unlock()

	if goExistsCalls != 1 {
		t.Fatalf("expected CollectionExists for ragcode-ws-clean-go to be called once across %d files, got %d", len(staleFiles), goExistsCalls)
	}

	// Verify cache size bound reset when exceeding staleCleanupCacheMax
	staleCleanupCacheCount.Store(staleCleanupCacheMax)
	eng.CleanupStaleFiles("ws-bound", []string{"/ws/new.go"})
	if got := staleCleanupCacheCount.Load(); got != 1 {
		t.Fatalf("expected staleCleanupCacheCount to reset and equal 1, got %d", got)
	}
}

func TestIndexFilesSavesLoadedStatesBeforeWorkspaceFallback(t *testing.T) {
	root := t.TempDir()
	goFile := filepath.Join(root, "main.go")
	pyFile := filepath.Join(root, "script.py")
	if err := os.WriteFile(goFile, []byte("package main\nfunc Main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pyFile, []byte("def hello():\n    pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := &testStore{
		existing: map[string]bool{},
	}
	llmProvider := &mockLLM{}
	idxSvc := indexer.NewService(llmProvider, store)
	searchSvc := search.NewService(llmProvider, store)
	eng := NewEngine(idxSvc, searchSvc, "", &config.Config{})
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &exactRootDetector{}}))

	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatal(err)
	}

	goColl := wctx.CollectionName("go")
	store.existing[goColl] = true
	// Pre-create an empty state file for goColl so IndexFiles processes goFile first,
	// then hits missing python collection and falls back to IndexWorkspace.
	initialState := indexer.NewState()
	if err := initialState.Save(indexer.StatePath(wctx.Root, goColl)); err != nil {
		t.Fatal(err)
	}

	if err := eng.IndexFiles(context.Background(), root, []string{goFile, pyFile}); err != nil {
		t.Fatalf("IndexFiles failed: %v", err)
	}

	loadedState, err := indexer.LoadState(indexer.StatePath(wctx.Root, goColl))
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if _, ok := loadedState.GetFileState(goFile); !ok {
		t.Fatalf("expected %s to be recorded in saved state for %s", goFile, goColl)
	}
}

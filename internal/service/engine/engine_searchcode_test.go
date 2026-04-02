package engine

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"

	// Register parsers so parser.SupportedLanguages() returns non-empty.
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// ─── Mocks ─────────────────────────────────────────────────────────────────────

// countingLLM tracks how many times Embed() is called.
// Used to verify Phase 2: embed-once contract.
type countingLLM struct {
	calls int32
}

func (c *countingLLM) Embed(_ context.Context, _ string) ([]float64, error) {
	atomic.AddInt32(&c.calls, 1)
	return []float64{0.1, 0.2}, nil
}
func (c *countingLLM) Generate(_ context.Context, _ string, _ ...llm.GenerateOption) (string, error) {
	return "", nil
}
func (c *countingLLM) GenerateStream(_ context.Context, _ string, _ ...llm.GenerateOption) (<-chan string, <-chan error) {
	return nil, nil
}
func (c *countingLLM) Name() string                  { return "counting-mock" }
func (c *countingLLM) GetEmbeddingDimension() uint64 { return 2 }

// multiLangStore accepts any collection as existing and tracks SearchCodeOnly call count.
// It returns results only for go and python collections to simulate multi-lang fanout.
type multiLangStore struct {
	testStore
	codeOnlyCalls int32
	// resultsByLang maps last segment of collection name (e.g. "go","python") to results
	resultsByLang map[string][]storage.SearchResult
}

func (s *multiLangStore) CollectionExists(_ context.Context, name string) (bool, error) {
	// Existing is controlled by testStore.existing
	return s.testStore.existing[name], nil
}

func (s *multiLangStore) SearchCodeOnly(_ context.Context, coll string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
	atomic.AddInt32(&s.codeOnlyCalls, 1)
	// Match last segment of collection name (e.g. "ragcode-abc123-go" → "go")
	for lang, res := range s.resultsByLang {
		if len(coll) >= len(lang) && coll[len(coll)-len(lang):] == lang {
			return res, nil
		}
	}
	return nil, nil
}

func newEngineCountingLLM(store storage.VectorStore, llm llm.Provider) *Engine {
	idxSvc := indexer.NewService(llm, store)
	searchSvc := search.NewService(llm, store)
	eng := NewEngine(idxSvc, searchSvc, "", &config.Config{})
	res := resolver.New(resolver.Dependencies{Detector: &mockDetector{}})
	eng.SetResolver(res)
	return eng
}

// ─── Tests ─────────────────────────────────────────────────────────────────────

// TestSearchCodeEmbedsOnce verifies that SearchCode calls the embedder exactly once
// even when multiple language collections (go + python) exist and are searched in parallel.
func TestSearchCodeEmbedsOnce(t *testing.T) {
	llmProvider := &countingLLM{}

	// We need to know what collection names will be generated.
	// Since mockDetector returns root "/mock/ws", we first resolve to get the ID.
	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}
	wsID := wctx.ID

	goColl := CollectionNameFor(wsID, "go")
	pyColl := CollectionNameFor(wsID, "python")

	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{
				goColl: true,
				pyColl: true,
			},
		},
		resultsByLang: map[string][]storage.SearchResult{
			"go": {
				{Score: 0.9, Point: storage.Point{ID: "go-sym", Payload: map[string]interface{}{"name": "GoFunc"}}},
			},
			"python": {
				{Score: 0.8, Point: storage.Point{ID: "py-sym", Payload: map[string]interface{}{"name": "py_func"}}},
			},
		},
	}

	// Reset after DetectContext (which doesn't embed)
	atomic.StoreInt32(&llmProvider.calls, 0)
	eng2 := newEngineCountingLLM(store, llmProvider)
	// Re-use same resolver for same wsID
	res := resolver.New(resolver.Dependencies{Detector: &mockDetector{}})
	eng2.SetResolver(res)

	result, err := eng2.SearchCode(context.Background(), "test.go", "find something", 10, false)
	if err != nil {
		t.Fatalf("SearchCode failed: %v", err)
	}

	embedCalls := atomic.LoadInt32(&llmProvider.calls)
	if embedCalls != 1 {
		t.Errorf("expected embedder called exactly once, got %d calls", embedCalls)
	}

	codeOnlyCalls := atomic.LoadInt32(&store.codeOnlyCalls)
	if codeOnlyCalls < 2 {
		t.Errorf("expected SearchCodeOnly called at least twice (go + python), got %d", codeOnlyCalls)
	}

	_ = result
}

// TestSearchCodePopulatesWorkspaceID verifies that SearchCodeResult.WorkspaceID is set.
func TestSearchCodePopulatesWorkspaceID(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}
	wsID := wctx.ID
	goColl := CollectionNameFor(wsID, "go")

	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{goColl: true},
		},
		resultsByLang: map[string][]storage.SearchResult{
			"go": {
				{Score: 0.9, Point: storage.Point{ID: "x", Payload: map[string]interface{}{"name": "fn"}}},
			},
		},
	}

	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	result, err := eng2.SearchCode(context.Background(), "test.go", "query", 5, false)
	if err != nil {
		t.Fatalf("SearchCode failed: %v", err)
	}
	if result.WorkspaceID == "" {
		t.Error("expected WorkspaceID to be set in SearchCodeResult, got empty string")
	}
	if result.WorkspaceID != wsID {
		t.Errorf("expected WorkspaceID=%q, got %q", wsID, result.WorkspaceID)
	}
}

// TestSearchCodeMergesMultiLangResults verifies that results from go and python
// collections are both present in the merged output.
func TestSearchCodeMergesMultiLangResults(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext: %v", err)
	}
	goColl := CollectionNameFor(wctx.ID, "go")
	pyColl := CollectionNameFor(wctx.ID, "python")

	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{goColl: true, pyColl: true},
		},
		resultsByLang: map[string][]storage.SearchResult{
			"go":     {{Score: 0.9, Point: storage.Point{ID: "go-result"}}},
			"python": {{Score: 0.7, Point: storage.Point{ID: "py-result"}}},
		},
	}

	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	result, err := eng2.SearchCode(context.Background(), "test.go", "something", 10, false)
	if err != nil {
		t.Fatalf("SearchCode: %v", err)
	}
	if len(result.Results) < 2 {
		t.Fatalf("expected at least 2 merged results (go+python), got %d", len(result.Results))
	}

	ids := map[string]bool{}
	for _, r := range result.Results {
		ids[r.Point.ID] = true
	}
	if !ids["go-result"] {
		t.Error("go-result missing from merged results")
	}
	if !ids["py-result"] {
		t.Error("py-result missing from merged results")
	}
}

// TestSearchCodeSurfacesErrorWhenAllFail verifies that if all language collections
// fail to search (and return zero results), the error is propagated.
func TestSearchCodeSurfacesErrorWhenAllFail(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext: %v", err)
	}
	goColl := CollectionNameFor(wctx.ID, "go")

	// Only go collection exists, but it always returns an error
	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{goColl: true},
		},
	}
	_ = store // store not used directly; errorStore replaces it below

	// We need a different approach — use errorStore
	errorStore := &errorSearchCodeOnlyStore{
		testStore: testStore{existing: map[string]bool{goColl: true}},
	}

	eng3 := newEngineCountingLLM(errorStore, llmProvider)
	eng3.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	_, err = eng3.SearchCode(context.Background(), "test.go", "query", 5, false)
	if err == nil {
		t.Error("expected error when all collections fail, got nil")
	}
}

// errorSearchCodeOnlyStore always returns an error from SearchCodeOnly.
type errorSearchCodeOnlyStore struct {
	testStore
}

func (s *errorSearchCodeOnlyStore) SearchCodeOnly(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
	return nil, errSearchFailed
}

var errSearchFailed = &searchError{"simulated storage failure"}

type searchError struct{ msg string }

func (e *searchError) Error() string { return e.msg }

func TestSearchCodeResumeInterruptedIndexing(t *testing.T) {
	llmProvider := &countingLLM{}
	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)

	// Mock resolver configures a fake detector returning a tmp root
	rootDir := t.TempDir()
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDirDetector{root: rootDir}}))

	// Get workspace ID early
	wctx, _ := eng.DetectContext(context.Background(), "dummy.go")
	if wctx == nil {
		t.Fatalf("Failed to detect context")
	}

	// Wait for background goroutine (triggered by connectTriggered in DetectContext)
	// to finish before TempDir cleanup removes the directory.
	t.Cleanup(func() {
		for i := 0; i < 200; i++ {
			if len(eng.ActiveIndexingJobs()) == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		os.RemoveAll(filepath.Join(rootDir, ".ragcode"))
	})

	// Make the collection exist so search continues
	goColl := CollectionNameFor(wctx.ID, "go")
	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{goColl: true},
		},
		resultsByLang: map[string][]storage.SearchResult{
			"go": {{Score: 0.5, Point: storage.Point{ID: "resume-hit", Payload: map[string]interface{}{"name": "Fn"}}}},
		},
	}
	eng.SetSearchService(search.NewService(llmProvider, store))

	// Verify SearchCode succeeds (returns results) when collection exists.
	_, err := eng.SearchCode(context.Background(), "dummy.go", "test", 10, false)
	if err != nil {
		t.Fatalf("Expected no error when collection exists, got: %v", err)
	}
}

// TestStartIndexingAsyncRecreateQueuesWhenJobRunning verifies BUG-004 fix:
// when recreate=true is requested while a job is already running, the recreate
// must be queued as pendingOverflow (full re-index) rather than silently dropped.
func TestStartIndexingAsyncRecreateQueuesWhenJobRunning(t *testing.T) {
	llmProvider := &countingLLM{}
	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)

	const wsID = "test-ws-id"
	const wsRoot = "/tmp/fake-ws"

	// Simulate a job already running for this workspace.
	eng.indexingJobs.Store(wsID, time.Now())

	// Request recreate=true while the job is running.
	eng.StartIndexingAsync(wsRoot, wsID, nil, true)

	// The job is still marked as running (we put it there).
	_, stillRunning := eng.indexingJobs.Load(wsID)
	if !stillRunning {
		t.Fatal("Expected job to still be running (we stored it manually)")
	}

	// The recreate MUST be queued as overflow — not silently dropped.
	eng.pendingMu.Lock()
	overflow := eng.pendingOverflow[wsID]
	_, hasPendingFiles := eng.pendingFiles[wsID]
	eng.pendingMu.Unlock()

	if !overflow {
		t.Error("Expected pendingOverflow[wsID]=true when recreate=true is requested while job runs")
	}
	if hasPendingFiles {
		t.Error("Expected pendingFiles[wsID] to be cleared when overflow is set")
	}

	// Cleanup: remove the fake job
	eng.indexingJobs.Delete(wsID)
}

// TestStartIndexingAsyncRecreateStartsImmediatelyWhenNoJobRunning verifies that
// when recreate=true and no job is running, the job starts immediately (normal path).
func TestStartIndexingAsyncRecreateStartsImmediatelyWhenNoJobRunning(t *testing.T) {
	llmProvider := &countingLLM{}
	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)

	rootDir := t.TempDir()
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDirDetector{root: rootDir}}))

	// DetectContext triggers connectTriggered → StartIndexingAsync. Wait for it to finish.
	wctx, _ := eng.DetectContext(context.Background(), "dummy.go")
	if wctx == nil {
		t.Fatalf("Failed to detect context")
	}

	for i := 0; i < 200; i++ {
		if len(eng.ActiveIndexingJobs()) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}

	t.Cleanup(func() {
		for i := 0; i < 200; i++ {
			if len(eng.ActiveIndexingJobs()) == 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		os.RemoveAll(filepath.Join(rootDir, ".ragcode"))
	})

	// No job running — recreate=true should start immediately.
	eng.StartIndexingAsync(wctx.Root, wctx.ID, nil, true)

	_, nowRunning := eng.indexingJobs.Load(wctx.ID)
	if !nowRunning {
		t.Error("Expected job to be running immediately when recreate=true and no job was active")
	}

	// Nothing should be queued in pendingOverflow.
	eng.pendingMu.Lock()
	overflow := eng.pendingOverflow[wctx.ID]
	eng.pendingMu.Unlock()

	if overflow {
		t.Error("Expected no pendingOverflow when job started immediately")
	}
}

// mockDirDetector is like mockDetector but allows specifying the root dir
type mockDirDetector struct {
	root string
}

func (m *mockDirDetector) DetectFromFilePath(_ context.Context, path string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
	return &contract.WorkspaceCandidate{
		Root:       m.root,
		Confidence: 1.0,
	}, nil
}

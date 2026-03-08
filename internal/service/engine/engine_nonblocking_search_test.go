package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"

	// Register parsers so parser.SupportedLanguages() returns non-empty.
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// ─── Non-blocking search during indexing ─────────────────────────────────────
//
// These tests verify that SearchCode and HybridSearchCode do NOT block with
// ErrIndexingStarted / ErrIndexingInProgress when some collections exist.
// They validate the core fix: search degrades gracefully during indexing.

// TestSearchCodeReturnsResultsFromOtherLangsWhenPrimaryMissing verifies that
// when the primary language collection (e.g. "go") does not exist but another
// language collection (e.g. "python") does, SearchCode returns the python
// results instead of blocking with ErrIndexingStarted.
func TestSearchCodeReturnsResultsFromOtherLangsWhenPrimaryMissing(t *testing.T) {
	llmProvider := &countingLLM{}

	// First, detect to learn the workspace ID
	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	// Only python collection exists, NOT go
	pyColl := CollectionNameFor(wctx.ID, "python")

	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{
				pyColl: true,
				// Go collection intentionally missing
			},
		},
		resultsByLang: map[string][]storage.SearchResult{
			"python": {
				{Score: 0.85, Point: storage.Point{ID: "py-result", Payload: map[string]interface{}{"name": "py_func"}}},
			},
		},
	}

	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	result, err := eng2.SearchCode(context.Background(), "test.go", "find something", 10, false)
	if err != nil {
		t.Fatalf("SearchCode should NOT block when other collections exist, got error: %v", err)
	}
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if len(result.Results) == 0 {
		t.Fatal("Expected results from python collection, got 0")
	}

	// Verify our python result is present
	found := false
	for _, r := range result.Results {
		if r.Point.ID == "py-result" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected py-result in merged results")
	}

	// Cleanup: stop progress flusher
	t.Cleanup(func() {
		if eng2.progress != nil {
			eng2.progress.stop()
		}
	})
}

// TestSearchCodeBlocksWhenZeroCollectionsExist verifies that when no collections
// exist at all (truly un-indexed workspace), SearchCode returns ErrIndexingStarted
// to signal that indexing was triggered.
func TestSearchCodeBlocksWhenZeroCollectionsExist(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	// No collections exist at all
	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{},
		},
	}

	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	_, err = eng2.SearchCode(context.Background(), "test.go", "find something", 10, false)
	if err == nil {
		t.Fatal("Expected ErrIndexingStarted when zero collections exist, got nil")
	}

	var indexingStarted *ErrIndexingStarted
	var indexingInProgress *ErrIndexingInProgress
	var notIndexed *ErrNotIndexed
	if errors.As(err, &indexingStarted) {
		// Expected: ErrIndexingStarted
		if indexingStarted.WorkspaceRoot != wctx.Root {
			t.Errorf("Expected WorkspaceRoot=%q, got %q", wctx.Root, indexingStarted.WorkspaceRoot)
		}
	} else if errors.As(err, &indexingInProgress) {
		// Also acceptable: the bg goroutine registered before fan-out completed
	} else if errors.As(err, &notIndexed) {
		// Also acceptable: ErrNotIndexed
	} else {
		t.Fatalf("Expected ErrIndexingStarted or ErrNotIndexed, got: %T: %v", err, err)
	}

	// Cleanup
	t.Cleanup(func() {
		if eng2.progress != nil {
			eng2.progress.stop()
		}
		// Wait for bg indexing to drain
		for i := 0; i < 100; i++ {
			if len(eng2.ActiveIndexingJobs()) == 0 {
				break
			}
		}
	})
}

// TestSearchCodeIndexingInProgressStillSearches verifies that when a background
// indexing job is running AND some collections already have data, SearchCode
// returns results instead of ErrIndexingInProgress.
func TestSearchCodeIndexingInProgressStillSearches(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	goColl := CollectionNameFor(wctx.ID, "go")

	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{goColl: true},
		},
		resultsByLang: map[string][]storage.SearchResult{
			"go": {
				{Score: 0.9, Point: storage.Point{ID: "go-during-index", Payload: map[string]interface{}{"name": "GoFunc"}}},
			},
		},
	}

	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	// Simulate an active indexing job
	eng2.indexingJobs.Store(wctx.ID, "running")

	result, err := eng2.SearchCode(context.Background(), "test.go", "find something", 10, false)
	if err != nil {
		t.Fatalf("SearchCode should still search when collections have data during indexing, got: %v", err)
	}
	if result == nil || len(result.Results) == 0 {
		t.Fatal("Expected results from existing go collection during indexing")
	}

	// Cleanup the fake job
	eng2.indexingJobs.Delete(wctx.ID)
	t.Cleanup(func() {
		if eng2.progress != nil {
			eng2.progress.stop()
		}
	})
}

// TestHybridSearchCodeReturnsNilWhenCollectionMissing verifies that
// HybridSearchCode returns (nil, nil) when its collection doesn't exist,
// allowing SmartSearch to use only the semantic results.
func TestHybridSearchCodeReturnsNilWhenCollectionMissing(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	rootDir := t.TempDir()
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDirDetector{root: rootDir}}))

	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	// Collection does NOT exist
	store := &multiLangStore{
		testStore: testStore{
			existing: map[string]bool{},
		},
	}
	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDirDetector{root: rootDir}}))

	result, err := eng2.HybridSearchCode(context.Background(), "test.go", "find something", 10)

	// Should NOT return an error — should return nil, nil
	if err != nil {
		var indexingStarted *ErrIndexingStarted
		var indexingInProgress *ErrIndexingInProgress
		if errors.As(err, &indexingStarted) || errors.As(err, &indexingInProgress) {
			t.Fatalf("HybridSearchCode should NOT block with indexing errors, got: %v", err)
		}
		t.Fatalf("HybridSearchCode returned unexpected error: %v", err)
	}

	// Result should be nil (no data available)
	if result != nil {
		t.Errorf("Expected nil result when collection is missing, got: %+v", result)
	}

	_ = wctx

	// Cleanup
	t.Cleanup(func() {
		if eng2.progress != nil {
			eng2.progress.stop()
		}
		// Wait for bg indexing to drain
		for i := 0; i < 100; i++ {
			if len(eng2.ActiveIndexingJobs()) == 0 {
				break
			}
		}
	})
}

// TestHybridSearchCodeStillWorksWhenCollectionExists verifies that the normal
// happy path (collection exists) is not broken by the fix.
func TestHybridSearchCodeStillWorksWhenCollectionExists(t *testing.T) {
	llmProvider := &countingLLM{}

	eng := newEngineCountingLLM(&testStore{existing: map[string]bool{}}, llmProvider)
	wctx, err := eng.DetectContext(context.Background(), "test.go")
	if err != nil {
		t.Fatalf("DetectContext failed: %v", err)
	}

	goColl := CollectionNameFor(wctx.ID, "go")

	// hybridStore wraps multiLangStore and adds HybridSearch support
	store := &hybridSearchStore{
		multiLangStore: multiLangStore{
			testStore: testStore{
				existing: map[string]bool{goColl: true},
			},
		},
		hybridResults: []storage.SearchResult{
			{Score: 0.95, Point: storage.Point{ID: "hybrid-hit", Payload: map[string]interface{}{"name": "HybridFunc"}}},
		},
	}

	eng2 := newEngineCountingLLM(store, llmProvider)
	eng2.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDetector{}}))

	result, err := eng2.HybridSearchCode(context.Background(), "test.go", "find something", 10)
	if err != nil {
		t.Fatalf("HybridSearchCode failed: %v", err)
	}
	if result == nil {
		t.Fatal("Expected non-nil result")
	}
	if len(result.Results) == 0 {
		t.Fatal("Expected hybrid results")
	}
	if result.Results[0].Point.ID != "hybrid-hit" {
		t.Errorf("Expected hybrid-hit, got %s", result.Results[0].Point.ID)
	}

	t.Cleanup(func() {
		if eng2.progress != nil {
			eng2.progress.stop()
		}
	})
}

// hybridSearchStore extends multiLangStore with SearchCodeOnly support for HybridSearch.
// search.Service.HybridSearch delegates to SearchCodeOnly, so we override that.
type hybridSearchStore struct {
	multiLangStore
	hybridResults []storage.SearchResult
}

func (s *hybridSearchStore) SearchCodeOnly(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
	return s.hybridResults, nil
}

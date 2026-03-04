package search

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/llm"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// ─── Mocks ─────────────────────────────────────────────────────────────────────

type mockEmbedder struct {
	embedCount int32
	result     []float64
	err        error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	atomic.AddInt32(&m.embedCount, 1)
	if m.err != nil {
		return nil, m.err
	}
	if m.result != nil {
		return m.result, nil
	}
	return []float64{0.1, 0.2, 0.3}, nil
}
func (m *mockEmbedder) Generate(_ context.Context, _ string, _ ...llm.GenerateOption) (string, error) {
	return "", nil
}
func (m *mockEmbedder) GenerateStream(_ context.Context, _ string, _ ...llm.GenerateOption) (<-chan string, <-chan error) {
	return nil, nil
}
func (m *mockEmbedder) Name() string                  { return "mock" }
func (m *mockEmbedder) GetEmbeddingDimension() uint64 { return 3 }

// mockVectorStore tracks which store methods are called.
type mockVectorStore struct {
	storage.VectorStore // embed to satisfy non-overridden methods
	searchCodeOnlyCalls int
	exactSearchCalls    int
	searchCalls         int
	exactSearchResults  []storage.SearchResult
	exactSearchErr      error
	searchCodeOnlyErr   error
}

func (m *mockVectorStore) SearchCodeOnly(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
	m.searchCodeOnlyCalls++
	return nil, m.searchCodeOnlyErr
}

func (m *mockVectorStore) Search(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
	m.searchCalls++
	return nil, nil
}

func (m *mockVectorStore) ExactSearch(_ context.Context, _ string, _ map[string]interface{}, _ int) ([]storage.SearchResult, error) {
	m.exactSearchCalls++
	return m.exactSearchResults, m.exactSearchErr
}

func (m *mockVectorStore) CollectionExists(_ context.Context, _ string) (bool, error) {
	return true, nil
}

// ─── Tests ─────────────────────────────────────────────────────────────────────

// TestEmbedQueryConvertsToFloat32 ensures EmbedQuery correctly converts []float64 -> []float32.
func TestEmbedQueryConvertsToFloat32(t *testing.T) {
	emb := &mockEmbedder{result: []float64{1.0, 2.5, 3.75}}
	svc := NewService(emb, &mockVectorStore{})

	vec, err := svc.EmbedQuery(context.Background(), "test query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("expected 3 floats, got %d", len(vec))
	}
	if vec[0] != float32(1.0) || vec[1] != float32(2.5) || vec[2] != float32(3.75) {
		t.Fatalf("float conversion incorrect: %v", vec)
	}
	if atomic.LoadInt32(&emb.embedCount) != 1 {
		t.Fatalf("expected 1 embed call, got %d", emb.embedCount)
	}
}

// TestEmbedQueryPropagatesError ensures errors from the embedder bubble up.
func TestEmbedQueryPropagatesError(t *testing.T) {
	emb := &mockEmbedder{err: errors.New("ollama down")}
	svc := NewService(emb, &mockVectorStore{})

	_, err := svc.EmbedQuery(context.Background(), "query")
	if err == nil {
		t.Fatal("expected error when embedder fails")
	}
	if !errContains(err, "ollama down") {
		t.Fatalf("expected original error message in wrapped error, got: %v", err)
	}
}

// TestSearchCodeOnlyCallsEmbedExactlyOnce ensures SearchCodeOnly embeds once
// and then delegates to the store (no double-embedding).
func TestSearchCodeOnlyCallsEmbedExactlyOnce(t *testing.T) {
	emb := &mockEmbedder{}
	store := &mockVectorStore{}
	svc := NewService(emb, store)

	_, _ = svc.SearchCodeOnly(context.Background(), "col", "find function", 10)

	if atomic.LoadInt32(&emb.embedCount) != 1 {
		t.Fatalf("expected exactly 1 embed call, got %d", emb.embedCount)
	}
	if store.searchCodeOnlyCalls != 1 {
		t.Fatalf("expected 1 store.SearchCodeOnly call, got %d", store.searchCodeOnlyCalls)
	}
}

// TestSearchCodeWithVectorSkipsEmbedder verifies that SearchCodeWithVector
// does NOT call the embedder at all -- it uses the pre-computed vector directly.
func TestSearchCodeWithVectorSkipsEmbedder(t *testing.T) {
	emb := &mockEmbedder{}
	store := &mockVectorStore{}
	svc := NewService(emb, store)

	precomputed := []float32{0.1, 0.2, 0.3}
	_, _ = svc.SearchCodeWithVector(context.Background(), "col", precomputed, 5)

	if atomic.LoadInt32(&emb.embedCount) != 0 {
		t.Fatalf("SearchCodeWithVector must NOT call embedder, got %d calls", emb.embedCount)
	}
	if store.searchCodeOnlyCalls != 1 {
		t.Fatalf("expected 1 store.SearchCodeOnly call, got %d", store.searchCodeOnlyCalls)
	}
}

// TestExactSearchDelegatesToStoreExactSearch is the core correctness test:
// ExactSearch must call store.ExactSearch (Scroll path) and must NOT call
// store.Search (which would use a dummy vector and traverse HNSW).
func TestExactSearchDelegatesToStoreExactSearch(t *testing.T) {
	emb := &mockEmbedder{}
	expected := []storage.SearchResult{
		{Score: 1.0, Point: storage.Point{ID: "x1", Payload: map[string]interface{}{"name": "MyFunc"}}},
	}
	store := &mockVectorStore{exactSearchResults: expected}
	svc := NewService(emb, store)

	results, err := svc.ExactSearch(context.Background(), "col", map[string]interface{}{"name": "MyFunc"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Embedder must NOT be called at all
	if atomic.LoadInt32(&emb.embedCount) != 0 {
		t.Fatalf("ExactSearch must NOT call embedder, got %d calls", emb.embedCount)
	}
	// store.Search (HNSW path) must NOT be called
	if store.searchCalls != 0 {
		t.Fatalf("ExactSearch must NOT call store.Search, got %d calls", store.searchCalls)
	}
	// store.ExactSearch (Scroll path) must be called
	if store.exactSearchCalls != 1 {
		t.Fatalf("ExactSearch must call store.ExactSearch once, got %d calls", store.exactSearchCalls)
	}
	if len(results) != 1 || results[0].Point.ID != "x1" {
		t.Fatalf("unexpected results: %v", results)
	}
}

// TestExactSearchPropagatesStoreError verifies errors from the store bubble up.
func TestExactSearchPropagatesStoreError(t *testing.T) {
	store := &mockVectorStore{exactSearchErr: errors.New("qdrant scroll failed")}
	svc := NewService(&mockEmbedder{}, store)

	_, err := svc.ExactSearch(context.Background(), "col", map[string]interface{}{"name": "X"}, 5)
	if err == nil {
		t.Fatal("expected error when store returns error")
	}
	if !errContains(err, "qdrant scroll failed") {
		t.Fatalf("expected store error wrapped, got: %v", err)
	}
}

func errContains(err error, sub string) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

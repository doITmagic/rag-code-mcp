package engine

import (
"context"
"errors"
"testing"

"github.com/doITmagic/rag-code-mcp/internal/config"
"github.com/doITmagic/rag-code-mcp/internal/service/search"
"github.com/doITmagic/rag-code-mcp/pkg/indexer"
"github.com/doITmagic/rag-code-mcp/pkg/llm"
"github.com/doITmagic/rag-code-mcp/pkg/storage"
"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

// ─── Mocks ─────────────────────────────────────────────────────────────────────

// testStore is a controlled mock of storage.VectorStore for engine-level tests.
// Only the methods used by ExactSearchPolyglot/SearchByName are implemented.
type testStore struct {
storage.VectorStore
existing         map[string]bool
exactSearchFunc  func(coll string, filters map[string]interface{}) ([]storage.SearchResult, error)
recordedFilters  []capturedCall
exactSearchErr   error
}

type capturedCall struct {
collection string
filters    map[string]interface{}
}

func (s *testStore) CollectionExists(_ context.Context, name string) (bool, error) {
return s.existing[name], nil
}

func (s *testStore) ExactSearch(_ context.Context, coll string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
s.recordedFilters = append(s.recordedFilters, capturedCall{collection: coll, filters: filters})
if s.exactSearchErr != nil {
return nil, s.exactSearchErr
}
if s.exactSearchFunc != nil {
return s.exactSearchFunc(coll, filters)
}
return nil, nil
}

// Stub out other VectorStore methods so search.Service won't panic.
func (s *testStore) Upsert(_ context.Context, _ string, _ []storage.Point) (*storage.UpdateResult, error) {
return nil, nil
}
func (s *testStore) Search(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
return nil, nil
}
func (s *testStore) SearchCodeOnly(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
return nil, nil
}
func (s *testStore) SearchDocsOnly(_ context.Context, _ string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
return nil, nil
}
func (s *testStore) SearchByChunkType(_ context.Context, _ string, _ storage.SearchQuery, _ string) ([]storage.SearchResult, error) {
return nil, nil
}
func (s *testStore) CreateCollection(_ context.Context, _ string, _ int) error {
return nil
}
func (s *testStore) GetCollectionInfo(_ context.Context, _ string) (*storage.CollectionInfo, error) {
return &storage.CollectionInfo{}, nil
}
func (s *testStore) GetCollectionPointCount(_ context.Context, _ string) (uint64, error) {
return 0, nil
}
func (s *testStore) DeleteByFilter(_ context.Context, _ string, _ string, _ interface{}) error {
return nil
}
func (s *testStore) DeleteCollection(_ context.Context, _ string) error {
return nil
}

// mockLLM satisfies llm.Provider (embedding not called during ExactSearch).
type mockLLM struct{}

func (m *mockLLM) Embed(_ context.Context, _ string) ([]float64, error) {
return []float64{0.1}, nil
}
func (m *mockLLM) Generate(_ context.Context, _ string, _ ...llm.GenerateOption) (string, error) {
return "", nil
}
func (m *mockLLM) GenerateStream(_ context.Context, _ string, _ ...llm.GenerateOption) (<-chan string, <-chan error) {
return nil, nil
}
func (m *mockLLM) Name() string              { return "mock" }
func (m *mockLLM) GetEmbeddingDimension() uint64 { return 1 }

// mockDetector satisfies detector.Detector for resolver setup.
type mockDetector struct{}

func (m *mockDetector) DetectFromFilePath(_ context.Context, _ string) (*contract.WorkspaceCandidate, *contract.ResolveWorkspaceError) {
return &contract.WorkspaceCandidate{Root: "/mock/ws", Confidence: 1.0}, nil
}

func newTestEngine(store *testStore) *Engine {
llmProvider := &mockLLM{}
idxSvc := indexer.NewService(llmProvider, store)
searchSvc := search.NewService(llmProvider, store)
eng := NewEngine(idxSvc, searchSvc, "", &config.Config{})

res := resolver.New(resolver.Dependencies{
Detector: &mockDetector{},
})
eng.SetResolver(res)
return eng
}

// ─── Tests ─────────────────────────────────────────────────────────────────────

// TestExactSearchPolyglotMergesFromMultipleCollections verifies that results
// from multiple language collections are merged into one slice.
func TestExactSearchPolyglotMergesFromMultipleCollections(t *testing.T) {
store := &testStore{
existing: map[string]bool{
"ragcode-ws1-go":     true,
"ragcode-ws1-python": true,
},
exactSearchFunc: func(coll string, _ map[string]interface{}) ([]storage.SearchResult, error) {
if coll == "ragcode-ws1-go" {
return []storage.SearchResult{
{Score: 1.0, Point: storage.Point{ID: "go-sym", Payload: map[string]interface{}{"name": "GoFunc"}}},
}, nil
}
if coll == "ragcode-ws1-python" {
return []storage.SearchResult{
{Score: 1.0, Point: storage.Point{ID: "py-sym", Payload: map[string]interface{}{"name": "py_func"}}},
}, nil
}
return nil, nil
},
}
eng := newTestEngine(store)

results, err := eng.ExactSearchPolyglot(context.Background(), "ws1", map[string]interface{}{"name": "anything"}, 10)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if len(results) != 2 {
t.Fatalf("expected 2 merged results (one per language), got %d", len(results))
}

ids := map[string]bool{}
for _, r := range results {
ids[r.Point.ID] = true
}
if !ids["go-sym"] {
t.Error("expected go-sym in merged results")
}
if !ids["py-sym"] {
t.Error("expected py-sym in merged results")
}
}

// TestExactSearchPolyglotSkipsNonExistentCollections verifies that collections
// that do not exist are silently skipped.
func TestExactSearchPolyglotSkipsNonExistentCollections(t *testing.T) {
store := &testStore{
existing: map[string]bool{
"ragcode-ws2-go": true,
// python, php, html do NOT exist
},
exactSearchFunc: func(coll string, _ map[string]interface{}) ([]storage.SearchResult, error) {
return []storage.SearchResult{
{Score: 1.0, Point: storage.Point{ID: "go-only"}},
}, nil
},
}
eng := newTestEngine(store)

results, err := eng.ExactSearchPolyglot(context.Background(), "ws2", map[string]interface{}{"name": "X"}, 10)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if len(results) != 1 {
t.Fatalf("expected 1 result from go collection only, got %d", len(results))
}
if results[0].Point.ID != "go-only" {
t.Fatalf("expected go-only ID, got %s", results[0].Point.ID)
}
// Verify ExactSearch was called only for go (not for the non-existent ones)
if len(store.recordedFilters) != 1 {
t.Fatalf("ExactSearch should only be called for existing collections, got %d calls", len(store.recordedFilters))
}
}

// TestExactSearchPolyglotReturnsErrWhenNoCollections verifies that
// ErrNoCollectionsFound is returned when no collections exist at all.
// This allows tools to return a meaningful "not indexed yet" message.
func TestExactSearchPolyglotReturnsErrWhenNoCollections(t *testing.T) {
store := &testStore{
existing: map[string]bool{}, // none
}
eng := newTestEngine(store)

_, err := eng.ExactSearchPolyglot(context.Background(), "ws3", map[string]interface{}{"name": "X"}, 10)
if err == nil {
t.Fatal("expected ErrNoCollectionsFound, got nil")
}
var noCollErr *ErrNoCollectionsFound
if !errors.As(err, &noCollErr) {
t.Fatalf("expected *ErrNoCollectionsFound, got: %T: %v", err, err)
}
if noCollErr.WorkspaceID != "ws3" {
t.Fatalf("expected WorkspaceID=ws3, got %q", noCollErr.WorkspaceID)
}
}

// TestSearchByNamePassesNameFilter verifies that SearchByName calls
// ExactSearchPolyglot with the filter {"name": symbolName}.
func TestSearchByNamePassesNameFilter(t *testing.T) {
store := &testStore{
existing: map[string]bool{"ragcode-ws4-go": true},
exactSearchFunc: func(coll string, filters map[string]interface{}) ([]storage.SearchResult, error) {
return []storage.SearchResult{
{Score: 1.0, Point: storage.Point{ID: "found", Payload: filters}},
}, nil
},
}
eng := newTestEngine(store)

results, err := eng.SearchByName(context.Background(), "ws4", "IndexItems", 5)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
if len(results) == 0 {
t.Fatal("expected at least one result")
}
if len(store.recordedFilters) == 0 {
t.Fatal("expected ExactSearch to be called")
}
capturedName, ok := store.recordedFilters[0].filters["name"]
if !ok {
t.Fatal("expected filter with 'name' key")
}
if capturedName != "IndexItems" {
t.Fatalf("expected name=IndexItems in filter, got %v", capturedName)
}
}

// TestExactSearchPolyglotExactSearchError verifies that when ExactSearch fails
// for a collection, the error is logged but other collections still contribute.
func TestExactSearchPolyglotExactSearchError(t *testing.T) {
store := &testStore{
existing: map[string]bool{
"ragcode-ws5-go":     true,
"ragcode-ws5-python": true,
},
exactSearchFunc: func(coll string, _ map[string]interface{}) ([]storage.SearchResult, error) {
if coll == "ragcode-ws5-go" {
return nil, errors.New("qdrant error")
}
// python succeeds
return []storage.SearchResult{
{Score: 1.0, Point: storage.Point{ID: "py-fallback"}},
}, nil
},
}
eng := newTestEngine(store)

results, err := eng.ExactSearchPolyglot(context.Background(), "ws5", map[string]interface{}{"name": "X"}, 10)
// No error returned even if one collection failed (we log and continue)
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
// Should still get results from python
if len(results) != 1 || results[0].Point.ID != "py-fallback" {
t.Fatalf("expected py-fallback result, got: %v", results)
}
}


package tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// An unreachable vector store must surface as an error, never as "no usages"
// or "not indexed", which a client would take as fact.
func TestExactLookupsReportVectorStoreOutage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signer.go")
	if err := os.WriteFile(path, []byte("package signer\n"), 0600); err != nil {
		t.Fatal(err)
	}
	down := errors.New("connection refused")
	stores := map[string]*mockVectorStore{
		// Collection existence still answered from the search service cache.
		"scroll fails": {ExactSearchFunc: func(context.Context, string, map[string]interface{}, int) ([]storage.SearchResult, error) {
			return nil, down
		}},
		"existence check fails": {CollectionExistsFunc: func(context.Context, string) (bool, error) { return false, down }},
	}
	for name, store := range stores {
		eng := setupTestEngine(store)
		t.Cleanup(eng.Close)
		exact := true
		calls := map[string]func() (string, error){
			"rag_search": func() (string, error) {
				return tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "TimestampSigner", FilePath: path, ExactSymbol: &exact})
			},
			"find_usages": func() (string, error) {
				return tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "BadSignature", "file_path": path})
			},
			"call_hierarchy": func() (string, error) {
				return tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "base64_decode", "file_path": path})
			},
		}
		for tool, call := range calls {
			text, err := call()
			if err != nil {
				continue
			}
			var response tools.ToolResponse
			if err := json.Unmarshal([]byte(text), &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != "error" {
				t.Errorf("%s, %s: status=%s %s, want an error", name, tool, response.Status, response.Message)
			}
		}
	}
}

// rag_index_workspace recreate=true drops a collection while the search
// service still caches it as existing; a lookup in that gap must read as a
// missing collection (indexing), not as a broken tool.
func TestExactLookupsDuringCollectionRecreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "count.go")
	if err := os.WriteFile(path, []byte("package count\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var dropped atomic.Bool
	store := &mockVectorStore{
		CollectionExistsFunc: func(_ context.Context, name string) (bool, error) {
			return strings.HasSuffix(name, "-go") && !dropped.Load(), nil
		},
		ExactSearchFunc: func(context.Context, string, map[string]interface{}, int) ([]storage.SearchResult, error) {
			if dropped.Load() {
				return nil, errors.New("rpc error: code = NotFound desc = Not found: Collection doesn't exist!")
			}
			return nil, nil
		},
	}
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	exact := true
	search := tools.NewSmartSearchTool(eng)
	// Warms the collection cache with the -go collection.
	if _, err := search.Execute(context.Background(), tools.SmartSearchInput{Query: "newCountValue", FilePath: path, ExactSymbol: &exact}); err != nil {
		t.Fatal(err)
	}
	dropped.Store(true)
	calls := map[string]func() (string, error){
		"rag_search": func() (string, error) {
			return search.Execute(context.Background(), tools.SmartSearchInput{Query: "newCountValue", FilePath: path, ExactSymbol: &exact})
		},
		"find_usages": func() (string, error) {
			return tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "newCountValue", "file_path": path})
		},
	}
	for tool, call := range calls {
		text, err := call()
		if err != nil {
			t.Errorf("%s: %v", tool, err)
			continue
		}
		var response tools.ToolResponse
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status == "error" {
			t.Errorf("%s: %s", tool, text)
		}
	}
}

// A run that ended without creating any collection (a store error at
// CreateCollection) is over: reporting it as in progress has clients wait
// for an index that is not being built.
func TestExactLookupsAfterFailedIndexAreNotInProgress(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "signer.py")
	if err := os.WriteFile(path, []byte("def sign(): pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	indexer.SaveIndexStatus(root, &indexer.IndexStatus{StartedAt: "2026-01-01T00:00:00Z", EndedAt: "2026-01-01T00:00:01Z",
		Error: "indexing failed for 1 language(s): python: failed to create collection: Too many open files"})
	eng := setupTestEngine(&mockVectorStore{CollectionExistsFunc: func(context.Context, string) (bool, error) { return false, nil }})
	t.Cleanup(eng.Close)
	calls := map[string]func() (string, error){
		"find_usages": func() (string, error) {
			return tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "sign", "file_path": path})
		},
		"call_hierarchy": func() (string, error) {
			return tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "sign", "file_path": path})
		},
	}
	for tool, call := range calls {
		text, err := call()
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		var response tools.ToolResponse
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status == "indexing_in_progress" {
			t.Errorf("%s reports a failed, finished run as in progress: %s", tool, text)
		}
	}
}

// With the collection cache still warm, a store that refuses every search
// must not be answered from the AST fallback as "indexing in progress".
func TestSearchReportsVectorStoreOutage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signer.go")
	if err := os.WriteFile(path, []byte("package signer\n\n// DeriveKey derives the signing key.\nfunc DeriveKey() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	down := func(context.Context, string, storage.SearchQuery) ([]storage.SearchResult, error) {
		return nil, errors.New("connection refused")
	}
	eng := setupTestEngine(&mockVectorStore{SearchFunc: down, SearchCodeOnlyFunc: down})
	t.Cleanup(eng.Close)
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "derive signing key", FilePath: path})
	if err != nil {
		text = err.Error()
	} else {
		var response tools.ToolResponse
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != "error" {
			t.Fatalf("status=%s %s, want an error", response.Status, text)
		}
	}
	if !strings.Contains(text, "connection refused") {
		t.Fatalf("%s, want the store error", text)
	}
}

// One stale collection (built with another embedding size) failing while the
// primary answers with nothing is not an outage: the search must still fall
// back to the AST scan of the workspace.
func TestSearchSurvivesOneFailingCollection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signer.go")
	if err := os.WriteFile(path, []byte("package signer\n\n// DeriveKey derives the signing key.\nfunc DeriveKey() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stale := func(_ context.Context, collection string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
		if strings.HasSuffix(collection, "-python") {
			return nil, errors.New("wrong vector dimension")
		}
		return nil, nil
	}
	eng := setupTestEngine(&mockVectorStore{SearchFunc: stale, SearchCodeOnlyFunc: stale})
	t.Cleanup(eng.Close)
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "derive key", FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "DeriveKey") {
		t.Fatalf("%s, want the fallback hit", text)
	}
}

// A semantic rag_search during a recreate's drop-to-rebuild window, while the
// search service still caches the dropped collection as existing, must not
// fail with the store's "Collection doesn't exist" error, nor when the
// collection is already rebuilt by the time the search looks again.
func TestSemanticSearchDuringCollectionRecreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "count.go")
	if err := os.WriteFile(path, []byte("package count\n"), 0600); err != nil {
		t.Fatal(err)
	}
	notFound := errors.New("rpc error: code = NotFound desc = Not found: Collection doesn't exist!")
	for name, rebuilt := range map[string]bool{"dropped": false, "rebuilt before the recheck": true} {
		t.Run(name, func(t *testing.T) {
			var dropped, recreated atomic.Bool
			semantic := func(context.Context, string, storage.SearchQuery) ([]storage.SearchResult, error) {
				if dropped.Load() && !recreated.Load() {
					return nil, notFound
				}
				return nil, nil
			}
			store := &mockVectorStore{
				CollectionExistsFunc: func(_ context.Context, name string) (bool, error) {
					if !strings.HasSuffix(name, "-go") {
						return false, nil
					}
					// The cache answers until a lookup reaches the store, which
					// sees the collection rebuilt in the "rebuilt" case.
					if dropped.Load() && rebuilt {
						recreated.Store(true)
					}
					return !dropped.Load() || recreated.Load(), nil
				},
				SearchFunc:         semantic,
				SearchCodeOnlyFunc: semantic,
			}
			eng := setupTestEngine(store)
			t.Cleanup(eng.Close)
			search := tools.NewSmartSearchTool(eng)
			// Warms the collection cache with the -go collection.
			if _, err := search.Execute(context.Background(), tools.SmartSearchInput{Query: "newCountValue", FilePath: path}); err != nil {
				t.Fatal(err)
			}
			dropped.Store(true)
			text, err := search.Execute(context.Background(), tools.SmartSearchInput{Query: "newCountValue", FilePath: path})
			if err != nil {
				t.Fatal(err)
			}
			// Which of the parallel searches sees the dropped collection first
			// decides between "not indexed" and no results; either way the
			// store's raw error must not reach the client.
			if strings.Contains(text, "Collection doesn't exist") || strings.Contains(text, "NotFound") {
				t.Errorf("rag_search: %s", text)
			}
		})
	}
}

package tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
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

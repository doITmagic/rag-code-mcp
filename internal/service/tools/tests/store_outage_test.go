package tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

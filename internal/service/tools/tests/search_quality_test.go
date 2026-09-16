package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
)

func TestMissingIdentifierDoesNotBecomeSemanticSuccess(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.go")
	if err := os.WriteFile(path, []byte("package example\nfunc ExistingSymbol() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".ragcode"), 0700); err != nil {
		t.Fatal(err)
	}
	store := &mockVectorStore{SearchCodeOnlyFunc: func(context.Context, string, storage.SearchQuery) ([]storage.SearchResult, error) {
		return []storage.SearchResult{{Score: .55, Point: storage.Point{ID: "unrelated", Payload: map[string]interface{}{"name": "ExistingSymbol", "file_path": path, "content": "func ExistingSymbol() {}", "type": "function"}}}}, nil
	}}
	tool := tools.NewSmartSearchTool(setupTestEngine(store))
	semantic := false
	for _, tc := range []struct {
		exact  *bool
		status string
	}{{nil, "no_results"}, {&semantic, "success"}} {
		text, err := tool.Execute(context.Background(), tools.SmartSearchInput{Query: "MissingSymbol", FilePath: path, ExactSymbol: tc.exact, IncludeFullContent: true})
		if err != nil {
			t.Fatal(err)
		}
		var response tools.ToolResponse
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != tc.status {
			t.Fatal(text)
		}
	}
	m := telemetry.ReadAggregatedMetrics(root)
	if m == nil || m.TotalSearches != 2 || m.SearchesWithResults != 1 {
		t.Fatalf("metrics=%+v", m)
	}
}

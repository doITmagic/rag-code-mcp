package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
)

func TestExactSearchFindsPackageQualifiedGoFunction(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"cache.go": "package cache\n\nfunc Load() string { return \"loaded\" }\n\ntype Disk struct{}\n\nfunc (Disk) Load() string { return \"disk\" }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	exact := true
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "cache.Load", FilePath: filepath.Join(dir, "cache.go"), ExactSymbol: &exact})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Data []map[string]interface{} }
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0]["type"] != "function" {
		t.Fatalf("cache.Load want only the package function: %s", text)
	}
}

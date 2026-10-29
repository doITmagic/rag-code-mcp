package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
)

// A call on the method's own receiver is recursion and must stay a usage,
// while c.lru.Get next to it is still not one.
func TestGoMethodKeepsRecursiveSelfCall(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"lru.go": "package cache\n\ntype LRUCache struct{}\n\nfunc (l *LRUCache) Get(key string) string { return key }\n",
		"cache.go": "package cache\n\ntype PromptCache struct{ lru *LRUCache }\n\n" +
			"func (c *PromptCache) Get(key string) string {\n\tif key == \"\" {\n\t\treturn c.Get(\"x\")\n\t}\n\treturn c.lru.Get(key)\n}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "cache.go")
	text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": "PromptCache.Get", "file_path": path})
	if err != nil {
		t.Fatal(err)
	}
	var usages struct{ Data []tools.UsageResult }
	if err := json.Unmarshal([]byte(text), &usages); err != nil {
		t.Fatal(err)
	}
	if len(usages.Data) != 1 || usages.Data[0].FilePath != path {
		t.Errorf("PromptCache.Get usages want its own recursive call: %s", text)
	}

	text, err = tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
		"symbol_name": "PromptCache.Get", "direction": "incoming", "file_path": path, "depth": 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct{ Data tools.CallNode }
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	if children := hierarchy.Data.Children; len(children) != 1 || !children[0].Recursive {
		t.Errorf("PromptCache.Get callers want itself, marked recursive: %s", text)
	}
}

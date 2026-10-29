package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
)

// Three methods share the name Get. PromptCache.Get's only caller lives in
// another file; its own c.lru.Get call is LRUCache.Get, not a self-call.
func TestQualifiedGoMethodKeepsCrossFileCallers(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"lru.go":   "package cache\n\ntype LRUCache struct{}\n\nfunc (l *LRUCache) Get(key string) string { return key }\n",
		"ttl.go":   "package cache\n\ntype TTLCache struct{}\n\nfunc (t *TTLCache) Get(key string) string { return key }\n",
		"cache.go": "package cache\n\ntype PromptCache struct{ lru *LRUCache }\n\nfunc (c *PromptCache) Get(key string) string { return c.lru.Get(key) }\n",
		"engine.go": "package cache\n\ntype Engine struct{ promptCache *PromptCache }\n\n" +
			"func (e *Engine) Execute(key string) string { return e.promptCache.Get(key) }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "cache.go")
	for _, name := range []string{"PromptCache.Get", "cache.PromptCache.Get"} {
		text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": name, "file_path": path})
		if err != nil {
			t.Fatal(err)
		}
		var usages struct{ Data []tools.UsageResult }
		if err := json.Unmarshal([]byte(text), &usages); err != nil {
			t.Fatal(err)
		}
		if len(usages.Data) != 1 || usages.Data[0].Name != "Execute" {
			t.Errorf("%s usages want only Execute: %s", name, text)
		}

		text, err = tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
			"symbol_name": name, "direction": "incoming", "file_path": path, "depth": 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		var hierarchy struct{ Data tools.CallNode }
		if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
			t.Fatal(err)
		}
		if children := hierarchy.Data.Children; len(children) != 1 || children[0].Name != "Execute" || children[0].Recursive {
			t.Errorf("%s callers want only Execute: %s", name, text)
		}
	}
}

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

// A range variable that shadows the receiver is not the receiver: s.Close()
// on it calls Listener.Close, not Server.Close recursively.
func TestGoMethodShadowedReceiverIsNotSelfCall(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"l.go": "package srv\n\ntype Listener struct{}\n\nfunc (l *Listener) Close() error { return nil }\n",
		"s.go": "package srv\n\ntype Server struct{ ls []*Listener }\n\n" +
			"func (s *Server) Close() error {\n\tfor _, s := range s.ls {\n\t\ts.Close()\n\t}\n\treturn nil\n}\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	path := filepath.Join(dir, "s.go")
	usagesOf := func(symbol string) []tools.UsageResult {
		text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": symbol, "file_path": path})
		if err != nil {
			t.Fatal(err)
		}
		var usages struct{ Data []tools.UsageResult }
		if err := json.Unmarshal([]byte(text), &usages); err != nil {
			t.Fatal(err)
		}
		return usages.Data
	}
	if got := usagesOf("Server.Close"); len(got) != 0 {
		t.Errorf("Server.Close usages want none, got %+v", got)
	}
	if got := usagesOf("Listener.Close"); len(got) != 1 || got[0].FilePath != path {
		t.Errorf("Listener.Close usages want the call in Server.Close, got %+v", got)
	}
}

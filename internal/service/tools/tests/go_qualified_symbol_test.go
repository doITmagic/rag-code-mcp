package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

func TestQualifiedGoMethodsPreserveReceiverAndPackage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.go")
	if err := os.WriteFile(path, []byte("package cache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	method := func(id, pkg, receiver, language string) storage.SearchResult {
		return storage.SearchResult{Point: storage.Point{ID: id, Payload: map[string]interface{}{
			"symbol_id": id, "name": "Get", "package": pkg, "type": "method", "language": language,
			"file_path": path, "metadata": map[string]interface{}{"receiver": receiver},
		}}}
	}
	points := []storage.SearchResult{
		method("prompt", "cache", "PromptCache", "go"),
		method("other-receiver", "cache", "OtherCache", "go"),
		method("other-package", "other", "PromptCache", "go"),
		method("other-language", "cache", "PromptCache", "javascript"),
		{Point: storage.Point{ID: "caller", Payload: map[string]interface{}{
			"symbol_id": "caller", "name": "ReadPrompt", "file_path": path, "type": "function", "language": "go",
			"relations": []interface{}{map[string]interface{}{"type": "calls", "target_name": "Get", "target_id": "prompt", "resolution": "resolved"}},
		}}},
	}
	store := &mockVectorStore{ExactSearchFunc: func(_ context.Context, collection string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
		if !strings.HasSuffix(collection, "-go") {
			return nil, nil
		}
		var matches []storage.SearchResult
		for _, point := range points {
			match := true
			for key, value := range filters {
				if strings.HasPrefix(key, "relations[].") {
					match = false
					for _, raw := range relationList(point.Point.Payload) {
						if raw.(map[string]interface{})[strings.TrimPrefix(key, "relations[].")] == value {
							match = true
						}
					}
				} else {
					var actual interface{} = point.Point.Payload
					for _, part := range strings.Split(key, ".") {
						object, _ := actual.(map[string]interface{})
						actual = object[part]
					}
					match = actual == value
				}
				if !match {
					break
				}
			}
			if match {
				matches = append(matches, point)
			}
		}
		return matches, nil
	}}
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	exact := true
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"PromptCache.Get", 2},
		{"cache.PromptCache.Get", 1},
		{"other.PromptCache.Get", 1},
		{"MissingCache.Get", 0},
		{"missing.PromptCache.Get", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: tc.query, FilePath: path, ExactSymbol: &exact})
			if err != nil {
				t.Fatal(err)
			}
			var response struct{ Data []map[string]interface{} }
			if err := json.Unmarshal([]byte(text), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Data) != tc.want {
				t.Fatalf("want %d scoped methods: %s", tc.want, text)
			}
		})
	}
	text, err := tools.NewCallHierarchyTool(eng).Execute(context.Background(), map[string]interface{}{
		"symbol_name": "cache.PromptCache.Get", "direction": "incoming", "file_path": path, "depth": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var hierarchy struct {
		Data struct {
			SymbolID string `json:"symbol_id"`
			Children []tools.CallNode
		}
	}
	if err := json.Unmarshal([]byte(text), &hierarchy); err != nil {
		t.Fatal(err)
	}
	if hierarchy.Data.SymbolID != "prompt" || len(hierarchy.Data.Children) != 1 || hierarchy.Data.Children[0].SymbolID != "caller" {
		t.Fatalf("qualified hierarchy used the wrong identity: %s", text)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{"cache.PromptCache.Get", 1}, {"other.PromptCache.Get", 0}, {"MissingCache.Get", 0}} {
		text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": tc.query, "file_path": path})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []tools.UsageResult }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != tc.want {
			t.Errorf("%s usages want %d: %s", tc.query, tc.want, text)
		}
	}
}

func relationList(payload map[string]interface{}) []interface{} {
	relations, _ := payload["relations"].([]interface{})
	return relations
}

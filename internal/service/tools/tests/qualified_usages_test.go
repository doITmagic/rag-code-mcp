package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// Qualified names the resolver cannot map to one symbol must still find
// usages by name; only a checked qualifier that matches nothing is a miss.
func TestQualifiedUsagesFallBackToName(t *testing.T) {
	for _, tc := range []struct {
		ext      string
		analyzer parser.Analyzer
		files    map[string]string
		queries  map[string]string // symbol_name -> expected caller ("" = no_results)
	}{
		{"go", golang.NewCodeAnalyzer(), map[string]string{
			"cache.go": "package cache\n\nimport \"fmt\"\n\ntype PromptCache struct{}\n\n" +
				"func NewPromptCache() *PromptCache { return &PromptCache{} }\n\n" +
				"func (c *PromptCache) Get(key string) error { return fmt.Errorf(\"miss %s\", key) }\n",
			"main.go": "package cache\n\ntype MissingCache struct{}\n\nfunc Run() error { c := NewPromptCache(); var _ PromptCache = *c; return c.Get(\"k\") }\n",
		}, map[string]string{
			"cache.NewPromptCache": "Run", "cache.PromptCache": "NewPromptCache", "fmt.Errorf": "Get",
			"(*PromptCache).Get": "Run", "*PromptCache.Get": "Run", "MissingCache.Get": "",
		}},
		{"py", python.NewAnalyzer(), map[string]string{
			"service.py": "class UserService:\n    def get_user(self, user_id):\n        return user_id\n\n\n" +
				"def handler():\n    return UserService.get_user(None, 1)\n",
		}, map[string]string{"UserService.get_user": "handler"}},
		{"php", php.NewAnalyzer(), map[string]string{
			"User.php": "<?php\nnamespace App\\Models;\n\nclass User {\n    public static function find($id) { return $id; }\n}\n\n" +
				"class Controller {\n    public function show($id) { return User::find($id); }\n}\n",
		}, map[string]string{"User::find": "show"}},
	} {
		dir, store := indexFixture(t, tc.ext, tc.analyzer, tc.files)
		eng := setupTestEngine(store)
		t.Cleanup(eng.Close)
		for query, caller := range tc.queries {
			text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": query, "file_path": filepath.Join(dir, "x."+tc.ext)})
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Status string
				Data   []tools.UsageResult
			}
			if err := json.Unmarshal([]byte(text), &response); err != nil {
				t.Fatal(err)
			}
			if caller == "" {
				if response.Status != "no_results" {
					t.Errorf("%s: want no_results: %s", query, text)
				}
				continue
			}
			found := false
			for _, usage := range response.Data {
				found = found || usage.Name == caller
			}
			if !found {
				t.Errorf("%s: want usage in %s: %s", query, caller, text)
			}
		}
	}
}

// A same-named method elsewhere does not rule out a qualifier that names no
// indexed type (an external package) or an interface, whose methods are not
// indexed with a receiver.
func TestQualifierWithoutIndexedTypeKeepsUsages(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"log.go": "package app\n\nimport \"fmt\"\n\ntype Logger struct{}\n\n" +
			"func (l *Logger) Errorf(f string) error { return nil }\n\n" +
			"func Run() error { return fmt.Errorf(\"x\") }\n",
		"store.go": "package app\n\ntype Store interface{ Get(k string) string }\n\ntype Mem struct{}\n\n" +
			"func (m Mem) Get(k string) string { return k }\n\n" +
			"func Use(s Store) string { return s.Get(\"k\") }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	for query, caller := range map[string]string{"fmt.Errorf": "Run", "Store.Get": "Use"} {
		text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": query, "file_path": filepath.Join(dir, "x.go")})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []tools.UsageResult }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, usage := range response.Data {
			found = found || usage.Name == caller
		}
		if !found {
			t.Errorf("%s: want usage in %s: %s", query, caller, text)
		}
	}
}

// A method promoted through an embedded field is not declared on the outer
// type, yet Outer.Do names it and o.Do() is a usage.
func TestPromotedGoMethodKeepsUsages(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"embed.go": "package embed\n\ntype Inner struct{}\n\nfunc (Inner) Do() {}\n\ntype Outer struct{ Inner }\n\n" +
			"func CallOuter() { var o Outer; o.Do() }\n",
	})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	for _, query := range []string{"Outer.Do", "embed.Outer.Do"} {
		text, err := tools.NewFindUsagesTool(eng).Execute(context.Background(), map[string]interface{}{"symbol_name": query, "file_path": filepath.Join(dir, "x.go")})
		if err != nil {
			t.Fatal(err)
		}
		var response struct{ Data []tools.UsageResult }
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) != 1 || response.Data[0].Name != "CallOuter" {
			t.Errorf("%s: want usage in CallOuter: %s", query, text)
		}
	}
}

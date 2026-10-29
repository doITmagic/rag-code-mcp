package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
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
	}{{nil, "no_results"}, {&semantic, "no_results"}} {
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
	if m == nil || m.TotalSearches != 2 || m.SearchesWithResults != 0 {
		t.Fatalf("metrics=%+v", m)
	}
}

func TestStrictDocsAppliesLimitAfterModeFilter(t *testing.T) {
	root := t.TempDir()
	codePath, docsPath := filepath.Join(root, "cache.go"), filepath.Join(root, "README.md")
	for _, path := range []string{codePath, docsPath} {
		if err := os.WriteFile(path, []byte("cache lifetime"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := func(id, path, kind string, score float32) []storage.SearchResult {
		return []storage.SearchResult{{Score: score, Point: storage.Point{ID: id, Payload: map[string]interface{}{
			"name": id, "type": kind, "file_path": path, "content": "cache lifetime", "start_line": 1, "end_line": 1,
		}}}}
	}
	// A code chunk outranks the README; strict_docs must still return the README.
	search := func(_ context.Context, collection string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
		switch {
		case strings.HasSuffix(collection, "-go"):
			return result("code", codePath, "function", 0.99), nil
		case strings.HasSuffix(collection, "-docs"):
			return result("docs", docsPath, "documentation", 0.95), nil
		}
		return nil, nil
	}
	eng := setupTestEngine(&mockVectorStore{SearchFunc: search, SearchCodeOnlyFunc: search})
	t.Cleanup(eng.Close)
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "cache lifetime", FilePath: codePath, Mode: "strict_docs", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Status string
		Data   []map[string]interface{}
	}
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data) != 1 || response.Data[0]["file_path"] != docsPath {
		t.Fatalf("strict_docs limit=1: status=%s data=%v, want README.md", response.Status, response.Data)
	}
}

func TestStrictCodeKeepsHybridScoreOfDefaultMode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "flag.go")
	if err := os.WriteFile(path, []byte("package flag\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hit := func(id string, score float32, content string) storage.SearchResult {
		return storage.SearchResult{Score: score, Point: storage.Point{ID: id, Payload: map[string]interface{}{
			"name": id, "type": "function", "file_path": path, "content": content, "start_line": 1, "end_line": 1,
		}}}
	}
	// wrap ranks 4th semantically but matches every query word. A long chunk
	// with many more word hits sits far down the semantic ranking.
	pool := []storage.SearchResult{hit("a", .66, "x"), hit("b", .66, "y"), hit("c", .66, "z"), hit("wrap", .62, "wrap usage text column")}
	for i := len(pool); i < 30; i++ {
		pool = append(pool, hit(fmt.Sprintf("filler%d", i), .5, "x"))
	}
	pool = append(pool, hit("long", .5, strings.Repeat("wrap usage text column ", 50)))
	search := func(_ context.Context, collection string, q storage.SearchQuery) ([]storage.SearchResult, error) {
		if !strings.HasSuffix(collection, "-go") {
			return nil, nil
		}
		return append([]storage.SearchResult(nil), pool[:min(q.Limit, len(pool))]...), nil
	}
	eng := setupTestEngine(&mockVectorStore{SearchFunc: search, SearchCodeOnlyFunc: search})
	t.Cleanup(eng.Close)
	for _, mode := range []string{"", "strict_code"} {
		text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "wrap usage text column", FilePath: path, Mode: mode, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		var response struct {
			Data []map[string]interface{}
		}
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Data) == 0 || response.Data[0]["name"] != "wrap" {
			t.Fatalf("mode=%q: %s, want wrap first", mode, text)
		}
	}
}

func TestSearchWithoutCodeFileUsesWorkspaceLanguage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "src")
	path := filepath.Join(dir, "signer.py")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("def derive_key(): pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fully indexed Python workspace: no Go collection exists.
	indexer.SaveIndexStatus(root, &indexer.IndexStatus{StartedAt: "2026-01-01T00:00:00Z", EndedAt: "2026-01-01T00:00:01Z",
		Languages: map[string]indexer.LangStatus{"python": {OnDisk: 1, Processed: 1}, "docs": {OnDisk: 3, Processed: 3}}})
	search := func(_ context.Context, collection string, _ storage.SearchQuery) ([]storage.SearchResult, error) {
		if !strings.HasSuffix(collection, "-python") {
			return nil, nil
		}
		// Only the lexical half of the search lifts this hit over the score floor.
		return []storage.SearchResult{{Score: .45, Point: storage.Point{ID: "derive_key", Payload: map[string]interface{}{
			"name": "derive_key", "type": "function", "file_path": path, "content": "def derive_key signing", "start_line": 1, "end_line": 1,
		}}}}, nil
	}
	exists := func(_ context.Context, name string) (bool, error) {
		return strings.HasSuffix(name, "-python") || strings.HasSuffix(name, "-docs"), nil
	}
	eng := setupTestEngine(&mockVectorStore{SearchFunc: search, SearchCodeOnlyFunc: search, CollectionExistsFunc: exists})
	t.Cleanup(eng.Close)
	// file_path is a directory, so it names no language.
	text, err := tools.NewSmartSearchTool(eng).Execute(context.Background(), tools.SmartSearchInput{Query: "derive signing key", FilePath: dir})
	if err != nil {
		t.Fatal(err)
	}
	var response tools.ToolResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "success" || response.Context.Language != "python" {
		t.Fatalf("status=%s language=%s: %s, want derive_key from the python collection", response.Status, response.Context.Language, text)
	}
	if jobs := eng.ActiveIndexingJobs(); len(jobs) != 0 {
		t.Fatalf("search of an indexed workspace started indexing: %v", jobs)
	}
}

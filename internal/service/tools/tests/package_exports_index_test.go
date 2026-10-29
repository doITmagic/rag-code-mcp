package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	golang "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

// indexGoTree parses root with the real Go analyzer and indexer; only the
// embedder and the store are fakes. ExactSearch matches top-level payload keys.
func indexGoTree(t *testing.T, root string) *mockVectorStore {
	t.Helper()
	parsed, err := golang.NewCodeAnalyzer().Analyze(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	var points []storage.Point
	store := &mockVectorStore{UpsertFunc: func(_ context.Context, _ string, batch []storage.Point) (*storage.UpdateResult, error) {
		points = append(points, batch...)
		return nil, nil
	}}
	if err := indexer.NewService(&mockLLMProvider{}, store).IndexItems(context.Background(), "exports-go", parsed.Symbols); err != nil {
		t.Fatal(err)
	}
	store.ExactSearchFunc = func(_ context.Context, collection string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
		if !strings.HasSuffix(collection, "-go") {
			return nil, nil
		}
		var found []storage.SearchResult
		for _, point := range points {
			match := true
			for key, want := range filters {
				match = match && point.Payload[key] == want
			}
			if match {
				found = append(found, storage.SearchResult{Point: point, Score: 1})
			}
		}
		return found, nil
	}
	return store
}

func writeGoFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, code := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func listExports(t *testing.T, store *mockVectorStore, pkg, filePath string) map[string][]tools.ExportedSymbol {
	t.Helper()
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	out, err := tools.NewListPackageExportsTool(eng).Execute(context.Background(), map[string]interface{}{"package": pkg, "file_path": filePath})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Data map[string][]tools.ExportedSymbol
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatal(err)
	}
	return response.Data
}

func TestPackageExportsFullImportPathSelectsOnePackage(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{
		"go.mod":                 "module example.com/project\ngo 1.25\n",
		"api/cache/cache.go":     "package cache\nfunc APIOnly() {}\n",
		"storage/cache/cache.go": "package cache\nfunc StorageOnly() {}\n",
	})
	store := indexGoTree(t, root)
	apiFile := filepath.Join(root, "api/cache/cache.go")

	var names []string
	for _, sym := range listExports(t, store, "example.com/project/api/cache", apiFile)["function"] {
		names = append(names, sym.Name)
	}
	if len(names) != 1 || names[0] != "APIOnly" {
		t.Fatalf("example.com/project/api/cache exports = %v, want [APIOnly]", names)
	}

	// The short name still lists every package called cache.
	if got := len(listExports(t, store, "cache", apiFile)["function"]); got != 2 {
		t.Fatalf("cache exports = %d functions, want 2", got)
	}
}

// A full path must not fall back to a namesake in another directory.
func TestPackageExportsFullImportPathOfUnindexedPackage(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{
		"go.mod":                 "module example.com/project\ngo 1.25\n",
		"storage/cache/cache.go": "package cache\nfunc StorageOnly() {}\n",
	})
	file := filepath.Join(root, "go.mod")
	if got := listExports(t, indexGoTree(t, root), "example.com/project/api/cache", file); len(got) != 0 {
		t.Fatalf("example.com/project/api/cache exports = %v, want none", got)
	}
	// The module's root package still matches its import path.
	writeGoFiles(t, root, map[string]string{"root.go": "package project\nfunc RootOnly() {}\n"})
	if got := listExports(t, indexGoTree(t, root), "example.com/project", file)["function"]; len(got) != 1 || got[0].Name != "RootOnly" {
		t.Fatalf("example.com/project exports = %v, want [RootOnly]", got)
	}
}

// A nested module's import path starts at its own go.mod, not at the workspace
// root above it, even when its directory is named differently.
func TestPackageExportsFullImportPathInNestedModule(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{
		".git/HEAD":                     "ref: refs/heads/main\n",
		"go.work":                       "go 1.25\nuse ./server\n",
		"server/go.mod":                 "module github.com/acme/api\ngo 1.25\n",
		"server/storage/cache/cache.go": "package cache\nfunc StorageOnly() {}\n",
		"server/api/cache/cache.go":     "package cache\nfunc APIOnly() {}\n",
	})
	store := indexGoTree(t, root)
	for _, file := range []string{filepath.Join(root, "go.work"), filepath.Join(root, "server/go.mod")} {
		got := listExports(t, store, "github.com/acme/api/storage/cache", file)["function"]
		if len(got) != 1 || got[0].Name != "StorageOnly" {
			t.Errorf("file_path=%s: github.com/acme/api/storage/cache exports = %v, want [StorageOnly]", file, got)
		}
		if got := listExports(t, store, "github.com/acme/api/other/cache", file); len(got) != 0 {
			t.Errorf("file_path=%s: github.com/acme/api/other/cache exports = %v, want none", file, got)
		}
	}
}

// A deprecated module keeps its path; the trailing comment is not part of it.
func TestPackageExportsDeprecatedModuleComment(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{
		"go.mod":                 "module example.com/project // Deprecated: use example.com/project/v2\ngo 1.25\n",
		"storage/cache/cache.go": "package cache\nfunc StorageOnly() {}\n",
		"api/cache/cache.go":     "package cache\nfunc APIOnly() {}\n",
	})
	store := indexGoTree(t, root)
	got := listExports(t, store, "example.com/project/storage/cache", filepath.Join(root, "go.mod"))["function"]
	if len(got) != 1 || got[0].Name != "StorageOnly" {
		t.Fatalf("example.com/project/storage/cache exports = %v, want [StorageOnly]", got)
	}
}

// JS and Python modules are files: their package is the file stem.
func TestPackageExportsPathOfFileModule(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{"go.mod": "module example.com/project\ngo 1.25\n"})
	point := func(name, language, path string) storage.SearchResult {
		return storage.SearchResult{Point: storage.Point{ID: name, Payload: map[string]interface{}{
			"name": name, "type": "function", "package": "format", "language": language,
			"file_path": filepath.Join(root, path), "is_public": true,
		}}}
	}
	points := []storage.SearchResult{
		point("GoFormat", "go", "x/format/format.go"),
		point("jsFormat", "javascript", "src/utils/format.js"),
		point("py_format", "python", "lib/format.py"),
	}
	store := &mockVectorStore{ExactSearchFunc: func(_ context.Context, collection string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
		var found []storage.SearchResult
		for _, p := range points {
			if strings.HasSuffix(collection, "-"+p.Point.Payload["language"].(string)) && p.Point.Payload["package"] == filters["package"] {
				found = append(found, p)
			}
		}
		return found, nil
	}}
	file := filepath.Join(root, "go.mod")
	for request, want := range map[string]string{"src/utils/format": "jsFormat", "lib/format": "py_format", "example.com/project/x/format": "GoFormat"} {
		got := listExports(t, store, request, file)["function"]
		if len(got) != 1 || got[0].Name != want {
			t.Errorf("%s exports = %v, want [%s]", request, got, want)
		}
	}
}

func TestPackageExportsKeepSameNamedMethods(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{
		"cache.go": "package cache\ntype Memory struct{}\nfunc (Memory) Get() string { return \"memory\" }\ntype Disk struct{}\nfunc (Disk) Get() string { return \"disk\" }\n",
	})
	var signatures []string
	for _, sym := range listExports(t, indexGoTree(t, root), "cache", filepath.Join(root, "cache.go"))["method"] {
		signatures = append(signatures, sym.Signature)
	}
	if len(signatures) != 2 {
		t.Fatalf("cache methods = %v, want Memory.Get and Disk.Get", signatures)
	}
}

// A trailing slash still names the package; its empty last segment matched
// the documentation chunks, which have no package.
func TestPackageExportsTrailingSlash(t *testing.T) {
	root := t.TempDir()
	writeGoFiles(t, root, map[string]string{
		"go.mod":    "module example.com/pflag\ngo 1.25\n",
		"flag.go":   "package pflag\nfunc Parse() {}\n",
		"README.md": "# Usage\n\nCall Parse before reading flags.\n",
	})
	store := indexGoTree(t, root)
	parsed, err := docs.NewAnalyzer().Analyze(context.Background(), filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	var docPoints []storage.Point
	store.UpsertFunc = func(_ context.Context, _ string, batch []storage.Point) (*storage.UpdateResult, error) {
		docPoints = append(docPoints, batch...)
		return nil, nil
	}
	if err := indexer.NewService(&mockLLMProvider{}, store).IndexItems(context.Background(), "exports-docs", parsed.Symbols); err != nil {
		t.Fatal(err)
	}
	goSearch := store.ExactSearchFunc
	store.ExactSearchFunc = func(ctx context.Context, collection string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
		if strings.HasSuffix(collection, "-go") {
			return goSearch(ctx, collection, filters, limit)
		}
		var found []storage.SearchResult
		for _, point := range docPoints {
			if strings.HasSuffix(collection, "-docs") && point.Payload["package"] == filters["package"] {
				found = append(found, storage.SearchResult{Point: point, Score: 1})
			}
		}
		return found, nil
	}
	for _, request := range []string{"pflag/", "example.com/pflag/"} {
		got := listExports(t, store, request, filepath.Join(root, "flag.go"))
		if len(got) != 1 || len(got["function"]) != 1 || got["function"][0].Name != "Parse" {
			t.Errorf("%s exports = %v, want [Parse]", request, got)
		}
	}
}

// During a first index run the tool said "not indexed yet, indexing is
// required" and then answered "success: no exported symbols" or a short list
// as complete, so a client sent a redundant index request or took a partial
// answer as final.
func TestPackageExportsReportRunningIndex(t *testing.T) {
	dir, store := indexFixture(t, "go", golang.NewCodeAnalyzer(), map[string]string{
		"flag.go": "package pflag\n\nfunc Parse() {}\n",
	})
	indexer.SaveIndexStatus(dir, &indexer.IndexStatus{StartedAt: "2026-01-01T00:00:00Z"})
	eng := setupTestEngine(store)
	t.Cleanup(eng.Close)
	// The run has not created its first collection yet.
	empty := setupTestEngine(&mockVectorStore{CollectionExistsFunc: func(context.Context, string) (bool, error) { return false, nil }})
	t.Cleanup(empty.Close)
	exports := func(tool *tools.ListPackageExportsTool, pkg string) tools.ToolResponse {
		text, err := tool.Execute(context.Background(), map[string]interface{}{"package": pkg, "file_path": filepath.Join(dir, "flag.go")})
		if err != nil {
			t.Fatal(err)
		}
		var response tools.ToolResponse
		if err := json.Unmarshal([]byte(text), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}

	if got := exports(tools.NewListPackageExportsTool(eng), "pflag"); got.Status != "success" || got.Warning == "" {
		t.Errorf("partial exports: status %q warning %q, want success with a partial-index warning", got.Status, got.Warning)
	}
	if got := exports(tools.NewListPackageExportsTool(eng), "notyet"); got.Status != "indexing_in_progress" {
		t.Errorf("package not reached yet: status %q (%s), want indexing_in_progress", got.Status, got.Message)
	}
	if got := exports(tools.NewListPackageExportsTool(empty), "pflag"); got.Status != "indexing_in_progress" {
		t.Errorf("before the first collection: status %q (%s), want indexing_in_progress", got.Status, got.Message)
	}
}

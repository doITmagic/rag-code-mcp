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

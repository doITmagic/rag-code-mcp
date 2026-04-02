package indexer

import (
	"os"
	"path/filepath"
	"testing"

	// Import parsers so they register via init()
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/css"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/docs"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/html"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/javascript"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/php"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// createFile is a test helper that creates a file with minimal content.
func createFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// test"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCountAllFiles_Breakdown(t *testing.T) {
	root := t.TempDir()
	svc := &Service{} // CountAllFiles doesn't need embedder or store

	// Create a mixed JS/TS workspace
	createFile(t, filepath.Join(root, "src", "app.ts"))
	createFile(t, filepath.Join(root, "src", "utils.ts"))
	createFile(t, filepath.Join(root, "src", "types.tsx"))
	createFile(t, filepath.Join(root, "src", "legacy.js"))
	createFile(t, filepath.Join(root, "src", "config.mjs"))
	createFile(t, filepath.Join(root, "src", "App.vue"))

	// Create Go files
	createFile(t, filepath.Join(root, "backend", "main.go"))
	createFile(t, filepath.Join(root, "backend", "handler.go"))

	// Create docs
	createFile(t, filepath.Join(root, "README.md"))
	createFile(t, filepath.Join(root, "config.yaml"))

	// Create PHP
	createFile(t, filepath.Join(root, "web", "index.php"))

	result := svc.CountAllFiles(root, nil)

	// Verify total counts
	if result.Counts["javascript"] != 6 {
		t.Errorf("javascript count: got %d, want 6", result.Counts["javascript"])
	}
	if result.Counts["go"] != 2 {
		t.Errorf("go count: got %d, want 2", result.Counts["go"])
	}
	if result.Counts["docs"] != 2 {
		t.Errorf("docs count: got %d, want 2", result.Counts["docs"])
	}
	if result.Counts["php"] != 1 {
		t.Errorf("php count: got %d, want 1", result.Counts["php"])
	}

	// Verify JavaScript breakdown — the key test!
	jsBd := result.Breakdowns["javascript"]
	if jsBd == nil {
		t.Fatal("expected javascript breakdown to be non-nil")
	}
	if jsBd[".ts"] != 2 {
		t.Errorf("javascript .ts: got %d, want 2", jsBd[".ts"])
	}
	if jsBd[".tsx"] != 1 {
		t.Errorf("javascript .tsx: got %d, want 1", jsBd[".tsx"])
	}
	if jsBd[".js"] != 1 {
		t.Errorf("javascript .js: got %d, want 1", jsBd[".js"])
	}
	if jsBd[".mjs"] != 1 {
		t.Errorf("javascript .mjs: got %d, want 1", jsBd[".mjs"])
	}
	if jsBd[".vue"] != 1 {
		t.Errorf("javascript .vue: got %d, want 1", jsBd[".vue"])
	}

	// Verify Go breakdown (single extension)
	goBd := result.Breakdowns["go"]
	if goBd[".go"] != 2 {
		t.Errorf("go .go: got %d, want 2", goBd[".go"])
	}

	// Verify docs breakdown
	docsBd := result.Breakdowns["docs"]
	if docsBd[".md"] != 1 {
		t.Errorf("docs .md: got %d, want 1", docsBd[".md"])
	}
	if docsBd[".yaml"] != 1 {
		t.Errorf("docs .yaml: got %d, want 1", docsBd[".yaml"])
	}
}

func TestCountAllFiles_ExcludePatterns(t *testing.T) {
	root := t.TempDir()
	svc := &Service{}

	createFile(t, filepath.Join(root, "src", "app.ts"))
	createFile(t, filepath.Join(root, "src", "utils.js"))
	createFile(t, filepath.Join(root, "dist", "bundle.js"))  // should be excluded
	createFile(t, filepath.Join(root, "build", "output.js")) // should be excluded

	result := svc.CountAllFiles(root, []string{"dist", "build"})

	if result.Counts["javascript"] != 2 {
		t.Errorf("javascript count with excludes: got %d, want 2", result.Counts["javascript"])
	}

	jsBd := result.Breakdowns["javascript"]
	if jsBd[".ts"] != 1 {
		t.Errorf("javascript .ts with excludes: got %d, want 1", jsBd[".ts"])
	}
	if jsBd[".js"] != 1 {
		t.Errorf("javascript .js with excludes: got %d, want 1", jsBd[".js"])
	}
}

func TestCountAllFiles_EmptyDir(t *testing.T) {
	root := t.TempDir()
	svc := &Service{}

	result := svc.CountAllFiles(root, nil)

	if len(result.Counts) != 0 {
		t.Errorf("expected empty counts for empty dir, got %v", result.Counts)
	}
	if len(result.Breakdowns) != 0 {
		t.Errorf("expected empty breakdowns for empty dir, got %v", result.Breakdowns)
	}
}

func TestCountAllFiles_NodeModulesExcluded(t *testing.T) {
	root := t.TempDir()
	svc := &Service{}

	createFile(t, filepath.Join(root, "src", "app.ts"))
	createFile(t, filepath.Join(root, "node_modules", "lib", "index.js")) // auto-excluded

	result := svc.CountAllFiles(root, nil)

	if result.Counts["javascript"] != 1 {
		t.Errorf("expected 1 (node_modules excluded), got %d", result.Counts["javascript"])
	}
	if result.Breakdowns["javascript"][".ts"] != 1 {
		t.Errorf("expected .ts=1, got %d", result.Breakdowns["javascript"][".ts"])
	}
}

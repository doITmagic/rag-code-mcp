package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
)

func TestCorruptRegistryDoesNotPanicWorkspaceDetection(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	if err := os.WriteFile(registryPath, []byte("{truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(nil, nil, registryPath, &config.Config{})
	defer eng.Close()

	wctx, err := eng.DetectContextAsRoot(context.Background(), root, false)
	if err != nil {
		t.Fatalf("DetectContextAsRoot: %v", err)
	}
	if wctx.Root != root {
		t.Fatalf("root = %q, want %q", wctx.Root, root)
	}
}

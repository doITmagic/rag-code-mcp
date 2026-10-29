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

func TestAllowedWorkspacePathsRejectOutsideRoots(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	for _, dir := range []string{allowed, outside} {
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AllowedWorkspacePaths: []string{allowed}}}
	eng := NewEngine(nil, nil, filepath.Join(t.TempDir(), "registry.json"), cfg)
	defer eng.Close()

	if _, err := eng.DetectContextAsRoot(context.Background(), allowed, false); err != nil {
		t.Fatalf("allowed root rejected: %v", err)
	}
	if _, err := eng.DetectContextAsRoot(context.Background(), outside, false); err == nil {
		t.Fatal("root outside allowed_workspace_paths was accepted")
	}
	if _, err := eng.DetectContext(context.Background(), filepath.Join(outside, "main.go")); err == nil {
		t.Fatal("file path outside allowed_workspace_paths was accepted")
	}
}

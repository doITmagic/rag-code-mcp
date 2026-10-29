package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/registry"
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

// A workspace registered before allowed_workspace_paths was narrowed must not
// block the registry fallback: the newest allowed workspace answers instead.
func TestRegistryFallbackSkipsWorkspaceOutsideAllowedPaths(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	for _, dir := range []string{allowed, outside} {
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	reg, err := registry.New(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{allowed, outside} {
		if err := reg.RegisterWorkspace(root, filepath.Base(root), "test"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AllowedWorkspacePaths: []string{allowed}}}
	eng := NewEngine(nil, nil, registryPath, cfg)
	defer eng.Close()

	wctx, err := eng.DetectContext(context.Background(), "")
	if err != nil {
		t.Fatalf("pathless detection: %v", err)
	}
	if wctx.Root != allowed || wctx.DetectionSource != "registry_fallback" {
		t.Fatalf("fallback = %s (%s), want %s", wctx.Root, wctx.DetectionSource, allowed)
	}
}

func TestAutoTriggerQueuesRunWhileWorkspaceIsBusy(t *testing.T) {
	cfg := &config.Config{Workspace: config.WorkspaceConfig{AutoIndex: true}}
	eng := NewEngine(nil, nil, "", cfg)
	defer eng.Close()
	// A watcher batch is running when the retry after an aborted run arrives.
	eng.indexingJobs.Store("ws", time.Now())

	eng.autoTriggerIndex(&WorkspaceContext{Root: t.TempDir(), ID: "ws"})

	eng.pendingMu.Lock()
	defer eng.pendingMu.Unlock()
	if !eng.pendingOverflow["ws"] {
		t.Fatal("trigger was dropped instead of queued behind the running job")
	}
}

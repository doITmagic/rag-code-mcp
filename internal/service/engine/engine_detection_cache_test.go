package engine

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/transport"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/registry"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"
)

func TestDetectContextCacheHitSetsWorkspaceHeader(t *testing.T) {
	root := t.TempDir()
	eng := &Engine{
		config: &config.Config{},
		resolver: resolver.New(resolver.Dependencies{
			Detector: &mockDirDetector{root: root},
		}),
	}
	path := filepath.Join(root, "main.go")
	for i := 0; i < 2; i++ {
		writer := httptest.NewRecorder()
		ctx := transport.WithResponseWriter(context.Background(), writer)
		if _, err := eng.DetectContext(ctx, path); err != nil {
			t.Fatal(err)
		}
		if got := writer.Header().Get("X-Resolved-Workspace"); got != root {
			t.Fatalf("request %d: workspace header = %q, want %q", i, got, root)
		}
	}
}

func TestDetectContextRegistryFallbackDoesNotOverrideClientHint(t *testing.T) {
	fallbackRoot, hintedRoot := t.TempDir(), t.TempDir()
	reg, err := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterWorkspace(fallbackRoot, "fallback", "test"); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{
		config: &config.Config{},
		resolver: resolver.New(resolver.Dependencies{
			Detector: &mockDirDetector{root: hintedRoot},
			Registry: reg,
		}),
	}
	for _, test := range []struct {
		hint string
		want string
	}{
		{"", fallbackRoot},
		{hintedRoot, hintedRoot},
		{"", fallbackRoot},
		{hintedRoot, hintedRoot},
	} {
		ctx := transport.WithWorkspaceHint(context.Background(), test.hint)
		wctx, err := eng.DetectContext(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if wctx.Root != test.want {
			t.Fatalf("hint %q: root = %q, want %q", test.hint, wctx.Root, test.want)
		}
	}
}

// A registry fallback is a guess: echoing it as X-Resolved-Workspace makes the
// adapter send it back as a confirmed root, which drops the fallback warning.
func TestDetectContextRegistryFallbackIsNotConfirmed(t *testing.T) {
	fallbackRoot := t.TempDir()
	reg, err := registry.New(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterWorkspace(fallbackRoot, "fallback", "test"); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{
		config: &config.Config{},
		resolver: resolver.New(resolver.Dependencies{
			Detector: &mockDirDetector{root: fallbackRoot},
			Registry: reg,
		}),
	}
	writer := httptest.NewRecorder()
	wctx, err := eng.DetectContext(transport.WithResponseWriter(context.Background(), writer), "")
	if err != nil {
		t.Fatal(err)
	}
	if wctx.Root != fallbackRoot || wctx.DetectionSource != "registry_fallback" {
		t.Fatalf("fallback resolution = %+v", wctx)
	}
	if got := writer.Header().Get("X-Resolved-Workspace"); got != "" {
		t.Fatalf("registry fallback was confirmed as sticky workspace %q", got)
	}
}

// A relative file_path belongs to the client's workspace, not to whatever
// project the daemon process happened to be started in.
func TestDetectContextRelativePathUsesClientRoot(t *testing.T) {
	daemonCwd, clientRoot := t.TempDir(), t.TempDir()
	for _, dir := range []string{daemonCwd, clientRoot} {
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(daemonCwd)
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	eng := NewEngine(nil, nil, registryPath, &config.Config{})
	ctx := transport.WithWorkspaceHint(context.Background(), clientRoot)
	wctx, err := eng.DetectContext(ctx, "main.go")
	if err != nil {
		t.Fatal(err)
	}
	if wctx.Root != clientRoot {
		t.Fatalf("relative path resolved to %q, want client root %q", wctx.Root, clientRoot)
	}
}

// A deleted or moved workspace that is still the newest registry entry must
// fail with an error that tells the agent how to recover.
func TestDetectContextMissingFallbackRootIsActionable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	reg, err := registry.New(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterWorkspace(root, "gone", "test"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(nil, nil, registryPath, &config.Config{})
	_, err = eng.DetectContext(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "file_path") || strings.Contains(err.Error(), "git") {
		t.Fatalf("missing fallback root error = %v, want an actionable file_path hint", err)
	}
}

func TestDetectContextHintResolvesOnEveryRequest(t *testing.T) {
	hint, changedRoot := t.TempDir(), t.TempDir()
	detector := &mockDirDetector{root: hint}
	eng := &Engine{
		config: &config.Config{},
		resolver: resolver.New(resolver.Dependencies{
			Detector: detector,
		}),
	}
	// An explicit request for the same path must not cache subsequent hinted requests.
	if _, err := eng.DetectContext(context.Background(), hint); err != nil {
		t.Fatal(err)
	}
	ctx := transport.WithWorkspaceHint(context.Background(), hint)
	for _, root := range []string{hint, changedRoot} {
		detector.root = root
		wctx, err := eng.DetectContext(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if wctx.Root != root {
			t.Fatalf("hinted request used cached root %q, want newly resolved %q", wctx.Root, root)
		}
	}
}

func TestDetectContextMissingStickyRootDoesNotRebind(t *testing.T) {
	root, otherRoot := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(t.TempDir(), "registry.json")
	reg, err := registry.New(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterWorkspace(otherRoot, "other", "test"); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(nil, nil, registryPath, &config.Config{})
	ctx := transport.WithWorkspaceHint(context.Background(), root)
	if got, err := eng.DetectContext(ctx, ""); err != nil || got.Root != root {
		t.Fatalf("initial resolution = %+v, %v", got, err)
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	writer := httptest.NewRecorder()
	if got, err := eng.DetectContext(transport.WithResponseWriter(ctx, writer), ""); err == nil {
		t.Fatalf("missing sticky workspace silently rebound to %+v", got)
	}
	if got := writer.Header().Get("X-Resolved-Workspace"); got != "" {
		t.Fatalf("failed resolution must not change sticky workspace, got header %q", got)
	}
	if got, err := eng.DetectContext(context.Background(), ""); err != nil || got.Root != otherRoot {
		t.Fatalf("unbound client registry fallback = %+v, %v", got, err)
	}
}

package engine

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
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

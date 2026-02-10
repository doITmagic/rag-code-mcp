package tools

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

func TestExtractFilePathFromParams(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]interface{}
		expected string
	}{
		{
			name:     "empty params",
			params:   map[string]interface{}{},
			expected: "",
		},
		{
			name:     "file_path present",
			params:   map[string]interface{}{"file_path": "/home/user/project/main.go"},
			expected: "/home/user/project/main.go",
		},
		{
			name:     "filePath camelCase",
			params:   map[string]interface{}{"filePath": "/home/user/project/main.go"},
			expected: "/home/user/project/main.go",
		},
		{
			name:     "path param",
			params:   map[string]interface{}{"path": "/home/user/project/main.go"},
			expected: "/home/user/project/main.go",
		},
		{
			name:     "empty string value",
			params:   map[string]interface{}{"file_path": ""},
			expected: "",
		},
		{
			name:     "non-string value",
			params:   map[string]interface{}{"file_path": 123},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFilePathFromParams(tt.params)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestResolveFilePathWithFallback_ExplicitPath(t *testing.T) {
	params := map[string]interface{}{"file_path": "/home/user/project/main.go"}
	result := resolveFilePathWithFallback(params, nil, "test_tool")
	if result != "/home/user/project/main.go" {
		t.Fatalf("expected explicit path, got %q", result)
	}
}

func TestResolveFilePathWithFallback_NilManager(t *testing.T) {
	params := map[string]interface{}{}
	result := resolveFilePathWithFallback(params, nil, "test_tool")
	if result != "" {
		t.Fatalf("expected empty string with nil manager, got %q", result)
	}
}

func TestResolveFilePathWithFallback_SingleWorkspaceFallback(t *testing.T) {
	// Create a real workspace manager with a cache containing one workspace
	cache := workspace.NewCache(5 * time.Minute)
	cache.Set("/home/user/project/main.go", &workspace.Info{Root: "/home/user/project"})

	mgr := workspace.NewManagerForTest(cache)

	params := map[string]interface{}{}
	result := resolveFilePathWithFallback(params, mgr, "test_tool")

	if result != "/home/user/project" {
		t.Fatalf("expected fallback to /home/user/project, got %q", result)
	}

	// Verify file_path was injected into params
	if params["file_path"] != "/home/user/project" {
		t.Fatalf("expected file_path injected into params, got %v", params["file_path"])
	}
}

func TestResolveFilePathWithFallback_MultipleWorkspaces_ReturnsEmpty(t *testing.T) {
	cache := workspace.NewCache(5 * time.Minute)
	cache.Set("/home/user/project-a/main.go", &workspace.Info{Root: "/home/user/project-a"})
	cache.Set("/home/user/project-b/main.go", &workspace.Info{Root: "/home/user/project-b"})

	mgr := workspace.NewManagerForTest(cache)

	params := map[string]interface{}{}
	result := resolveFilePathWithFallback(params, mgr, "test_tool")

	if result != "" {
		t.Fatalf("expected empty string for multiple workspaces, got %q", result)
	}

	// Verify file_path was NOT injected
	if _, ok := params["file_path"]; ok {
		t.Fatalf("file_path should not be injected when multiple workspaces exist")
	}
}

func TestResolveFilePathWithFallback_EmptyCache_ReturnsEmpty(t *testing.T) {
	cache := workspace.NewCache(5 * time.Minute)
	mgr := workspace.NewManagerForTest(cache)

	params := map[string]interface{}{}
	result := resolveFilePathWithFallback(params, mgr, "test_tool")

	if result != "" {
		t.Fatalf("expected empty string for empty cache, got %q", result)
	}
}

func TestResolveFilePathWithFallback_ExplicitPathTakesPriority(t *testing.T) {
	cache := workspace.NewCache(5 * time.Minute)
	cache.Set("/home/user/cached-project/main.go", &workspace.Info{Root: "/home/user/cached-project"})

	mgr := workspace.NewManagerForTest(cache)

	params := map[string]interface{}{"file_path": "/home/user/explicit-project/main.go"}
	result := resolveFilePathWithFallback(params, mgr, "test_tool")

	if result != "/home/user/explicit-project/main.go" {
		t.Fatalf("expected explicit path to take priority, got %q", result)
	}
}

// TestEndToEnd_DetectWorkspace_WithoutFilePath tests the full flow:
// resolveFilePathWithFallback returns "" → DetectWorkspace → DetectFromParams → CWD fallback
func TestEndToEnd_DetectWorkspace_WithoutFilePath(t *testing.T) {
	// Create a temporary workspace with a marker
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "test-project")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "go.mod"), []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create detector with allowed paths
	detector := workspace.NewDetector()
	detector.SetAllowedPaths([]string{tmpDir})

	// Test DetectFromParams with empty params (will use CWD fallback)
	// Note: CWD fallback may not point to our test workspace, but the important thing
	// is that it doesn't panic or return a hard error about missing file_path
	params := map[string]interface{}{}

	// resolveFilePathWithFallback should return "" without error
	result := resolveFilePathWithFallback(params, nil, "test_tool")
	if result != "" {
		t.Fatalf("expected empty string, got %q", result)
	}

	// DetectFromParams should still work (uses CWD fallback)
	// We can't guarantee CWD is in our test workspace, but it should not panic
	_, err := detector.DetectFromParams(params)
	// Error is acceptable here (CWD may not be a valid workspace), but it should
	// be a workspace detection error, not a "file_path required" error
	if err != nil {
		if contains(err.Error(), "file_path parameter is required") {
			t.Fatalf("should not get 'file_path required' error, got: %v", err)
		}
		// Other errors (like CWD not being a valid workspace) are fine
		t.Logf("DetectFromParams with empty params returned expected error: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

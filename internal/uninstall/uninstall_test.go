package uninstall

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestExtractWorkspaceRoots_V2(t *testing.T) {
	data := []byte(`{
		"version": "v2",
		"entries": [
			{"root": "/home/user/project-a", "id": "abc123"},
			{"root": "/home/user/project-b", "id": "def456"},
			{"root": "/opt/workspace/app",   "id": "ghi789"}
		],
		"candidates": []
	}`)

	roots := extractWorkspaceRoots(data)
	if len(roots) != 3 {
		t.Fatalf("expected 3 roots, got %d: %v", len(roots), roots)
	}

	sort.Strings(roots)
	expected := []string{"/home/user/project-a", "/home/user/project-b", "/opt/workspace/app"}
	sort.Strings(expected)

	for i, r := range roots {
		if r != expected[i] {
			t.Errorf("root[%d] = %q, want %q", i, r, expected[i])
		}
	}
}

func TestExtractWorkspaceRoots_V1(t *testing.T) {
	data := []byte(`[
		{"root": "/home/user/proj1", "id": "a1"},
		{"root": "/home/user/proj2", "id": "b2"}
	]`)

	roots := extractWorkspaceRoots(data)
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %d: %v", len(roots), roots)
	}

	sort.Strings(roots)
	if roots[0] != "/home/user/proj1" || roots[1] != "/home/user/proj2" {
		t.Errorf("unexpected roots: %v", roots)
	}
}

func TestExtractWorkspaceRoots_LegacyFlatMap(t *testing.T) {
	data := []byte(`{
		"/home/user/old-project": {"name": "old"},
		"/var/www/site": {"name": "site"}
	}`)

	roots := extractWorkspaceRoots(data)
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %d: %v", len(roots), roots)
	}

	sort.Strings(roots)
	if roots[0] != "/home/user/old-project" || roots[1] != "/var/www/site" {
		t.Errorf("unexpected roots: %v", roots)
	}
}

func TestExtractWorkspaceRoots_EmptyV2(t *testing.T) {
	data := []byte(`{"version": "v2", "entries": []}`)

	roots := extractWorkspaceRoots(data)
	if roots != nil {
		t.Errorf("expected nil for empty V2, got %v", roots)
	}
}

func TestExtractWorkspaceRoots_InvalidJSON(t *testing.T) {
	data := []byte(`{not valid json at all`)

	roots := extractWorkspaceRoots(data)
	if roots != nil {
		t.Errorf("expected nil for invalid JSON, got %v", roots)
	}
}

func TestExtractWorkspaceRoots_V2SkipsEmptyRoots(t *testing.T) {
	data := []byte(`{
		"version": "v2",
		"entries": [
			{"root": "/valid/path", "id": "x"},
			{"root": "",            "id": "y"},
			{"root": "/another",    "id": "z"}
		]
	}`)

	roots := extractWorkspaceRoots(data)
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots (skipping empty), got %d: %v", len(roots), roots)
	}
}

func TestCleanWorkspaceData_WithV2Registry(t *testing.T) {
	// Create a temp "home" directory
	home := t.TempDir()

	// Create fake .ragcode install dir with registry
	installDir := filepath.Join(home, ".ragcode")
	if err := os.MkdirAll(installDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create two fake project directories with .ragcode inside
	proj1 := filepath.Join(home, "projects", "app1")
	proj2 := filepath.Join(home, "projects", "app2")
	proj3 := filepath.Join(home, "projects", "app3") // not in registry

	for _, p := range []string{proj1, proj2, proj3} {
		ragDir := filepath.Join(p, ".ragcode")
		if err := os.MkdirAll(ragDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Create a file inside to verify full removal
		if err := os.WriteFile(filepath.Join(ragDir, "state.json"), []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Write V2 registry with proj1 and proj2 (NOT proj3)
	registry := map[string]interface{}{
		"version": "v2",
		"entries": []map[string]string{
			{"root": proj1, "id": "aaa"},
			{"root": proj2, "id": "bbb"},
		},
	}
	regData, _ := json.MarshalIndent(registry, "", "  ")
	if err := os.WriteFile(filepath.Join(installDir, "registry.json"), regData, 0644); err != nil {
		t.Fatal(err)
	}

	// Run the function
	cleanWorkspaceData(home)

	// proj1/.ragcode should be gone
	if _, err := os.Stat(filepath.Join(proj1, ".ragcode")); !os.IsNotExist(err) {
		t.Errorf("proj1/.ragcode should have been removed")
	}

	// proj2/.ragcode should be gone
	if _, err := os.Stat(filepath.Join(proj2, ".ragcode")); !os.IsNotExist(err) {
		t.Errorf("proj2/.ragcode should have been removed")
	}

	// proj3/.ragcode should be gone too (cleaned by fallback scan)
	if _, err := os.Stat(filepath.Join(proj3, ".ragcode")); !os.IsNotExist(err) {
		t.Errorf("proj3/.ragcode should have been removed by fallback scan")
	}
}

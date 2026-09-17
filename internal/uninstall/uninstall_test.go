package uninstall

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/utils"
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
	isolate(t)

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

// isolate keeps cleanWorkspaceData inside the test's temp dirs: without it
// the sweep queried the live Qdrant and the real IDE configs and deleted
// .ragcode directories on the developer's machine, ~/.ragcode included.
func isolate(t *testing.T) {
	t.Helper()
	origQ, origI := qdrantRootsFn, ideProjectParentsFn
	qdrantRootsFn = func() []string { return nil }
	ideProjectParentsFn = func(string) []string { return nil }
	t.Cleanup(func() { qdrantRootsFn, ideProjectParentsFn = origQ, origI })
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
}

func TestCleanupReadsCanonicalRegistry(t *testing.T) {
	isolate(t)
	home, workspace := t.TempDir(), t.TempDir()
	cache := filepath.Join(workspace, ".ragcode")
	if err := os.MkdirAll(cache, 0755); err != nil {
		t.Fatal(err)
	}
	registry := utils.GetRegistryPath()
	if err := os.MkdirAll(filepath.Dir(registry), 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]interface{}{"version": "v2", "entries": []map[string]string{{"root": workspace}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, data, 0600); err != nil {
		t.Fatal(err)
	}
	cleanWorkspaceData(home)
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatalf("canonical registry workspace not cleaned: %v", err)
	}
}

// A .ragcode holding bin/ is the installation, never workspace cache: the
// sweep must leave it alone even when it sits under a scan root.
func TestScanSkipsInstallDirAndUnsafeRoots(t *testing.T) {
	isolate(t)
	home := t.TempDir()
	install := filepath.Join(home, ".ragcode")
	if err := os.MkdirAll(filepath.Join(install, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(home, "projects", "app")
	if err := os.MkdirAll(filepath.Join(ws, ".ragcode"), 0o755); err != nil {
		t.Fatal(err)
	}

	// registry root under home → its parent (home/projects) is scanned; home
	// itself is scanned at depth 1; home's parent must be refused.
	scanAndCleanRagcodeDirs(home, []string{ws, filepath.Join(filepath.Dir(home), "x", "y")})

	if _, err := os.Stat(filepath.Join(install, "bin")); err != nil {
		t.Fatal("install dir was removed by the sweep")
	}
	if _, err := os.Stat(filepath.Join(ws, ".ragcode")); !os.IsNotExist(err) {
		t.Fatal("workspace .ragcode should have been removed")
	}
}

// A file whose project is gone must not resolve to $HOME via the install
// dir marker.
func TestFindWorkspaceRootNeverReturnsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.MkdirAll(filepath.Join(home, ".ragcode", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findWorkspaceRootFromFilePath(filepath.Join(home, "gone", "project", "main.go")); got != "" {
		t.Fatalf("root = %q, want none", got)
	}
	repo := filepath.Join(home, "code", "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findWorkspaceRootFromFilePath(filepath.Join(repo, "pkg", "a.go")); got != repo {
		t.Fatalf("root = %q, want %q", got, repo)
	}
}

func TestRemoveRagcodeFromJSONPreservesOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte("\xef\xbb\xbf" + `{"counter":9007199254740993,"mcpServers":{"ragcode":{"command":"old"},"other":{"command":"keep"}},"projects":{"A":{},"a":{}}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	removeRagcodeFromJSON("test", path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var cfg map[string]interface{}
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	servers := cfg["mcpServers"].(map[string]interface{})
	if servers["ragcode"] != nil || servers["other"].(map[string]interface{})["command"] != "keep" {
		t.Fatalf("wrong servers: %v", servers)
	}
	if cfg["counter"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("number changed: %v", cfg["counter"])
	}
}

func TestUninstallIntegrationPathsMatchInstaller(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home := t.TempDir()
	paths := resolveIDEPaths(home)
	if paths["codex"].path != filepath.Join(home, ".codex", "config.toml") {
		t.Fatal(paths["codex"].path)
	}
	if paths["antigravity"].path != filepath.Join(home, ".gemini", "config", "mcp_config.json") {
		t.Fatal(paths["antigravity"].path)
	}
	if paths["claude-cli"].path != filepath.Join(home, ".claude.json") {
		t.Fatal(paths["claude-cli"].path)
	}
}

func TestRemoveCodexEntryPreservesOtherServer(t *testing.T) {
	if findCodexCLI(t.TempDir()) == "" {
		t.Skip("Codex CLI required")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	config := "[mcp_servers.other]\ncommand = \"keep\"\n\n[mcp_servers.ragcode]\ncommand = \"remove\"\n"
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	removeCodexEntry(t.TempDir(), path)
	data, _ := os.ReadFile(path)
	if bytes.Contains(data, []byte("mcp_servers.ragcode")) || !bytes.Contains(data, []byte("mcp_servers.other")) {
		t.Fatalf("unexpected config: %s", data)
	}
}

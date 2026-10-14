package detector

import (
	context "context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
)

func evalTempDir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(tmp); err == nil {
		return resolved
	}
	return tmp
}

func TestDetectFromFilePathMarkers(t *testing.T) {
	tmp := evalTempDir(t)
	markerDir := filepath.Join(tmp, "project")
	if err := os.Mkdir(markerDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, ".git"), []byte{}, 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("file: %v", err)
	}

	det := New(DefaultOptions())
	resp, err := det.DetectFromFilePath(context.Background(), filepath.Join(markerDir, "main.go"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Root != markerDir {
		t.Fatalf("expected root %s, got %s", markerDir, resp.Root)
	}
	if len(resp.Markers) == 0 {
		t.Fatalf("expected markers, got none")
	}
}

func TestDetectMetadataFallback(t *testing.T) {
	tmp := evalTempDir(t)
	project := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(filepath.Join(project, ".ragcode"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// metadata points to sibling dir with marker
	target := filepath.Join(tmp, "actual")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "go.mod"), []byte("module example"), 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".ragcode", "root"), []byte(target), 0o644); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "dummy.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("dummy: %v", err)
	}

	det := New(DefaultOptions())
	resp, err := det.DetectFromFilePath(context.Background(), filepath.Join(project, "dummy.txt"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Root != target {
		t.Fatalf("expected root %s, got %s", target, resp.Root)
	}
	if resp.Reason != contract.ReasonRootsList {
		t.Fatalf("expected reason %s, got %s", contract.ReasonRootsList, resp.Reason)
	}
}

func TestDetectRespectsAllowedRoots(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "proj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.js"), []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatalf("main: %v", err)
	}
	outside := filepath.Join(tmp, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}

	det := New(Options{AllowedRoots: []string{outside}})
	if _, err := det.DetectFromFilePath(context.Background(), filepath.Join(project, "main.js")); err == nil || err.Code != contract.ErrorOutsideAllowedRoots {
		t.Fatalf("expected outside allowed roots error, got %v", err)
	}
}

func TestDetectExclusion(t *testing.T) {
	tmp := t.TempDir()
	project := filepath.Join(tmp, "proj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example"), 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}

	det := New(Options{ExcludePatterns: []string{"proj"}})
	if _, err := det.DetectFromFilePath(context.Background(), filepath.Join(project, "main.go")); err == nil {
		t.Fatalf("expected exclusion error")
	}
}

// IDE dirs such as ~/.claude are Tier-2 markers; a project with only a Tier-3
// marker (package.json) used to resolve to home because the walk kept climbing.
func TestDetectStopsBelowHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := filepath.Join(home, "proj")
	mustMkdir(t, filepath.Join(home, ".claude"))
	mustMkdir(t, filepath.Join(project, "src"))
	mustWrite(t, filepath.Join(project, "package.json"))

	resp, err := New(DefaultOptions()).DetectFromFilePath(context.Background(), filepath.Join(project, "src", "a.js"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Root != project {
		t.Fatalf("expected root %s, got %s", project, resp.Root)
	}

	// A file directly in home has no project: refuse rather than pick home.
	if resp, err := New(DefaultOptions()).DetectFromFilePath(context.Background(), filepath.Join(home, "notes.txt")); err == nil {
		t.Fatalf("expected error for a file in home, got root %s", resp.Root)
	}
}

// Exclude patterns match whole path components: ".git" must not exclude
// ".github", nor "build" exclude "buildkite".
func TestDetectExcludeMatchesComponents(t *testing.T) {
	project := filepath.Join(t.TempDir(), "buildkite")
	mustMkdir(t, filepath.Join(project, ".github", "workflows"))
	mustMkdir(t, filepath.Join(project, "node_modules", "x"))
	mustWrite(t, filepath.Join(project, "go.mod"))

	det := New(Options{ExcludePatterns: []string{".git", "build", "node_modules"}})
	resp, err := det.DetectFromFilePath(context.Background(), filepath.Join(project, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Root != project {
		t.Fatalf("expected root %s, got %s", project, resp.Root)
	}
	if _, err := det.DetectFromFilePath(context.Background(), filepath.Join(project, "node_modules", "x", "a.js")); err == nil {
		t.Fatalf("expected node_modules path to be excluded")
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestDetectNoMarkers(t *testing.T) {
	tmp := t.TempDir()
	// Use a nested subdirectory to avoid picking up .ragcode markers
	// left by other tests in /tmp/ (parent directory traversal).
	nested := filepath.Join(tmp, "isolated", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Restrict detection to the temp dir so it won't traverse to /tmp/.ragcode/
	det := New(Options{AllowedRoots: []string{tmp}})
	if _, err := det.DetectFromFilePath(context.Background(), filepath.Join(nested, "nope.go")); err == nil {
		t.Fatalf("expected error when no markers present")
	}
}

func TestDetectNonExistentFileParentFallback(t *testing.T) {
	// Setup: directory has .git marker but the requested file does NOT exist
	tmp := t.TempDir()
	project := filepath.Join(tmp, "myproject")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte{}, 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}
	// "main.go" does NOT exist on disk — simulates AI agent passing a non-existent file
	nonExistentFile := filepath.Join(project, "main.go")

	det := New(DefaultOptions())
	resp, err := det.DetectFromFilePath(context.Background(), nonExistentFile)
	if err != nil {
		t.Fatalf("expected fallback to parent directory, got error: %v", err)
	}
	if resp.Root != project {
		t.Fatalf("expected root %s, got %s", project, resp.Root)
	}
}

func TestDetectNonExistentFileDescriptiveError(t *testing.T) {
	// Both the file AND its parent directory don't exist
	det := New(DefaultOptions())
	_, err := det.DetectFromFilePath(context.Background(), "/completely/fake/path/main.go")
	if err == nil {
		t.Fatalf("expected error for fully invalid path")
	}
	if err.Code != contract.ErrorInvalidPath {
		t.Fatalf("expected ErrorInvalidPath, got %s", err.Code)
	}
	// Error message should contain AI-friendly guidance
	if !strings.Contains(err.Message, "workspace detection") {
		t.Fatalf("expected descriptive error with workspace detection hint, got: %s", err.Message)
	}
}

func TestDetectMonorepoGitRootPriority(t *testing.T) {
	// Level 0 has .git (Tier 1)
	tmp := evalTempDir(t)
	if err := os.WriteFile(filepath.Join(tmp, ".git"), []byte{}, 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}

	// Level 1 has go.mod (Tier 3)
	subDir1 := filepath.Join(tmp, "project_alpha")
	if err := os.MkdirAll(subDir1, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir1, "go.mod"), []byte("module project_alpha"), 0o644); err != nil {
		t.Fatalf("marker: %v", err)
	}

	// File inside Level 1
	targetFile := filepath.Join(subDir1, "main.go")
	if err := os.WriteFile(targetFile, []byte("package main"), 0o644); err != nil {
		t.Fatalf("file: %v", err)
	}

	det := New(DefaultOptions())
	resp, err := det.DetectFromFilePath(context.Background(), targetFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Validates the detector skips the Tier 3 directory and climbs to the Tier 1 VCS root.
	if resp.Root != tmp {
		t.Fatalf("expected tier 1 root %s, got tier 3 %s", tmp, resp.Root)
	}
}

func TestDetectSameTierPrefersOutermostRoot(t *testing.T) {
	tmp := evalTempDir(t)
	// Both parent and child have Tier 3 marker (package.json), no .git
	if err := os.WriteFile(filepath.Join(tmp, "package.json"), []byte(`{"name":"root"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(tmp, "packages", "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "package.json"), []byte(`{"name":"web"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(sub, "index.js")
	if err := os.WriteFile(targetFile, []byte("export default 1"), 0o644); err != nil {
		t.Fatal(err)
	}

	det := New(DefaultOptions())
	resp, err := det.DetectFromFilePath(context.Background(), targetFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Root != tmp {
		t.Fatalf("expected outermost tier-3 root %s, got %s", tmp, resp.Root)
	}
}

func TestDetectAllowedRootBoundaryDoesNotAbortValidChild(t *testing.T) {
	outer := evalTempDir(t)
	project := filepath.Join(outer, "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// Project only has Tier 3 marker (go.mod), so walkUp continues past `project` into `outer`
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/p"), 0o644); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(project, "main.go")
	if err := os.WriteFile(targetFile, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := DefaultOptions()
	opts.AllowedRoots = []string{project}
	det := New(opts)
	resp, err := det.DetectFromFilePath(context.Background(), targetFile)
	if err != nil {
		t.Fatalf("expected valid child candidate inside AllowedRoots to succeed, got error: %v", err)
	}
	if resp.Root != project {
		t.Fatalf("expected root %s, got %s", project, resp.Root)
	}
}


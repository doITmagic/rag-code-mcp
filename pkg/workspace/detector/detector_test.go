package detector

import (
	context "context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/workspace/contract"
)

func TestDetectFromFilePathMarkers(t *testing.T) {
	tmp := t.TempDir()
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
	tmp := t.TempDir()
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

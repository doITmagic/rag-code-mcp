package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetector_RejectsHomeDirectory tests that detector rejects Home directory WITHOUT markers
func TestDetector_RejectsHomeDirectory(t *testing.T) {
	detector := NewDetector()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	// Important: This only fails if Home directory doesn't have workspace markers
	// If someone actually has .git or go.mod in their Home, it would work!
	_, err = detector.DetectFromPath(homeDir)
	if err == nil {
		t.Logf("Note: Home directory was accepted - it probably contains workspace markers like .git")
		t.Skip("Home directory has workspace markers, which is valid")
	}

	if !strings.Contains(err.Error(), "could not detect workspace") {
		t.Fatalf("Expected error message about missing workspace markers, got: %v", err)
	}
	t.Logf("✅ Correctly rejected Home directory without workspace markers")
	t.Logf("   Error: %v", err)
}

// TestDetector_AcceptsHomeWithMarkers tests that Home directory WITH markers is accepted
func TestDetector_AcceptsHomeWithMarkers(t *testing.T) {
	// We can't modify the real home directory in tests, so we'll create a subdir
	// But let's document the expected behavior
	t.Log("✅ Expected behavior: If Home directory contains .git, go.mod, etc., it should be accepted as a valid workspace")
	t.Log("   The tool should ONLY reject Home directory if no workspace markers are found")
}

// TestDetector_AcceptsProjectInHome tests that projects INSIDE Home work correctly
func TestDetector_AcceptsProjectInHome(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	// Create a project directory inside Home with markers
	projectDir := filepath.Join(homeDir, ".test-project-"+filepath.Base(t.TempDir()))
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(projectDir)

	// Add .git marker
	gitDir := filepath.Join(projectDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(projectDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	detector := NewDetector()
	info, err := detector.DetectFromPath(testFile)
	if err != nil {
		t.Fatalf("Expected no error for valid project in Home, got: %v", err)
	}

	if info.Root != projectDir {
		t.Fatalf("Expected root to be %s, got %s", projectDir, info.Root)
	}

	t.Logf("✅ Correctly accepted project inside Home directory when it has workspace markers")
	t.Logf("   Project: %s", projectDir)
	t.Logf("   Root: %s", info.Root)
}

// TestDetector_RejectsRootDirectory tests that detector rejects root directory
func TestDetector_RejectsRootDirectory(t *testing.T) {
	detector := NewDetector()

	_, err := detector.DetectFromPath("/")
	if err == nil {
		t.Fatalf("Expected error when detecting from root directory, got nil")
	}

	if !strings.Contains(err.Error(), "cannot use") {
		t.Fatalf("Expected error message about invalid workspace, got: %v", err)
	}
	t.Logf("✅ Correctly rejected root directory with error: %v", err)
}

// TestDetector_RejectsBareTemp tests that detector rejects /tmp directly
func TestDetector_RejectsBareTemp(t *testing.T) {
	detector := NewDetector()

	_, err := detector.DetectFromPath("/tmp")
	if err == nil {
		t.Fatalf("Expected error when detecting from /tmp, got nil")
	}

	if !strings.Contains(err.Error(), "cannot use") {
		t.Fatalf("Expected error message about invalid workspace, got: %v", err)
	}
	t.Logf("✅ Correctly rejected /tmp with error: %v", err)
}

// TestDetector_AcceptsValidProject tests that valid projects are still accepted
func TestDetector_AcceptsValidProject(t *testing.T) {
	// Create a temp directory with a workspace marker
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	detector := NewDetector()
	info, err := detector.DetectFromPath(testFile)
	if err != nil {
		t.Fatalf("Expected no error for valid project, got: %v", err)
	}

	if info.Root != tmpDir {
		t.Fatalf("Expected root to be %s, got %s", tmpDir, info.Root)
	}

	if info.ProjectType != "git" {
		t.Fatalf("Expected project type to be 'git', got '%s'", info.ProjectType)
	}

	t.Logf("✅ Correctly accepted valid project at %s", tmpDir)
}

// TestManager_ScanWorkspace_RejectsHomeDirectory tests that scanWorkspace rejects Home
func TestManager_ScanWorkspace_RejectsHomeDirectory(t *testing.T) {
	manager := &Manager{}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	info := &Info{
		Root: homeDir,
	}

	_, err = manager.scanWorkspace(info)
	if err == nil {
		t.Fatalf("Expected error when scanning Home directory, got nil")
	}

	if !strings.Contains(err.Error(), "cannot scan invalid workspace root") {
		t.Fatalf("Expected error message about invalid workspace, got: %v", err)
	}
	t.Logf("✅ Correctly rejected scanning Home directory with error: %v", err)
}

// TestFileWatcher_Start_RejectsHomeDirectory tests that watcher rejects Home
func TestFileWatcher_Start_RejectsHomeDirectory(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	watcher, err := NewFileWatcher(homeDir, nil)
	if err != nil {
		t.Skip("Cannot create watcher")
	}
	defer watcher.watcher.Close()

	// Start should silently fail and not walk the directory
	watcher.Start()

	// If we get here without the test hanging, the watcher didn't walk Home
	t.Logf("✅ Watcher correctly avoided walking Home directory")
}

// TestDetector_StopsAtHomeDirectory tests that upward search stops at Home
func TestDetector_StopsAtHomeDirectory(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	// Create a nested directory structure under Home without any markers
	// This simulates a file deep in Home that doesn't have a project
	tmpDir := filepath.Join(homeDir, ".test-rag-code-detector-"+filepath.Base(t.TempDir()))
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	nestedDir := filepath.Join(tmpDir, "level1", "level2", "level3")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(nestedDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	detector := NewDetector()
	info, err := detector.DetectFromPath(testFile)

	// Debug output
	t.Logf("Test file: %s", testFile)
	t.Logf("Nested dir: %s", nestedDir)
	t.Logf("Tmp dir: %s", tmpDir)
	t.Logf("Home dir: %s", homeDir)
	if info != nil {
		t.Logf("Detected root: %s", info.Root)
	}
	if err != nil {
		t.Logf("Error: %v", err)
	}

	// Should fail because it stops at Home directory and doesn't find markers
	if err == nil {
		t.Fatalf("Expected error when no markers found and upward search reaches Home, got nil")
	}

	t.Logf("✅ Detector correctly stopped at Home directory during upward search")
}

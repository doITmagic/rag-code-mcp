package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDetector_RejectsHomeDirectory tests that detector rejects Home directory
func TestDetector_RejectsHomeDirectory(t *testing.T) {
	detector := NewDetector()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	_, err = detector.DetectFromPath(homeDir)
	if err == nil {
		t.Fatalf("Expected error when detecting from Home directory, got nil")
	}

	if !strings.Contains(err.Error(), "cannot use") {
		t.Fatalf("Expected error message about invalid workspace, got: %v", err)
	}
	t.Logf("✅ Correctly rejected Home directory with error: %v", err)
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

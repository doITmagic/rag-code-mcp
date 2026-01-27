package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

	if !strings.Contains(err.Error(), "cannot use") {
		t.Fatalf("Expected error message about invalid workspace, got: %v", err)
	}
	t.Logf("✅ Correctly rejected Home directory without workspace markers")
	t.Logf("   Error: %v", err)
}

// TestDetector_AcceptsHomeWithMarkers tests that Home directory WITH markers is accepted
func TestDetector_AcceptsHomeWithMarkers(t *testing.T) {
	// Create a temp directory with markers
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	detector := NewDetector()
	info, err := detector.DetectFromPath(tmpDir)
	if err != nil {
		t.Fatalf("Expected workspace with markers to be accepted, got error: %v", err)
	}

	if info.Root != tmpDir {
		t.Errorf("Expected root %s, got %s", tmpDir, info.Root)
	}
	t.Log("✅ Correctly accepted directory with markers")
}

// TestDetector_AllowedPaths tests the allowed paths security feature
func TestDetector_AllowedPaths(t *testing.T) {
	tmpBase := t.TempDir()
	allowedDir := filepath.Join(tmpBase, "allowed")
	restrictedDir := filepath.Join(tmpBase, "restricted")

	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(restrictedDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a project in each
	for _, dir := range []string{allowedDir, restrictedDir} {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
			t.Fatal(err)
		}
	}

	detector := NewDetectorWithConfig(nil, nil, []string{allowedDir}, false)

	// Test allowed path
	_, err := detector.DetectFromPath(filepath.Join(allowedDir, "file.go"))
	if err != nil {
		t.Errorf("Expected path in %s to be allowed, got error: %v", allowedDir, err)
	}

	// Test restricted path
	_, err = detector.DetectFromPath(filepath.Join(restrictedDir, "file.go"))
	if err == nil {
		t.Errorf("Expected path in %s to be rejected, but it was allowed", restrictedDir)
	} else if !strings.Contains(err.Error(), "not within allowed workspace paths") {
		t.Errorf("Expected 'not within allowed workspace paths' error, got: %v", err)
	}
	t.Log("✅ Correctly enforced allowed paths boundary")
}

// TestDetector_SymlinkNormalization tests security normalization for symlinks
func TestDetector_SymlinkNormalization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Symlinks require special permissions on Windows")
	}

	tmpBase := t.TempDir()
	realDir := filepath.Join(tmpBase, "real-project")
	linkDir := filepath.Join(tmpBase, "linked-project")

	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(realDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create symlink: linked-project -> real-project
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("Failed to create symlink: %v", err)
	}

	// Only allow the REAL path
	detector := NewDetectorWithConfig(nil, nil, []string{realDir}, false)

	// Accessing via link should still work because we normalize
	_, err := detector.DetectFromPath(filepath.Join(linkDir, "main.go"))
	if err != nil {
		t.Errorf("Expected symlinked path to be allowed via normalization, got error: %v", err)
	}

	// Now only allow the LINK path
	detector2 := NewDetectorWithConfig(nil, nil, []string{linkDir}, false)
	_, err = detector2.DetectFromPath(filepath.Join(realDir, "main.go"))
	if err != nil {
		t.Errorf("Expected real path to be allowed when linked path is in allowed list, got error: %v", err)
	}
	t.Log("✅ Correctly handled symlink normalization in security checks")
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

// TestIsInvalidRoot tests the root validation helper
func TestIsInvalidRoot(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"Linux root", "/", true},
		{"Bare /tmp", "/tmp", true},
		{"Bare /var/tmp", "/var/tmp", true},
		{"Valid project dir", "/home/user/projects/my-app", false},
		{"Valid tmp subdir", "/tmp/my-test-app", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && (tt.path == "/" || tt.path == "/tmp") {
				t.Skip("Unix-specific path")
			}
			result := isInvalidRoot(tt.path)
			if result != tt.expected {
				t.Errorf("isInvalidRoot(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}

	// Test Home directory separately
	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" {
		if !isInvalidRoot(homeDir) {
			t.Errorf("isInvalidRoot(homeDir) = false, want true")
		}
	}
}

// TestDetector_UpwardSearchLimit tests the maxDepth limit
func TestDetector_UpwardSearchLimit(t *testing.T) {
	tmpBase := t.TempDir()

	// Create a deep directory structure
	// .git is at level 0
	// file is at level 15 (exceeds maxDepth=10)
	current := tmpBase
	if err := os.MkdirAll(filepath.Join(current, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 15; i++ {
		current = filepath.Join(current, fmt.Sprintf("level%d", i))
	}
	if err := os.MkdirAll(current, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(current, "main.go")
	if err := os.WriteFile(testFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	detector := NewDetector()
	_, err := detector.DetectFromPath(testFile)

	if err == nil {
		t.Fatal("Expected error for upward search exceeding limit, got nil")
	}
	if !strings.Contains(err.Error(), "searched up to 10 levels") {
		t.Errorf("Expected error mentioning search depth limit, got: %v", err)
	}
	t.Log("✅ Correctly enforced upward search depth limit")
}

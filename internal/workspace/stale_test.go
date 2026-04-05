package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestFindDeletedRoot_FileOnlyDeleted(t *testing.T) {
	// Setup: create workspace/dir/subdir/ but NOT the file inside it
	wsRoot := t.TempDir()
	subdir := filepath.Join(wsRoot, "src", "pkg")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	// File that doesn't exist, but parent directory does
	missingFile := filepath.Join(subdir, "deleted.go")

	result := FindDeletedRoot(missingFile, wsRoot)
	if result != missingFile {
		t.Errorf("expected %q, got %q — when only the file is missing, should return the file path itself", missingFile, result)
	}
}

func TestFindDeletedRoot_DirectoryDeleted(t *testing.T) {
	// Setup: workspace root exists, but "tmp" directory does NOT
	wsRoot := t.TempDir()

	// Neither tmp/ nor its children exist
	missingFile := filepath.Join(wsRoot, "tmp", "vendor", "src", "foo.php")

	result := FindDeletedRoot(missingFile, wsRoot)
	expected := filepath.Join(wsRoot, "tmp")
	if result != expected {
		t.Errorf("expected %q, got %q — should return the highest deleted directory", expected, result)
	}
}

func TestFindDeletedRoot_IntermediateDirectoryDeleted(t *testing.T) {
	// Setup: workspace/src/ exists, but workspace/src/old/ does NOT
	wsRoot := t.TempDir()
	srcDir := filepath.Join(wsRoot, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	// src/ exists, but src/old/ and deeper don't
	missingFile := filepath.Join(wsRoot, "src", "old", "legacy", "file.go")

	result := FindDeletedRoot(missingFile, wsRoot)
	expected := filepath.Join(wsRoot, "src", "old")
	if result != expected {
		t.Errorf("expected %q, got %q — should return the highest deleted directory below existing parent", expected, result)
	}
}

func TestFindDeletedRoot_WorkspaceRootBoundary(t *testing.T) {
	// Ensure we never go above workspace root
	wsRoot := t.TempDir()

	// Everything under workspace root is deleted
	missingFile := filepath.Join(wsRoot, "a", "b", "c", "d.txt")

	result := FindDeletedRoot(missingFile, wsRoot)
	expected := filepath.Join(wsRoot, "a")
	if result != expected {
		t.Errorf("expected %q, got %q — should stop at direct child of workspace root", expected, result)
	}
}

func TestIsDirectoryDeletion(t *testing.T) {
	tests := []struct {
		deletedRoot  string
		originalFile string
		expected     bool
	}{
		{"/project/tmp", "/project/tmp/foo.php", true},
		{"/project/foo.go", "/project/foo.go", false},
		{"/project/src/old", "/project/src/old/deep/file.go", true},
	}

	for _, tt := range tests {
		result := IsDirectoryDeletion(tt.deletedRoot, tt.originalFile)
		if result != tt.expected {
			t.Errorf("IsDirectoryDeletion(%q, %q) = %v, want %v",
				tt.deletedRoot, tt.originalFile, result, tt.expected)
		}
	}
}

func TestGroupByDeletedRoot(t *testing.T) {
	wsRoot := t.TempDir()

	// Create src/ (exists) but not tmp/ (deleted)
	srcDir := filepath.Join(wsRoot, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}

	staleFiles := []string{
		filepath.Join(wsRoot, "tmp", "a", "file1.go"),
		filepath.Join(wsRoot, "tmp", "b", "file2.go"),
		filepath.Join(wsRoot, "src", "deleted.go"), // parent exists, only file missing
	}

	dirPrefixes, individualFiles := GroupByDeletedRoot(staleFiles, wsRoot)

	// tmp/ is the deleted root for both tmp files
	expectedPrefix := filepath.Join(wsRoot, "tmp") + string(os.PathSeparator)
	if _, ok := dirPrefixes[expectedPrefix]; !ok {
		t.Errorf("expected prefix %q in dirPrefixes, got: %v", expectedPrefix, dirPrefixes)
	}

	if len(dirPrefixes[expectedPrefix]) != 2 {
		t.Errorf("expected 2 files under prefix %q, got %d", expectedPrefix, len(dirPrefixes[expectedPrefix]))
	}

	sort.Strings(individualFiles)
	expectedIndividual := filepath.Join(wsRoot, "src", "deleted.go")
	if len(individualFiles) != 1 || individualFiles[0] != expectedIndividual {
		t.Errorf("expected individualFiles = [%q], got %v", expectedIndividual, individualFiles)
	}
}

func TestCollectStaleFiles(t *testing.T) {
	wsRoot := t.TempDir()

	// Create a real file
	existingFile := filepath.Join(wsRoot, "exists.go")
	if err := os.WriteFile(existingFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create state with both existing and non-existing files
	state := NewWorkspaceState()

	realInfo, _ := os.Stat(existingFile)
	state.UpdateFile(existingFile, realInfo)
	state.Files[filepath.Join(wsRoot, "deleted.go")] = FileState{}

	stale := CollectStaleFiles(state)

	if len(stale) != 1 {
		t.Fatalf("expected 1 stale file, got %d: %v", len(stale), stale)
	}
	if stale[0] != filepath.Join(wsRoot, "deleted.go") {
		t.Errorf("expected stale file %q, got %q", filepath.Join(wsRoot, "deleted.go"), stale[0])
	}
}

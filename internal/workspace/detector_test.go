package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDetector_DetectFromPath(t *testing.T) {
	// Create a temporary directory structure for testing
	tmpDir := t.TempDir()

	// Create a fake workspace with go.mod
	workspaceDir := filepath.Join(tmpDir, "test-project")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create go.mod marker
	goModPath := filepath.Join(workspaceDir, "go.mod")
	if err := os.WriteFile(goModPath, []byte("module test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a subdirectory with a file
	subDir := filepath.Join(workspaceDir, "internal", "pkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(subDir, "test.go")
	if err := os.WriteFile(testFile, []byte("package pkg"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test detection from deep file path
	detector := NewDetector()
	info, err := detector.DetectFromPath(testFile)
	if err != nil {
		t.Fatalf("DetectFromPath failed: %v", err)
	}

	// Verify workspace root
	if info.Root != workspaceDir {
		t.Errorf("Expected root %s, got %s", workspaceDir, info.Root)
	}

	// Verify project type
	if info.ProjectType != "go" {
		t.Errorf("Expected project type 'go', got '%s'", info.ProjectType)
	}

	// Verify markers
	if len(info.Markers) == 0 {
		t.Error("Expected markers to be found")
	}

	// Verify ID is generated
	if info.ID == "" {
		t.Error("Expected workspace ID to be generated")
	}

	// Verify ID is stable (same path = same ID)
	info2, _ := detector.DetectFromPath(testFile)
	if info.ID != info2.ID {
		t.Error("Expected stable workspace ID for same path")
	}
}

func TestDetector_DetectFromPath_NoMarkers(t *testing.T) {
	// Create a temporary directory without markers
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	detector := NewDetector()
	_, err := detector.DetectFromPath(testFile)
	if err == nil {
		t.Fatalf("expected error for path without workspace markers in temporary directory, got nil")
	}
	if !strings.Contains(err.Error(), "could not detect workspace") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestDetector_DetectFromParams(t *testing.T) {
	// Create test workspace
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

	tests := []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{
			name: "file_path parameter",
			params: map[string]interface{}{
				"file_path": testFile,
			},
			want: tmpDir,
		},
		{
			name: "filePath parameter",
			params: map[string]interface{}{
				"filePath": testFile,
			},
			want: tmpDir,
		},
		{
			name: "path parameter",
			params: map[string]interface{}{
				"path": testFile,
			},
			want: tmpDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := detector.DetectFromParams(tt.params)
			if err != nil {
				t.Fatalf("DetectFromParams failed: %v", err)
			}

			if info.Root != tt.want {
				t.Errorf("Expected root %s, got %s", tt.want, info.Root)
			}
		})
	}
}

func TestGenerateWorkspaceID(t *testing.T) {
	// Test ID stability
	path := "/home/user/project"
	id1 := generateWorkspaceID(path)
	id2 := generateWorkspaceID(path)

	if id1 != id2 {
		t.Error("Expected same ID for same path")
	}

	// Test ID uniqueness
	path2 := "/home/user/other-project"
	id3 := generateWorkspaceID(path2)

	if id1 == id3 {
		t.Error("Expected different IDs for different paths")
	}

	// Test ID length
	if len(id1) != 12 {
		t.Errorf("Expected ID length 12, got %d", len(id1))
	}
}

func TestInfo_CollectionName(t *testing.T) {
	tests := []struct {
		name     string
		info     *Info
		expected string
	}{
		{
			name: "default prefix",
			info: &Info{
				Root: "/home/user/project",
				ID:   "a3f4b8c9d2e1",
			},
			expected: "ragcode-a3f4b8c9d2e1",
		},
		{
			name: "custom prefix",
			info: &Info{
				Root:             "/home/user/myproject",
				ID:               "x1y2z3a4b5c6",
				CollectionPrefix: "myproject",
			},
			expected: "myproject-x1y2z3a4b5c6",
		},
		{
			name: "empty prefix uses default",
			info: &Info{
				Root:             "/home/user/test",
				ID:               "abc123",
				CollectionPrefix: "",
			},
			expected: "ragcode-abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.info.CollectionName()
			if got != tt.expected {
				t.Errorf("Expected collection name %s, got %s", tt.expected, got)
			}
		})
	}
}

func TestCache(t *testing.T) {
	cache := NewCache(100 * time.Millisecond)

	info := &Info{
		Root: "/home/user/project",
		ID:   "test123",
	}

	// Test Set and Get
	cache.Set("key1", info)
	retrieved := cache.Get("key1")
	if retrieved == nil {
		t.Fatal("Expected to retrieve cached value")
	}

	if retrieved.ID != info.ID {
		t.Errorf("Expected ID %s, got %s", info.ID, retrieved.ID)
	}

	// Test expiration
	time.Sleep(150 * time.Millisecond)
	expired := cache.Get("key1")
	if expired != nil {
		t.Error("Expected cached value to expire")
	}

	// Test Size
	cache.Set("key1", info)
	cache.Set("key2", info)
	if cache.Size() != 2 {
		t.Errorf("Expected cache size 2, got %d", cache.Size())
	}

	// Test Clear
	cache.Clear()
	if cache.Size() != 0 {
		t.Errorf("Expected cache size 0 after clear, got %d", cache.Size())
	}
}

func TestCache_CleanExpired(t *testing.T) {
	cache := NewCache(50 * time.Millisecond)

	info := &Info{Root: "/test", ID: "test"}

	// Add multiple entries
	cache.Set("key1", info)
	cache.Set("key2", info)
	cache.Set("key3", info)

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Add fresh entry
	cache.Set("key4", info)

	// Clean expired
	removed := cache.CleanExpired()
	if removed != 3 {
		t.Errorf("Expected to remove 3 expired entries, removed %d", removed)
	}

	// Check size
	if cache.Size() != 1 {
		t.Errorf("Expected cache size 1 after cleanup, got %d", cache.Size())
	}
}

func TestInferProjectType(t *testing.T) {
	tests := []struct {
		marker string
		want   string
	}{
		{"go.mod", "go"},
		{"artisan", "laravel"},
		{"composer.json", "php"},
		{"package.json", "nodejs"},
		{"Cargo.toml", "rust"},
		{"pyproject.toml", "python"},
		{"setup.py", "python"},
		{"pom.xml", "maven"},
		{"build.gradle", "gradle"},
		{".git", "git"},
		{"unknown.file", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.marker, func(t *testing.T) {
			got := inferProjectType(tt.marker)
			if got != tt.want {
				t.Errorf("inferProjectType(%s) = %s, want %s", tt.marker, got, tt.want)
			}
		})
	}
}

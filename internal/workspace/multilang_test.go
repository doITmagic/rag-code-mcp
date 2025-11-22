package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLanguageDetection tests detection of multiple languages in a workspace
func TestMultiLanguageDetection(t *testing.T) {
	// Create a temporary directory for multi-language workspace
	tmpDir := t.TempDir()

	// Create markers for multiple languages
	markers := map[string]string{
		"go.mod":         "module example.com/myapp\n\ngo 1.21\n",
		"package.json":   `{"name": "myapp", "version": "1.0.0"}`,
		"pyproject.toml": "[tool.poetry]\nname = \"myapp\"\nversion = \"0.1.0\"\n",
		"composer.json":  `{"name": "vendor/myapp", "description": "My PHP app"}`,
	}

	for filename, content := range markers {
		if err := os.WriteFile(filepath.Join(tmpDir, filename), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create %s: %v", filename, err)
		}
	}

	// Create detector with all markers
	detector := NewDetectorWithConfig([]string{
		".git",
		"go.mod",
		"package.json",
		"pyproject.toml",
		"setup.py",
		"requirements.txt",
		"composer.json",
	}, []string{"node_modules", ".git", "vendor"})

	// Detect workspace
	info, err := detector.DetectFromPath(tmpDir)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}

	// Verify multiple languages detected
	expectedLanguages := map[string]bool{
		"go":         true,
		"javascript": true,
		"python":     true,
		"php":        true,
	}

	if len(info.Languages) == 0 {
		t.Fatalf("No languages detected, expected %d", len(expectedLanguages))
	}

	detectedLangs := make(map[string]bool)
	for _, lang := range info.Languages {
		detectedLangs[lang] = true
	}

	for expectedLang := range expectedLanguages {
		if !detectedLangs[expectedLang] {
			t.Errorf("Expected language %s not detected. Got: %v", expectedLang, info.Languages)
		}
	}

	t.Logf("Detected %d languages: %v", len(info.Languages), info.Languages)
}

// TestCollectionNameForLanguage tests language-specific collection naming
func TestCollectionNameForLanguage(t *testing.T) {
	info := &Info{
		Root:             "/home/user/project",
		ID:               "abc123def456",
		CollectionPrefix: "coderag",
		Languages:        []string{"go", "python"},
	}

	tests := []struct {
		name     string
		language string
		expected string
	}{
		{
			name:     "Go collection",
			language: "go",
			expected: "coderag-abc123def456-go",
		},
		{
			name:     "Python collection",
			language: "python",
			expected: "coderag-abc123def456-python",
		},
		{
			name:     "Empty language fallback",
			language: "",
			expected: "coderag-abc123def456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := info.CollectionNameForLanguage(tt.language)
			if result != tt.expected {
				t.Errorf("CollectionNameForLanguage(%q) = %q, want %q",
					tt.language, result, tt.expected)
			}
		})
	}
}

// TestInferLanguageFromMarker tests language inference from markers
func TestInferLanguageFromMarker(t *testing.T) {
	tests := []struct {
		marker   string
		expected string
	}{
		{"go.mod", "go"},
		{"package.json", "javascript"},
		{"pyproject.toml", "python"},
		{"setup.py", "python"},
		{"requirements.txt", "python"},
		{"composer.json", "php"},
		{"Cargo.toml", "rust"},
		{"pom.xml", "java"},
		{"build.gradle", "java"},
		{"Gemfile", "ruby"},
		{"Package.swift", "swift"},
		{".git", ""},        // Should return empty
		{"unknown.txt", ""}, // Should return empty
	}

	for _, tt := range tests {
		t.Run(tt.marker, func(t *testing.T) {
			result := inferLanguageFromMarker(tt.marker)
			if result != tt.expected {
				t.Errorf("inferLanguageFromMarker(%q) = %q, want %q",
					tt.marker, result, tt.expected)
			}
		})
	}
}

// TestSingleLanguageWorkspace tests workspace with only one language
func TestSingleLanguageWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	// Create only Go marker
	goMod := filepath.Join(tmpDir, "go.mod")
	if err := os.WriteFile(goMod, []byte("module example.com/app\n"), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	detector := NewDetectorWithConfig([]string{"go.mod", "package.json"}, []string{})

	info, err := detector.DetectFromPath(tmpDir)
	if err != nil {
		t.Fatalf("Failed to detect workspace: %v", err)
	}

	if len(info.Languages) != 1 {
		t.Errorf("Expected 1 language, got %d: %v", len(info.Languages), info.Languages)
	}

	if info.Languages[0] != "go" {
		t.Errorf("Expected language 'go', got %q", info.Languages[0])
	}

	if info.ProjectType != "go" {
		t.Errorf("Expected project type 'go', got %q", info.ProjectType)
	}
}

// TestEmptyWorkspace tests workspace with no language markers
func TestEmptyWorkspace(t *testing.T) {
	tmpDir := t.TempDir()

	detector := NewDetectorWithConfig([]string{"go.mod", "package.json"}, []string{})

	_, err := detector.DetectFromPath(tmpDir)
	if err == nil {
		t.Fatalf("expected error for empty workspace without markers, got nil")
	}
	if !strings.Contains(err.Error(), "could not detect workspace") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

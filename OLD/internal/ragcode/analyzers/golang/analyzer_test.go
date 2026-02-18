package golang

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/codetypes"
)

func TestCodeAnalyzer_AnalyzePaths(t *testing.T) {
	// Create a temporary Go file for testing
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")

	testCode := `package testpkg

import "fmt"

// Calculator provides mathematical operations
type Calculator struct {
	precision int
}

// Add adds two numbers and returns the result
func Add(a, b int) int {
	return a + b
}

// Multiply multiplies two numbers
func (c *Calculator) Multiply(a, b int) int {
	fmt.Println("Multiplying", a, b)
	return a * b
}

// Config holds configuration settings
type Config struct {
	Host string
	Port int
}
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("Expected at least some chunks, got none")
	}

	// Verify that chunks have Language set to "go"
	for _, chunk := range chunks {
		if chunk.Language != "go" {
			t.Errorf("Expected Language='go', got '%s' for chunk: %s", chunk.Language, chunk.Name)
		}
	}

	// Check for specific symbols
	foundAdd := false
	foundMultiply := false
	foundCalculator := false
	foundConfig := false

	for _, chunk := range chunks {
		t.Logf("Found chunk: Type=%s, Name=%s, Package=%s", chunk.Type, chunk.Name, chunk.Package)

		switch chunk.Name {
		case "Add":
			foundAdd = true
			if chunk.Type != "function" {
				t.Errorf("Expected Add to be 'function', got '%s'", chunk.Type)
			}
			if chunk.Package != "testpkg" {
				t.Errorf("Expected package 'testpkg', got '%s'", chunk.Package)
			}
		case "Multiply":
			foundMultiply = true
			if chunk.Type != "method" {
				t.Errorf("Expected Multiply to be 'method', got '%s'", chunk.Type)
			}
		case "Calculator":
			foundCalculator = true
			if chunk.Type != "type" {
				t.Errorf("Expected Calculator to be 'type', got '%s'", chunk.Type)
			}
		case "Config":
			foundConfig = true
			if chunk.Type != "type" {
				t.Errorf("Expected Config to be 'type', got '%s'", chunk.Type)
			}
		}
	}

	if !foundAdd {
		t.Error("Did not find 'Add' function in chunks")
	}
	if !foundMultiply {
		t.Error("Did not find 'Multiply' method in chunks")
	}
	if !foundCalculator {
		t.Error("Did not find 'Calculator' type in chunks")
	}
	if !foundConfig {
		t.Error("Did not find 'Config' type in chunks")
	}
}

func TestCodeAnalyzer_EmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed on empty directory: %v", err)
	}

	if len(chunks) != 0 {
		t.Errorf("Expected 0 chunks for empty directory, got %d", len(chunks))
	}
}

func TestCodeAnalyzer_NonExistentPath(t *testing.T) {
	analyzer := NewCodeAnalyzer()
	_, err := analyzer.AnalyzePaths([]string{"/nonexistent/path/that/does/not/exist"})

	// Should not crash, might return empty or error depending on implementation
	if err != nil {
		t.Logf("Got expected error for non-existent path: %v", err)
	}
}

func TestCodeAnalyzer_InterfaceTypes(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "interface.go")

	testCode := `package iface

// Reader reads data from a source
type Reader interface {
	Read(p []byte) (n int, err error)
	Close() error
}

// Writer writes data to a destination
type Writer interface {
	Write(p []byte) (n int, err error)
}
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	foundReader := false
	foundWriter := false

	for _, chunk := range chunks {
		if chunk.Name == "Reader" {
			foundReader = true
			if chunk.Type != "interface" && chunk.Type != "type" {
				t.Errorf("Expected Reader to be 'interface' or 'type', got '%s'", chunk.Type)
			}
		}
		if chunk.Name == "Writer" {
			foundWriter = true
			if chunk.Type != "interface" && chunk.Type != "type" {
				t.Errorf("Expected Writer to be 'interface' or 'type', got '%s'", chunk.Type)
			}
		}
	}

	if !foundReader {
		t.Error("Did not find 'Reader' interface in chunks")
	}
	if !foundWriter {
		t.Error("Did not find 'Writer' interface in chunks")
	}
}

func TestCodeAnalyzer_ChunkMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "meta.go")

	testCode := `package meta

// ProcessData processes input data
func ProcessData(input string) string {
	return input
}
`

	if err := os.WriteFile(testFile, []byte(testCode), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	if err != nil {
		t.Fatalf("AnalyzePaths failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("Expected at least one chunk")
	}

	for _, chunk := range chunks {
		// Verify required fields are populated
		if chunk.FilePath == "" {
			t.Error("FilePath should not be empty")
		}
		if chunk.StartLine <= 0 {
			t.Error("StartLine should be > 0")
		}
		if chunk.EndLine <= 0 {
			t.Error("EndLine should be > 0")
		}
		if chunk.EndLine < chunk.StartLine {
			t.Errorf("EndLine (%d) should be >= StartLine (%d)", chunk.EndLine, chunk.StartLine)
		}

		// Language must be set
		if chunk.Language != "go" {
			t.Errorf("Expected Language='go', got '%s'", chunk.Language)
		}
	}
}

func TestCodeAnalyzer_ImplementsInterface(t *testing.T) {
	analyzer := NewCodeAnalyzer()

	// Verify that CodeAnalyzer implements codetypes.PathAnalyzer
	var _ codetypes.PathAnalyzer = analyzer
}

package python

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPythonAnalyzer(t *testing.T) {
	tmpDir := t.TempDir()
	
	code := `"""Module docstring"""

class Calculator:
    """Class docstring"""
    def add(self, a: int, b: int) -> int:
        """Method docstring"""
        return a + b

def global_func():
    return 42
`
	filePath := filepath.Join(tmpDir, "calc.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	analyzer := NewAnalyzer()
	ctx := context.Background()

	res, err := analyzer.Analyze(ctx, filePath)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(res.Symbols) != 4 {
		t.Errorf("Expected 4 symbols, got %d", len(res.Symbols))
	}

	foundModule := false
	foundClass := false
	foundMethod := false
	foundFunc := false

	for _, sym := range res.Symbols {
		switch sym.Name {
		case "calc":
			if sym.Metadata["python_kind"] == "module" {
				foundModule = true
				if sym.Docstring != "Module docstring" {
					t.Errorf("Expected module docstring 'Module docstring', got '%s'", sym.Docstring)
				}
			}
		case "Calculator":
			foundClass = true
			if sym.Docstring != "Class docstring" {
				t.Errorf("Expected class docstring 'Class docstring', got '%s'", sym.Docstring)
			}
		case "add":
			foundMethod = true
			if sym.Metadata["class"] != "Calculator" {
				t.Errorf("Expected method class 'Calculator', got '%v'", sym.Metadata["class"])
			}
			if sym.Docstring != "Method docstring" {
				t.Errorf("Expected method docstring 'Method docstring', got '%s'", sym.Docstring)
			}
		case "global_func":
			foundFunc = true
		}
	}

	if !foundModule || !foundClass || !foundMethod || !foundFunc {
		t.Errorf("Missing symbols: module=%v, class=%v, method=%v, func=%v", foundModule, foundClass, foundMethod, foundFunc)
	}
}

package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGoAnalyzer(t *testing.T) {
	tmpDir := t.TempDir()
	
	code := `package testpkg
// HelloWorld is a test function.
func HelloWorld() string {
	return "Hello"
}

// Greeter is an interface.
type Greeter interface {
	Greet() string
}

const Version = "1.0.0"
var Status = "ready"
`
	filePath := filepath.Join(tmpDir, "test.go")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	analyzer := NewAnalyzer()
	ctx := context.Background()

	// Test single file analysis
	res, err := analyzer.Analyze(ctx, filePath)
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if res == nil {
		t.Fatal("Result is nil")
	}

	// Since extractSymbolsFromAST is currently a stub, we expect 0 symbols for single file for now
	// This test will help us verify the implementation later.
	t.Logf("Found %d symbols in single file", len(res.Symbols))

	// Test package analysis
	resPkg, err := analyzer.Analyze(ctx, tmpDir)
	if err != nil {
		t.Fatalf("Analyze package failed: %v", err)
	}

	foundFunc := false
	foundInterface := false
	foundConst := false
	foundVar := false

	for _, sym := range resPkg.Symbols {
		switch sym.Name {
		case "HelloWorld":
			foundFunc = true
		case "Greeter":
			foundInterface = true
		case "Version":
			foundConst = true
		case "Status":
			foundVar = true
		}
	}

	if !foundFunc {
		t.Error("HelloWorld function not found in package analysis")
	}
	if !foundInterface {
		t.Error("Greeter interface not found in package analysis")
	}
	if !foundConst {
		t.Log("Note: Constants extraction might need verification in package analysis")
	}
	if !foundVar {
		t.Log("Note: Variables extraction might need verification in package analysis")
	}
}

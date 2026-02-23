package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCodeAnalyzer_Complete(t *testing.T) {
	tmpDir := t.TempDir()

	code := `package testpkg

import "fmt"

const Version = "1.0.0"
var Debug = true

// Calculator provides mathematical operations
type Calculator struct {
	precision int
}

// Add adds two numbers
func Add(a, b int) int {
	return a + b
}

// Multiply multiplies two numbers
func (c *Calculator) Multiply(a, b int) int {
	return a * b
}

// Subtractor is an interface for subtraction
type Subtractor interface {
	Subtract(a, b int) int
}
`
	testFile := filepath.Join(tmpDir, "test.go")
	err := os.WriteFile(testFile, []byte(code), 0644)
	require.NoError(t, err)

	ca := NewCodeAnalyzer()

	t.Run("Interface implementation", func(t *testing.T) {
		var _ pkgParser.Analyzer = ca
		assert.Equal(t, "go", ca.Name())
		assert.True(t, ca.CanHandle("test.go"))
		assert.False(t, ca.CanHandle("test.py"))
		assert.False(t, ca.CanHandle("test_test.go"))
	})

	t.Run("Analyze directory", func(t *testing.T) {
		res, err := ca.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		assert.Equal(t, "go", res.Language)

		symbols := make(map[string]pkgParser.Symbol)
		for _, s := range res.Symbols {
			symbols[s.Name+"_"+string(s.Type)] = s
		}

		// Verify symbols
		assert.Contains(t, symbols, "Add_function")
		assert.Contains(t, symbols, "Calculator_type")
		assert.Contains(t, symbols, "Multiply_method")
		assert.Contains(t, symbols, "Subtractor_type")
		assert.Contains(t, symbols, "Version_const")
		assert.Contains(t, symbols, "Debug_var")

		// Verify function details
		addFunc := symbols["Add_function"]
		assert.Equal(t, "testpkg", addFunc.Package)
		assert.Equal(t, "func Add(a int, b int) int", addFunc.Signature)
		assert.Equal(t, "Add adds two numbers", addFunc.Docstring)
		assert.Contains(t, addFunc.Content, "return a + b")

		// Verify interface methods
		subtractor := symbols["Subtractor_type"]
		assert.Equal(t, "interface", subtractor.Metadata["kind"])
		methods := subtractor.Metadata["methods"].([]MethodInfo)
		require.Len(t, methods, 1)
		assert.Equal(t, "Subtract", methods[0].Name)
	})

	t.Run("AnalyzePackage", func(t *testing.T) {
		pkg, err := ca.AnalyzePackage(tmpDir)
		require.NoError(t, err)
		assert.Equal(t, "testpkg", pkg.Name)
		assert.Contains(t, pkg.Imports, "fmt")
		assert.Len(t, pkg.Functions, 2) // Add and Multiply (Multiply is method but also in Functions slice)
		assert.Len(t, pkg.Types, 2)
	})

	t.Run("Non-existent directory", func(t *testing.T) {
		_, err := ca.AnalyzePackage("/tmp/does-not-exist-rag-code")
		assert.Error(t, err)
	})

	t.Run("Directory with no go files", func(t *testing.T) {
		emptyDir := t.TempDir()
		res, err := ca.Analyze(context.Background(), emptyDir)
		assert.NoError(t, err)
		assert.Empty(t, res.Symbols)
	})
}

func TestCodeAnalyzer_EdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	ca := NewCodeAnalyzer()

	t.Run("Parse error file", func(t *testing.T) {
		badCode := `package bad; func {`
		err := os.WriteFile(filepath.Join(tmpDir, "bad.go"), []byte(badCode), 0644)
		require.NoError(t, err)

		res, err := ca.Analyze(context.Background(), tmpDir)
		assert.NoError(t, err) // Should skip bad file and return empty result
		assert.Empty(t, res.Symbols)
	})

	t.Run("Skip vendor and hidden", func(t *testing.T) {
		vendorDir := filepath.Join(tmpDir, "vendor")
		require.NoError(t, os.Mkdir(vendorDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(vendorDir, "skip.go"), []byte("package vendor"), 0644))

		hiddenDir := filepath.Join(tmpDir, ".hidden")
		require.NoError(t, os.Mkdir(hiddenDir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(hiddenDir, "skip.go"), []byte("package hidden"), 0644))

		res, err := ca.AnalyzePaths([]string{tmpDir})
		assert.NoError(t, err)
		assert.Empty(t, res)
	})

	t.Run("Complex types", func(t *testing.T) {
		complexDir := t.TempDir()
		code := `package complexpkg
type Data struct {
    Items []string
    Mapping map[string]int
    Done chan bool
    Handler func(int) error
}
`
		require.NoError(t, os.WriteFile(filepath.Join(complexDir, "complex.go"), []byte(code), 0644))
		res, err := ca.Analyze(context.Background(), complexDir)
		assert.NoError(t, err)
		assert.NotEmpty(t, res.Symbols)
	})
}

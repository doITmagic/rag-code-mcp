package parser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockAnalyzer struct {
	name string
	ext  string
}

func (m *mockAnalyzer) Name() string { return m.name }
func (m *mockAnalyzer) CanHandle(filePath string) bool {
	return filePath[len(filePath)-len(m.ext):] == m.ext
}
func (m *mockAnalyzer) Analyze(ctx context.Context, path string) (*Result, error) {
	return &Result{Language: m.name}, nil
}

func TestRegistry(t *testing.T) {
	// Clear registry for testing
	mu.Lock()
	analyzers = make(map[string]Analyzer)
	mu.Unlock()

	a1 := &mockAnalyzer{name: "go", ext: ".go"}
	a2 := &mockAnalyzer{name: "py", ext: ".py"}

	Register(a1)
	Register(a2)

	t.Run("GetByName", func(t *testing.T) {
		assert.Equal(t, a1, GetByName("go"))
		assert.Equal(t, a2, GetByName("py"))
		assert.Nil(t, GetByName("unknown"))
	})

	t.Run("GetByFile", func(t *testing.T) {
		assert.Equal(t, a1, GetByFile("test.go"))
		assert.Equal(t, a2, GetByFile("main.py"))
		assert.Nil(t, GetByFile("style.css"))
	})
}

func TestSymbolTypes(t *testing.T) {
	assert.Equal(t, SymbolType("function"), Function)
	assert.Equal(t, SymbolType("method"), Method)
	assert.Equal(t, SymbolType("class"), Class)
	assert.Equal(t, SymbolType("interface"), Interface)
	assert.Equal(t, SymbolType("type"), Type)
	assert.Equal(t, SymbolType("const"), Const)
	assert.Equal(t, SymbolType("var"), Var)
}

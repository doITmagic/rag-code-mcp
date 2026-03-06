package docs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_CanHandle(t *testing.T) {
	analyzer := NewAnalyzer()

	validExts := []string{"test.md", "README.markdown", "config.yaml", "data.json", "conf.toml", "index.xml", "doc.rst", "style.css", "main.scss", "query.sql", "script.sh"}
	invalidExts := []string{"main.go", "script.js", "style.less", "data.csv"}

	for _, ext := range validExts {
		assert.True(t, analyzer.CanHandle(ext), "Should handle %s", ext)
	}

	for _, ext := range invalidExts {
		assert.False(t, analyzer.CanHandle(ext), "Should NOT handle %s", ext)
	}
}

func TestAnalyzer_MarkdownParsing(t *testing.T) {
	analyzer := NewAnalyzer()
	
	// Create a temporary md file
	tmpDir := t.TempDir()
	mdFile := filepath.Join(tmpDir, "test.md")
	
	mdContent := `
# Main Title

This is an introductory paragraph.

## Section 1

* Point 1
* Point 2

` + "```" + `javascript
console.log("Hello, World!");
` + "```" + `
`
	err := os.WriteFile(mdFile, []byte(mdContent), 0644)
	require.NoError(t, err)

	result, err := analyzer.Analyze(context.Background(), mdFile)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "docs", result.Language)
	assert.GreaterOrEqual(t, len(result.Symbols), 3, "Should extract multiple symbols from markdown")

	// Validate symbols somewhat based on markdown content
	expectedSignatures := []string{
		"# Main Title",
		"## Section 1",
		"## Section 1", // For the code block that inherits the last heading
	}

	foundSignatures := make(map[string]bool)
	for _, sym := range result.Symbols {
		assert.Equal(t, "test.md", sym.Name)
		assert.NotEmpty(t, sym.Signature)
		assert.NotEmpty(t, sym.Content)
		foundSignatures[sym.Signature] = true
	}

	for _, expectedSig := range expectedSignatures {
		assert.True(t, foundSignatures[expectedSig], "Missing expected signature: %s", expectedSig)
	}
}

func TestAnalyzer_TreesitterParsing_YAML(t *testing.T) {
	analyzer := NewAnalyzer()
	
	tmpDir := t.TempDir()
	yamlFile := filepath.Join(tmpDir, "config.yaml")
	
	yamlContent := `
server:
  port: 8080
  host: localhost
database:
  user: root
  password: secure
`
	err := os.WriteFile(yamlFile, []byte(yamlContent), 0644)
	require.NoError(t, err)

	result, err := analyzer.Analyze(context.Background(), yamlFile)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, len(result.Symbols), 0, "Should extract symbols from yaml via treesitter")
	
	for _, sym := range result.Symbols {
		assert.Equal(t, "config.yaml", sym.Name)
		assert.Equal(t, "yaml", sym.Language)
		assert.Equal(t, "documentation", sym.Metadata["chunk_type"])
	}
}

func TestAnalyzer_TreesitterParsing_JSON(t *testing.T) {
	analyzer := NewAnalyzer()
	
	tmpDir := t.TempDir()
	jsonFile := filepath.Join(tmpDir, "data.json")
	
	jsonContent := `
{
  "project": "rag-code-mcp",
  "version": "1.0",
  "features": ["AST", "RAG"]
}
`
	err := os.WriteFile(jsonFile, []byte(jsonContent), 0644)
	require.NoError(t, err)

	result, err := analyzer.Analyze(context.Background(), jsonFile)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, len(result.Symbols), 0, "Should extract symbols from json via treesitter")
}

func TestAnalyzer_TreesitterParsing_CSS(t *testing.T) {
	analyzer := NewAnalyzer()
	
	tmpDir := t.TempDir()
	cssFile := filepath.Join(tmpDir, "style.css")
	
	cssContent := `
body {
  background-color: red;
}
.header {
  font-size: 24px;
}
`
	err := os.WriteFile(cssFile, []byte(cssContent), 0644)
	require.NoError(t, err)

	result, err := analyzer.Analyze(context.Background(), cssFile)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, len(result.Symbols), 0, "Should extract symbols from css via treesitter")
}

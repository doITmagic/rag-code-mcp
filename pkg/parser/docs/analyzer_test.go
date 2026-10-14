package docs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzer_CanHandle(t *testing.T) {
	analyzer := NewAnalyzer()

	validExts := []string{"test.md", "README.markdown", "config.yaml", "data.json", "conf.toml", "index.xml", "doc.rst"}
	invalidExts := []string{"main.go", "script.js", "style.css", "main.scss", "style.less", "query.sql", "script.sh", "app.svelte", "data.csv"}

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

func TestAnalyzer_DoesNotHandle_CSS(t *testing.T) {
	analyzer := NewAnalyzer()
	assert.False(t, analyzer.CanHandle("style.css"), "CSS should NOT be handled by docs parser")
	assert.False(t, analyzer.CanHandle("main.scss"), "SCSS should NOT be handled by docs parser")
	assert.False(t, analyzer.CanHandle("query.sql"), "SQL should NOT be handled by docs parser")
	assert.False(t, analyzer.CanHandle("script.sh"), "Shell should NOT be handled by docs parser")
	assert.False(t, analyzer.CanHandle("app.svelte"), "Svelte should NOT be handled by docs parser")
}

func TestAnalyzer_MarkdownTablesIndentedCodeAndHTML(t *testing.T) {
	analyzer := NewAnalyzer()
	tmpDir := t.TempDir()
	mdFile := filepath.Join(tmpDir, "advanced.md")

	mdContent := `# Data Section

| Name | Value |
| ---- | ----- |
| Foo  | 42    |
| Bar  | 99    |

## Indented Code

    func indented() {
        return
    }

## Raw HTML

<div class="custom-callout">Important note inside HTML block</div>
`
	require.NoError(t, os.WriteFile(mdFile, []byte(mdContent), 0644))

	res, err := analyzer.Analyze(context.Background(), mdFile)
	require.NoError(t, err)
	require.Len(t, res.Symbols, 3)

	assert.Contains(t, res.Symbols[0].Content, "Foo | 42")
	assert.Equal(t, "# Data Section", res.Symbols[0].Signature)

	assert.Equal(t, "code_block", string(res.Symbols[1].Type))
	assert.Contains(t, res.Symbols[1].Content, "func indented()")

	assert.Contains(t, res.Symbols[2].Content, "Important note inside HTML block")
}

func TestAnalyzer_JSONSmallAndLargeCoalescingAndUTF8Truncation(t *testing.T) {
	analyzer := NewAnalyzer()
	tmpDir := t.TempDir()

	// 1. Small JSON (< 10 bytes) should not be dropped
	smallJSON := filepath.Join(tmpDir, "small.json")
	require.NoError(t, os.WriteFile(smallJSON, []byte(`{"a":1}`), 0644))
	resSmall, err := analyzer.Analyze(context.Background(), smallJSON)
	require.NoError(t, err)
	require.Len(t, resSmall.Symbols, 1)
	assert.Equal(t, `{"a":1}`, resSmall.Symbols[0].Content)

	// 2. 400-key JSON object should coalesce small sibling pairs instead of 400 tiny chunks
	largeJSON := filepath.Join(tmpDir, "large.json")
	var sb strings.Builder
	sb.WriteString("{\n")
	for i := 0; i < 400; i++ {
		if i > 0 {
			sb.WriteString(",\n")
		}
		sb.WriteString(fmt.Sprintf(`  "k%d": %d`, i, i))
	}
	sb.WriteString("\n}")
	require.NoError(t, os.WriteFile(largeJSON, []byte(sb.String()), 0644))
	resLarge, err := analyzer.Analyze(context.Background(), largeJSON)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(resLarge.Symbols), 3)
	assert.LessOrEqual(t, len(resLarge.Symbols), 15)
	for _, sym := range resLarge.Symbols {
		assert.NotContains(t, sym.Signature, "_")
	}

	// 3. UTF-8 safe truncation at 8192 bytes on multibyte string leaf
	utf8JSON := filepath.Join(tmpDir, "utf8.json")
	// "ă" is 2 bytes in UTF-8; construct a string > 8192 bytes where byte 8192 lands mid-rune
	longVal := strings.Repeat("ă", 5000)
	require.NoError(t, os.WriteFile(utf8JSON, []byte(fmt.Sprintf(`{"text": "%s"}`, longVal)), 0644))
	resUTF8, err := analyzer.Analyze(context.Background(), utf8JSON)
	require.NoError(t, err)
	require.NotEmpty(t, resUTF8.Symbols)
	for _, sym := range resUTF8.Symbols {
		assert.True(t, utf8.ValidString(sym.Content), "chunk content must be valid UTF-8")
	}
}


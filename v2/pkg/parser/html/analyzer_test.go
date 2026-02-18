package html

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTMLAnalyzer_Comprehensive(t *testing.T) {
	tmpDir := t.TempDir()

	code := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>Complex Page</title>
</head>
<body>
    <h1 id="main-title" class="header">Main Welcome</h1>
    <p>Intro text.</p>
    <h2 id="section1">Features</h2>
    <ul>
        <li>Fast</li>
        <li>Reliable</li>
    </ul>
    <pre><code>func main() {}</code></pre>
    <h3>Nesting</h3>
    <p>Nested content.</p>
    <h2>Contact</h2>
    <p>Contact us at...</p>
</body>
</html>`

	filePath := filepath.Join(tmpDir, "complex.html")
	err := os.WriteFile(filePath, []byte(code), 0644)
	require.NoError(t, err)

	analyzer := NewAnalyzer()

	t.Run("Interface", func(t *testing.T) {
		assert.Equal(t, "html", analyzer.Name())
		assert.True(t, analyzer.CanHandle("index.html"))
		assert.True(t, analyzer.CanHandle("index.htm"))
		assert.False(t, analyzer.CanHandle("index.txt"))
	})

	t.Run("Analyze File", func(t *testing.T) {
		res, err := analyzer.Analyze(context.Background(), filePath)
		require.NoError(t, err)
		assert.Equal(t, "html", res.Language)

		require.Len(t, res.Symbols, 4) // H1, H2, H3, H2

		symbols := make(map[string]int)
		for _, s := range res.Symbols {
			symbols[s.Name]++
		}

		assert.Contains(t, symbols, "Main Welcome")
		assert.Contains(t, symbols, "Features")
		assert.Contains(t, symbols, "Nesting")
		assert.Contains(t, symbols, "Contact")

		// Verify metadata for H1
		var h1Found bool
		for _, s := range res.Symbols {
			if s.Name == "Main Welcome" {
				h1Found = true
				assert.Equal(t, 1, s.Metadata["heading_level"])
				assert.Equal(t, "main-title", s.Metadata["html_id"])
				assert.Equal(t, "header", s.Metadata["class"])
				assert.Equal(t, "Complex Page", s.Metadata["page_title"])
			}
			if s.Name == "Features" {
				assert.Contains(t, s.Metadata, "code_blocks")
				blocks := s.Metadata["code_blocks"].([]string)
				assert.Contains(t, blocks, "func main() {}")
			}
		}
		assert.True(t, h1Found)
	})

	t.Run("Fallback to body", func(t *testing.T) {
		simpleCode := `<html><body>Just some plain text without headers.</body></html>`
		simplePath := filepath.Join(tmpDir, "simple.html")
		os.WriteFile(simplePath, []byte(simpleCode), 0644)

		res, err := analyzer.Analyze(context.Background(), simplePath)
		require.NoError(t, err)
		require.Len(t, res.Symbols, 1)
		assert.Equal(t, "Just some plain text without headers.", res.Symbols[0].Content)
	})

	t.Run("Empty file", func(t *testing.T) {
		emptyPath := filepath.Join(tmpDir, "empty.html")
		os.WriteFile(emptyPath, []byte(""), 0644)

		res, err := analyzer.Analyze(context.Background(), emptyPath)
		require.NoError(t, err)
		assert.Empty(t, res.Symbols)
	})

	t.Run("Directory walk", func(t *testing.T) {
		subDir := filepath.Join(tmpDir, "subdir")
		os.Mkdir(subDir, 0755)
		os.WriteFile(filepath.Join(subDir, "file.html"), []byte("<h1>Sub</h1>"), 0644)

		res, err := analyzer.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		// 4 from complex + 1 from simple + 1 from subdir = 6
		assert.GreaterOrEqual(t, len(res.Symbols), 6)
	})

	t.Run("Skip dirs", func(t *testing.T) {
		skipDir := filepath.Join(tmpDir, "node_modules")
		os.Mkdir(skipDir, 0755)
		os.WriteFile(filepath.Join(skipDir, "skip.html"), []byte("<h1>Skip</h1>"), 0644)

		// Re-analyze should not find "Skip"
		res, err := analyzer.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		for _, s := range res.Symbols {
			assert.NotEqual(t, "Skip", s.Name)
		}
	})
}

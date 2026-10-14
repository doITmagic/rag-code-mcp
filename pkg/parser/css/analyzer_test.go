package css

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSSAnalyzer_SCSSAndEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	analyzer := NewAnalyzer()

	t.Run("LineCommentsTopLevelDeclsAndStartLine", func(t *testing.T) {
		scss := `@charset "UTF-8";
@import "variables";
$primary-color: #333;
// Single-line comment with a brace { that must be ignored
/* Block comment */

.header {
    background: url(http://example.com/bg.png);
    color: $primary-color;
}

// Stray closing brace below should not break subsequent rules
}

.footer {
    content: "string with } and // and ; inside";
    padding: 10px;
}
`
		filePath := filepath.Join(tmpDir, "styles.scss")
		require.NoError(t, os.WriteFile(filePath, []byte(scss), 0644))

		res, err := analyzer.Analyze(context.Background(), filePath)
		require.NoError(t, err)
		require.Len(t, res.Symbols, 2)

		assert.Equal(t, ".header", res.Symbols[0].Signature)
		assert.Equal(t, 7, res.Symbols[0].StartLine)
		assert.Equal(t, 10, res.Symbols[0].EndLine)
		assert.Contains(t, res.Symbols[0].Content, "http://example.com/bg.png")

		assert.Equal(t, ".footer", res.Symbols[1].Signature)
		assert.Equal(t, 15, res.Symbols[1].StartLine)
		assert.Equal(t, 18, res.Symbols[1].EndLine)
	})

	t.Run("VariablesOnlyFileFallback", func(t *testing.T) {
		varsOnly := `$font-size: 16px;
$margin: 12px;
`
		filePath := filepath.Join(tmpDir, "_vars.scss")
		require.NoError(t, os.WriteFile(filePath, []byte(varsOnly), 0644))

		res, err := analyzer.Analyze(context.Background(), filePath)
		require.NoError(t, err)
		require.Len(t, res.Symbols, 1)
		assert.Equal(t, "global", res.Symbols[0].Signature)
		assert.Contains(t, res.Symbols[0].Content, "$font-size: 16px;")
	})
}

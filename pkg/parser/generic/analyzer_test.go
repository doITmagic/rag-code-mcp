package generic

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenericAnalyzer(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a test file
	content := ""
	for i := 1; i <= 150; i++ {
		content += fmt.Sprintf("line %d\n", i)
	}

	filePath := filepath.Join(tmpDir, "test.sh")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	analyzer := NewAnalyzer("bash", []string{".sh"})
	analyzer.chunkSize = 50 // Use smaller chunk size for testing

	t.Run("Name", func(t *testing.T) {
		assert.Equal(t, "bash", analyzer.Name())
	})

	t.Run("CanHandle", func(t *testing.T) {
		assert.True(t, analyzer.CanHandle("script.sh"))
		assert.False(t, analyzer.CanHandle("script.py"))
	})

	t.Run("Analyze File", func(t *testing.T) {
		res, err := analyzer.Analyze(context.Background(), filePath)
		require.NoError(t, err)
		assert.Equal(t, "generic", res.Language)
		assert.Len(t, res.Symbols, 3) // 150 lines / 50 chunk size = 3 chunks

		assert.Equal(t, 1, res.Symbols[0].StartLine)
		assert.Equal(t, 50, res.Symbols[0].EndLine)
		assert.Equal(t, 51, res.Symbols[1].StartLine)
		assert.Equal(t, 100, res.Symbols[1].EndLine)
		assert.Equal(t, 101, res.Symbols[2].StartLine)
		assert.Equal(t, 150, res.Symbols[2].EndLine)

		assert.Contains(t, res.Symbols[0].Content, "line")
	})

	t.Run("Analyze Directory", func(t *testing.T) {
		// Add another file
		filePath2 := filepath.Join(tmpDir, "test2.sh")
		err := os.WriteFile(filePath2, []byte("short file"), 0644)
		require.NoError(t, err)

		res, err := analyzer.Analyze(context.Background(), tmpDir)
		require.NoError(t, err)
		// 3 from first file + 1 from second file
		assert.Len(t, res.Symbols, 4)
	})

	t.Run("Analyze Non-Existent", func(t *testing.T) {
		_, err := analyzer.Analyze(context.Background(), "/non/existent/path")
		assert.Error(t, err)
	})
}

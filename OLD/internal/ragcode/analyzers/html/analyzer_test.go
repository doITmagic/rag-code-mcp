package html

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodeAnalyzer_ExtractsSections(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "doc.html")
	html := `<!DOCTYPE html>
    <html>
        <head><title>Exemplu</title></head>
        <body>
            <h1 id="intro">Introducere</h1>
            <p>Paragraf introductiv.</p>
            <h2 class="detalii">Detalii</h2>
            <p>Informatii suplimentare.</p>
            <pre><code>console.log('test');</code></pre>
        </body>
    </html>`

	require.NoError(t, os.WriteFile(filePath, []byte(html), 0o644))

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	require.NoError(t, err)
	require.Len(t, chunks, 2)

	first := chunks[0]
	require.Equal(t, "html", first.Language)
	require.Equal(t, "section", first.Type)
	require.Equal(t, "Introducere", first.Name)
	require.Contains(t, first.Code, "Paragraf introductiv")
	require.Equal(t, "intro", first.Metadata["html_id"])

	second := chunks[1]
	require.Equal(t, "Detalii", second.Name)
	require.Equal(t, "section", second.Type)
	require.Contains(t, second.Code, "Informatii suplimentare")
	require.Contains(t, second.Code, "console.log('test');")
	require.Equal(t, "detalii", second.Metadata["class"])
}

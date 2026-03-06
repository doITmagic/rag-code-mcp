package php

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCodeAnalyzer_AdvancedPHPExtraction(t *testing.T) {
	tmpDir := t.TempDir()
	phpFile := filepath.Join(tmpDir, "advanced.php")

	phpCode := `<?php
namespace App\Core;

use App\Contracts\RepositoryInterface as Repo;
use App\Traits\Loggable;

abstract class BaseService extends \StdClass implements Repo {
    use Loggable;

    public const VERSION = '2.0';
    protected static int $instances = 0;
    private readonly string $secret;

    /**
     * @param string $name
     * @return bool
     */
    final public function process(string $name): bool {
        return true;
    }

    abstract protected function inner();
}
`

	err := os.WriteFile(phpFile, []byte(phpCode), 0644)
	require.NoError(t, err)

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzeFile(phpFile)
	require.NoError(t, err)

	// Verify advanced features
	var classChunk *CodeChunk
	for i := range chunks {
		if chunks[i].Type == "class" && chunks[i].Name == "BaseService" {
			classChunk = &chunks[i]
			break
		}
	}
	require.NotNil(t, classChunk)
	require.Equal(t, "\\StdClass", classChunk.Metadata["extends"])
	require.Contains(t, classChunk.Metadata["implements"], "Repo")
	require.Contains(t, classChunk.Metadata["uses"], "Loggable")

	// Check imports mapping
	imports := classChunk.Metadata["imports"].(map[string]string)
	require.Equal(t, "App\\Contracts\\RepositoryInterface", imports["Repo"])
	require.Equal(t, "App\\Traits\\Loggable", imports["Loggable"])

	// Check method modifiers
	foundProcess := false
	for _, ch := range chunks {
		if ch.Type == "method" && ch.Name == "process" {
			foundProcess = true
			break
		}
	}
	require.True(t, foundProcess)
}

func TestCodeAnalyzer_Directories(t *testing.T) {
	tmpDir := t.TempDir()

	// Test skipping dirs
	skipDir := filepath.Join(tmpDir, "vendor")
	require.NoError(t, os.Mkdir(skipDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(skipDir, "skip.php"), []byte("<?php class Skip {}"), 0644))

	validFile := filepath.Join(tmpDir, "valid.php")
	require.NoError(t, os.WriteFile(validFile, []byte("<?php class Valid {}"), 0644))

	analyzer := NewCodeAnalyzer()
	chunks, err := analyzer.AnalyzePaths([]string{tmpDir})
	require.NoError(t, err)

	foundValid := false
	foundSkip := false
	for _, ch := range chunks {
		if ch.Name == "Valid" {
			foundValid = true
		}
		if ch.Name == "Skip" {
			foundSkip = true
		}
	}
	require.True(t, foundValid)
	require.False(t, foundSkip)
}

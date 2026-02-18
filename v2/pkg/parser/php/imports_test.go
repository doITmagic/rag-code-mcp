package php

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAnalyzer_CollectsImports(t *testing.T) {
	code := `<?php
namespace App\Services;

use App\Models\User;
use App\Models\Post as BlogPost;
use Illuminate\Support\Facades\Log;

class UserService {
    public function getUser() {
        return User::find(1);
    }
}
`
	analyzer := NewCodeAnalyzer()
	// We need to mock reading a file, but AnalyzeFile reads from disk.
	// However, we can use the internal parsePHPSource and symbolCollector directly
	// OR we can write a temp file. Writing a temp file is safer and tests the whole flow.

	// Create temp file
	tmpDir := t.TempDir()
	importFile := filepath.Join(tmpDir, "UserService.php")
	err := os.WriteFile(importFile, []byte(code), 0644)
	assert.NoError(t, err)

	chunks, err := analyzer.AnalyzeFile(importFile)
	assert.NoError(t, err)
	assert.NotEmpty(t, chunks)

	// Check internal package info to see if imports were captured
	// The chunks themselves don't expose the Imports map directly in the struct
	// (it's in ClassInfo but CodeChunk flattens it or might not include it depending on implementation).
	// Wait, I added Imports to ClassInfo, but did I add it to CodeChunk?
	// I checked types.go, ClassInfo has Imports.
	// The analyzer stores packages internally.

	pkgs := analyzer.GetPackages()
	assert.NotEmpty(t, pkgs)

	var foundClass ClassInfo
	found := false
	for _, pkg := range pkgs {
		for _, cls := range pkg.Classes {
			if cls.Name == "UserService" {
				foundClass = cls
				found = true
				break
			}
		}
	}

	assert.True(t, found, "Class UserService should be found")

	// Verify imports
	assert.NotNil(t, foundClass.Imports)
	assert.Equal(t, "App\\Models\\User", foundClass.Imports["User"])
	assert.Equal(t, "App\\Models\\Post", foundClass.Imports["BlogPost"])
	assert.Equal(t, "Illuminate\\Support\\Facades\\Log", foundClass.Imports["Log"])
}

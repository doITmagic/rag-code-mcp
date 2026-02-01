package laravel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	"github.com/stretchr/testify/assert"
)

func TestEloquentAnalyzer_ResolveRelationsWithImports(t *testing.T) {
	// Manually construct a PackageInfo with a class that has imports and a relation method
	// We need to mock the AST for the method body so the AST helper can find the relation call.
	// Since mocking AST nodes manually is complex, we'll use the PHP analyzer to parse a snippet
	// and then feed that into the Eloquent analyzer.

	code := `<?php
namespace App\Models;

use App\Models\Other\Comment;
use App\Security\Role as UserRole;

class User extends Model {
    public function comments() {
        return $this->hasMany(Comment::class);
    }

    public function role() {
        return $this->belongsTo(UserRole::class);
    }
    
    public function posts() {
        // No import, should fallback to same namespace
        return $this->hasMany(Post::class);
    }
}
`
	// 1. Parse with PHP analyzer to get PackageInfo and AST
	phpAnalyzer := php.NewCodeAnalyzer()
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "User.php")
	err := os.WriteFile(tmpFile, []byte(code), 0644)
	assert.NoError(t, err)

	_, err = phpAnalyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	pkgs := phpAnalyzer.GetPackages()
	assert.NotEmpty(t, pkgs)

	// 2. Run Eloquent Analyzer
	// We need to find the package that contains App\Models
	var targetPkg *php.PackageInfo
	for _, pkg := range pkgs {
		if pkg.Namespace == "App\\Models" {
			targetPkg = pkg
			break
		}
	}
	assert.NotNil(t, targetPkg)

	eloquentAnalyzer := NewEloquentAnalyzer(targetPkg)
	models := eloquentAnalyzer.AnalyzeModels()

	assert.Len(t, models, 1)
	userModel := models[0]
	assert.Equal(t, "User", userModel.ClassName)

	// 3. Verify Relations
	relations := userModel.Relations
	assert.Len(t, relations, 3)

	relMap := make(map[string]EloquentRelation)
	for _, r := range relations {
		relMap[r.Name] = r
	}

	// Check 'comments' relation - should resolve to App\Models\Other\Comment via import
	assert.Contains(t, relMap, "comments")
	assert.Equal(t, "App\\Models\\Other\\Comment", relMap["comments"].RelatedModel)

	// Check 'role' relation - should resolve to App\Security\Role via alias import
	assert.Contains(t, relMap, "role")
	assert.Equal(t, "App\\Security\\Role", relMap["role"].RelatedModel)

	// Check 'posts' relation - should resolve to App\Models\Post via fallback
	assert.Contains(t, relMap, "posts")
	assert.Equal(t, "App\\Models\\Post", relMap["posts"].RelatedModel)
}

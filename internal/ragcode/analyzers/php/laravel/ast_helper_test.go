package laravel

import (
	"os"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/ragcode/analyzers/php"
	"github.com/stretchr/testify/assert"
)

func TestASTPropertyExtractor_ExtractStringArray(t *testing.T) {
	phpCode := `<?php
namespace App;

class Test {
    protected $fillable = ['name', 'email', 'password'];
    protected $guarded = ['id'];
}
`

	tmpFile := "/tmp/test_ast_extract.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	// Analyze PHP file
	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	packages := analyzer.GetPackages()
	assert.Len(t, packages, 1)

	pkg := packages[0]
	assert.Len(t, pkg.Classes, 1)
	assert.Len(t, pkg.ClassNodes, 1)

	classNode := pkg.ClassNodes[pkg.Classes[0].FullName]
	assert.NotNil(t, classNode)

	// Test AST helper
	helper := NewASTPropertyExtractor()

	fillable := helper.ExtractStringArrayFromClass(classNode, "fillable")
	t.Logf("Fillable result: %v", fillable)
	assert.ElementsMatch(t, []string{"name", "email", "password"}, fillable)

	guarded := helper.ExtractStringArrayFromClass(classNode, "guarded")
	t.Logf("Guarded result: %v", guarded)
	assert.ElementsMatch(t, []string{"id"}, guarded)
}

func TestASTPropertyExtractor_ExtractMap(t *testing.T) {
	phpCode := `<?php
namespace App;

class Test {
    protected $casts = [
        'is_admin' => 'boolean',
        'age' => 'integer',
    ];
}
`

	tmpFile := "/tmp/test_ast_map.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	packages := analyzer.GetPackages()
	pkg := packages[0]
	classNode := pkg.ClassNodes[pkg.Classes[0].FullName]

	helper := NewASTPropertyExtractor()
	casts := helper.ExtractMapFromClass(classNode, "casts")
	t.Logf("Casts result: %v", casts)

	assert.Equal(t, "boolean", casts["is_admin"])
	assert.Equal(t, "integer", casts["age"])
}

func TestASTPropertyExtractor_ExtractString(t *testing.T) {
	phpCode := `<?php
namespace App;

class Test {
    protected $table = 'users';
    protected $primaryKey = 'id';
}
`

	tmpFile := "/tmp/test_ast_string.php"
	err := os.WriteFile(tmpFile, []byte(phpCode), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	analyzer := php.NewCodeAnalyzer()
	_, err = analyzer.AnalyzeFile(tmpFile)
	assert.NoError(t, err)

	packages := analyzer.GetPackages()
	pkg := packages[0]
	classNode := pkg.ClassNodes[pkg.Classes[0].FullName]

	helper := NewASTPropertyExtractor()

	table := helper.ExtractStringPropertyFromClass(classNode, "table")
	t.Logf("Table result: %s", table)
	assert.Equal(t, "users", table)

	primaryKey := helper.ExtractStringPropertyFromClass(classNode, "primaryKey")
	t.Logf("PrimaryKey result: %s", primaryKey)
	assert.Equal(t, "id", primaryKey)
}

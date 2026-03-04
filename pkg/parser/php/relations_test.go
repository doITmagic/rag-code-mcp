package php

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const phpRelationsCode = `<?php
namespace App;

interface Loggable {}

trait Timestampable {
    public function touch() {}
}

class BaseModel {
    public function validate() { return true; }
}

class Article extends BaseModel implements Loggable {
    use Timestampable;

    public function save() {
        $this->validate();
    }
}
`

func findPHPSymbol(symbols []pkgParser.Symbol, name string) *pkgParser.Symbol {
	for i := range symbols {
		if symbols[i].Name == name {
			return &symbols[i]
		}
	}
	return nil
}

func hasPHPRelation(rels []pkgParser.Relation, target string, relType pkgParser.RelationType) bool {
	for _, r := range rels {
		if r.TargetName == target && r.Type == relType {
			return true
		}
	}
	return false
}

func TestPHPRelations_Inheritance(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "article.php")
	require.NoError(t, os.WriteFile(f, []byte(phpRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	article := findPHPSymbol(res.Symbols, "Article")
	require.NotNil(t, article, "Article class symbol must exist; got %v", res.Symbols)

	assert.True(t, hasPHPRelation(article.Relations, "BaseModel", pkgParser.RelInheritance),
		"Article should extend BaseModel; got %v", article.Relations)
}

func TestPHPRelations_Implements(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "article.php")
	require.NoError(t, os.WriteFile(f, []byte(phpRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	article := findPHPSymbol(res.Symbols, "Article")
	require.NotNil(t, article, "Article class symbol must exist")

	assert.True(t, hasPHPRelation(article.Relations, "Loggable", pkgParser.RelImplements),
		"Article should implement Loggable; got %v", article.Relations)
}

func TestPHPRelations_UsesTrait(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "article.php")
	require.NoError(t, os.WriteFile(f, []byte(phpRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	article := findPHPSymbol(res.Symbols, "Article")
	require.NotNil(t, article, "Article class symbol must exist")

	assert.True(t, hasPHPRelation(article.Relations, "Timestampable", pkgParser.RelUsesTrait),
		"Article should use trait Timestampable; got %v", article.Relations)
}

func TestPHPRelations_MethodCalls(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "article.php")
	require.NoError(t, os.WriteFile(f, []byte(phpRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	// Calls appear on the class-level chunk (aggregated from methods)
	article := findPHPSymbol(res.Symbols, "Article")
	require.NotNil(t, article, "Article class symbol must exist")

	assert.True(t, hasPHPRelation(article.Relations, "validate", pkgParser.RelCalls),
		"Article should have calls→validate relation; got %v", article.Relations)
}

func TestPHPRelations_TypesAreCanonical(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "article.php")
	require.NoError(t, os.WriteFile(f, []byte(phpRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	for _, sym := range res.Symbols {
		for _, rel := range sym.Relations {
			valid := rel.Type == pkgParser.RelInheritance ||
				rel.Type == pkgParser.RelImplements ||
				rel.Type == pkgParser.RelUsesTrait ||
				rel.Type == pkgParser.RelCalls ||
				rel.Type == pkgParser.RelUsesType ||
				rel.Type == pkgParser.RelDependency
			assert.True(t, valid,
				"symbol %q: unknown RelationType %q — must use pkgParser constants", sym.Name, rel.Type)
		}
	}
}

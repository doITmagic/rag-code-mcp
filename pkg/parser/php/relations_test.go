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

	// Calls belong to the method, not its containing class.
	article := findPHPSymbol(res.Symbols, "Article")
	require.NotNil(t, article, "Article class symbol must exist")

	assert.False(t, hasPHPRelation(article.Relations, "validate", pkgParser.RelCalls))
	save := findPHPSymbol(res.Symbols, "save")
	require.NotNil(t, save)
	assert.True(t, hasPHPRelation(save.Relations, "validate", pkgParser.RelCalls))
	assert.Equal(t, "Article", save.Metadata["class"])
	assert.Equal(t, "App\\Article::save", save.QualifiedName)
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

// Calls nested in assignments, operators, closures, new, try and switch
// must reach the call graph, not only bare statement calls.
func TestPHPRelations_CallsInNestedConstructs(t *testing.T) {
	code := `<?php
namespace App;

class Svc {
    public function run($x) {
        $a = self::inAssign();
        $a .= self::inCompound();
        if (!self::inNot() && self::inBinary() > 1) {}
        $b = $x ? self::inTernary() : null;
        $f = function () { return Str::inClosure(); };
        $g = fn() => self::inArrow();
        $o = new Entry(self::inNew());
        try { $this->inTry(); } catch (\Exception $e) { $this->inCatch(); } finally { $this->inFinally(); }
        switch ($x) { case 1: helperInSwitch(); break; }
        if ($x) {} elseif (self::inElseIf()) {}
        echo self::inEcho();
        do { $x--; } while (self::inDo());
        $s = "a" . self::inConcat();
        $m = match ($x) { 1 => self::inMatch(), default => 0 };
        $y = @file_get_contents($x);
        $n = $x?->inNullsafe();
        return $dotenv->load();
    }
}

function top() {
    $v = inFunctionAssign();
}
`
	f := filepath.Join(t.TempDir(), "svc.php")
	require.NoError(t, os.WriteFile(f, []byte(code), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	run := findPHPSymbol(res.Symbols, "run")
	require.NotNil(t, run)
	for _, target := range []string{"inAssign", "inCompound", "inNot", "inBinary", "inTernary",
		"inClosure", "inArrow", "inNew", "inTry", "inCatch", "inFinally", "helperInSwitch",
		"inElseIf", "inEcho", "inDo", "inConcat", "inMatch", "file_get_contents", "inNullsafe", "load"} {
		assert.True(t, hasPHPRelation(run.Relations, target, pkgParser.RelCalls), "run should call %s; got %v", target, run.Relations)
	}
	for _, rel := range run.Relations {
		switch rel.TargetName {
		case "inClosure":
			assert.Equal(t, "Str", rel.Receiver)
			assert.Equal(t, "App\\Str::inClosure", rel.TargetQualifiedName)
		case "load":
			assert.Equal(t, "dotenv", rel.Receiver)
		}
	}

	top := findPHPSymbol(res.Symbols, "top")
	require.NotNil(t, top)
	assert.True(t, hasPHPRelation(top.Relations, "inFunctionAssign", pkgParser.RelCalls), "got %v", top.Relations)
}

// Calls inside an anonymous class or a function declared in a method body
// belong to those symbols, not to the enclosing method.
func TestPHPRelations_NestedDeclarationsKeepTheirCalls(t *testing.T) {
	code := `<?php
class Svc {
    public function outer() {
        $h = new class(argCall()) { public function inner() { innerOnly(); } };
        function nestedFn() { nestedOnly(); }
        return outerOnly();
    }
}
`
	f := filepath.Join(t.TempDir(), "svc.php")
	require.NoError(t, os.WriteFile(f, []byte(code), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	outer := findPHPSymbol(res.Symbols, "outer")
	require.NotNil(t, outer)
	assert.True(t, hasPHPRelation(outer.Relations, "outerOnly", pkgParser.RelCalls), "got %v", outer.Relations)
	assert.True(t, hasPHPRelation(outer.Relations, "argCall", pkgParser.RelCalls), "got %v", outer.Relations)
	assert.False(t, hasPHPRelation(outer.Relations, "innerOnly", pkgParser.RelCalls), "got %v", outer.Relations)
	assert.False(t, hasPHPRelation(outer.Relations, "nestedOnly", pkgParser.RelCalls), "got %v", outer.Relations)
}

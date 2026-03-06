package python

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pyRelationsCode = `
class Base:
    pass

class Mixin:
    pass

class Child(Base, Mixin):
    """Inherits from Base and Mixin."""

    def process(self, value: str) -> None:
        helper()
        self.save()
`

func findSymbol(symbols []pkgParser.Symbol, name string) *pkgParser.Symbol {
	for i := range symbols {
		if symbols[i].Name == name {
			return &symbols[i]
		}
	}
	return nil
}

func hasRelation(rels []pkgParser.Relation, target string, relType pkgParser.RelationType) bool {
	for _, r := range rels {
		if r.TargetName == target && r.Type == relType {
			return true
		}
	}
	return false
}

func TestPythonRelations_Inheritance(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "rel.py")
	require.NoError(t, os.WriteFile(f, []byte(pyRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	child := findSymbol(res.Symbols, "Child")
	require.NotNil(t, child, "Child class symbol must exist")

	assert.True(t, hasRelation(child.Relations, "Base", pkgParser.RelInheritance),
		"Child should inherit from Base; got %v", child.Relations)
	assert.True(t, hasRelation(child.Relations, "Mixin", pkgParser.RelInheritance),
		"Child should inherit from Mixin; got %v", child.Relations)
}

func TestPythonRelations_MethodCalls(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "rel.py")
	require.NoError(t, os.WriteFile(f, []byte(pyRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	process := findSymbol(res.Symbols, "process")
	require.NotNil(t, process, "process method symbol must exist")

	assert.True(t, hasRelation(process.Relations, "helper", pkgParser.RelCalls),
		"process should call helper; got %v", process.Relations)
}

func TestPythonRelations_TypesAreCanonical(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "rel.py")
	require.NoError(t, os.WriteFile(f, []byte(pyRelationsCode), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	for _, sym := range res.Symbols {
		for _, rel := range sym.Relations {
			valid := rel.Type == pkgParser.RelInheritance ||
				rel.Type == pkgParser.RelDependency ||
				rel.Type == pkgParser.RelCalls ||
				rel.Type == pkgParser.RelUsesType ||
				rel.Type == pkgParser.RelImplements ||
				rel.Type == pkgParser.RelUsesTrait
			assert.True(t, valid,
				"symbol %q: unknown RelationType %q — must use pkgParser constants", sym.Name, rel.Type)
		}
	}
}

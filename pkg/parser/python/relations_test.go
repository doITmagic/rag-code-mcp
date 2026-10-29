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

// A call nested in another call's arguments, and a method called on any
// object (not only self, cls or a capitalised class), are calls too.
func TestPythonRelations_NestedAndObjectCalls(t *testing.T) {
	code := `
class TimestampSigner:
    def sign(self, value):
        timestamp = base64_encode(int_to_bytes(self.get_timestamp()))
        return signer.unsign(value)


def is_text_serializer(serializer):
    return isinstance(serializer.dumps({}), str)
`
	f := filepath.Join(t.TempDir(), "timed.py")
	require.NoError(t, os.WriteFile(f, []byte(code), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	sign := findSymbol(res.Symbols, "sign")
	require.NotNil(t, sign)
	for _, target := range []string{"base64_encode", "int_to_bytes", "get_timestamp", "unsign"} {
		assert.True(t, hasRelation(sign.Relations, target, pkgParser.RelCalls), "sign should call %s; got %v", target, sign.Relations)
	}
	isText := findSymbol(res.Symbols, "is_text_serializer")
	require.NotNil(t, isText)
	assert.True(t, hasRelation(isText.Relations, "dumps", pkgParser.RelCalls), "got %v", isText.Relations)
}

// Methods that share a name in one file can only be told apart by their
// class, so each carries Class.method as its qualified name.
func TestPythonMethodsCarryQualifiedName(t *testing.T) {
	code := `class SigningAlgorithm:
    def get_signature(self, key, value):
        return b""


class Signer:
    def get_signature(self, value):
        return b""


def helper():
    pass
`
	f := filepath.Join(t.TempDir(), "signer.py")
	require.NoError(t, os.WriteFile(f, []byte(code), 0644))

	res, err := NewAnalyzer().Analyze(context.Background(), f)
	require.NoError(t, err)

	var qualified []string
	for _, sym := range res.Symbols {
		if sym.Name == "get_signature" {
			qualified = append(qualified, sym.QualifiedName)
		}
		if sym.Name == "helper" || sym.Name == "Signer" {
			assert.Empty(t, sym.QualifiedName, sym.Name)
		}
	}
	assert.ElementsMatch(t, []string{"SigningAlgorithm.get_signature", "Signer.get_signature"}, qualified)
}

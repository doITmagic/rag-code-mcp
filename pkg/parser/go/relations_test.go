package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const goRelationsCode = `package mypkg

type Formatter struct{}

type Result struct{}

// ProcessData processes the data using the given Formatter (returns string, not a package type)
func ProcessData(f *Formatter, raw string) string {
	return raw
}

// Process uses both Formatter and Result
func (r *Result) Process(f *Formatter) string {
	return ""
}
`

func findGoSymbol(symbols []pkgParser.Symbol, name string) *pkgParser.Symbol {
	for i := range symbols {
		if symbols[i].Name == name {
			return &symbols[i]
		}
	}
	return nil
}

func hasGoRelation(rels []pkgParser.Relation, target string, relType pkgParser.RelationType) bool {
	for _, r := range rels {
		if r.TargetName == target && r.Type == relType {
			return true
		}
	}
	return false
}

func TestGoRelations_UsesType(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "rel.go")
	require.NoError(t, os.WriteFile(f, []byte(goRelationsCode), 0644))

	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)

	process := findGoSymbol(res.Symbols, "ProcessData")
	require.NotNil(t, process, "ProcessData function symbol must exist")

	assert.True(t, hasGoRelation(process.Relations, "Formatter", pkgParser.RelUsesType),
		"ProcessData should have uses_type→Formatter; got %v", process.Relations)
}

func TestGoRelations_MethodUsesType(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "rel.go")
	require.NoError(t, os.WriteFile(f, []byte(goRelationsCode), 0644))

	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)

	process := findGoSymbol(res.Symbols, "Process")
	require.NotNil(t, process, "Process method symbol must exist")

	assert.True(t, hasGoRelation(process.Relations, "Formatter", pkgParser.RelUsesType),
		"Process should have uses_type→Formatter; got %v", process.Relations)
}

func TestGoRelations_TypesAreCanonical(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "rel.go")
	require.NoError(t, os.WriteFile(f, []byte(goRelationsCode), 0644))

	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)

	for _, sym := range res.Symbols {
		for _, rel := range sym.Relations {
			valid := rel.Type == pkgParser.RelUsesType ||
				rel.Type == pkgParser.RelImplements ||
				rel.Type == pkgParser.RelCalls ||
				rel.Type == pkgParser.RelInheritance ||
				rel.Type == pkgParser.RelDependency ||
				rel.Type == pkgParser.RelUsesTrait
			assert.True(t, valid,
				"symbol %q: unknown RelationType %q — must use pkgParser constants", sym.Name, rel.Type)
		}
	}
}

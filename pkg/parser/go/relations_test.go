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

const goTemplateUsageCode = `package web

import "html/template"

func RenderPage(w io.Writer, data any) error {
	t, err := template.ParseFiles("templates/layout.html", "templates/header.html")
	if err != nil {
		return err
	}
	return t.Execute(w, data)
}

func RenderDashboard(w io.Writer, data any) error {
	t := template.Must(template.ParseGlob("templates/dashboard/*.tmpl"))
	return t.Execute(w, data)
}
`

func TestGoRelations_TemplateFileDependencies(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "handler.go")
	require.NoError(t, os.WriteFile(f, []byte(goTemplateUsageCode), 0644))

	ca := NewCodeAnalyzer()
	res, err := ca.Analyze(context.Background(), tmpDir)
	require.NoError(t, err)

	renderPage := findGoSymbol(res.Symbols, "RenderPage")
	require.NotNil(t, renderPage, "RenderPage function symbol must exist")

	// Should have dependency relations to template file paths
	assert.True(t, hasGoRelation(renderPage.Relations, "templates/layout.html", pkgParser.RelDependency),
		"RenderPage should have dependency→templates/layout.html; got %v", renderPage.Relations)
	assert.True(t, hasGoRelation(renderPage.Relations, "templates/header.html", pkgParser.RelDependency),
		"RenderPage should have dependency→templates/header.html; got %v", renderPage.Relations)

	// Should have template_files in metadata
	if tplFiles, ok := renderPage.Metadata["template_files"]; ok {
		files, ok := tplFiles.([]string)
		assert.True(t, ok, "template_files metadata should be []string")
		assert.Contains(t, files, "templates/layout.html")
		assert.Contains(t, files, "templates/header.html")
	} else {
		t.Error("expected template_files metadata on RenderPage")
	}

	// RenderDashboard: ParseGlob with glob pattern
	renderDash := findGoSymbol(res.Symbols, "RenderDashboard")
	require.NotNil(t, renderDash, "RenderDashboard function symbol must exist")

	assert.True(t, hasGoRelation(renderDash.Relations, "templates/dashboard/*.tmpl", pkgParser.RelDependency),
		"RenderDashboard should have dependency→templates/dashboard/*.tmpl; got %v", renderDash.Relations)
}

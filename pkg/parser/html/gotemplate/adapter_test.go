package gotemplate

import (
	"testing"

	pkgParser "github.com/doITmagic/rag-code-mcp/pkg/parser"
)

func TestConvertToSymbols_Layout(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("layout.html")})
	symbols := ConvertToSymbols(templates)

	if len(symbols) == 0 {
		t.Fatal("expected at least 1 symbol")
	}

	// Should have 2 symbols: one for {{ define "base" }}, one file-level
	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols (1 define + 1 file), got %d", len(symbols))
	}

	// First symbol: the define "base"
	defSym := symbols[0]
	if defSym.Name != "base" {
		t.Errorf("expected define symbol name 'base', got %q", defSym.Name)
	}
	if defSym.Metadata["template_type"] != "go_template_define" {
		t.Errorf("expected template_type=go_template_define, got %v", defSym.Metadata["template_type"])
	}
	// Should have relation to "nav" ({{ template "nav" }} is inside define "base")
	foundNavRel := false
	for _, rel := range defSym.Relations {
		if rel.TargetName == "nav" && rel.Type == pkgParser.RelDependency {
			foundNavRel = true
		}
	}
	if !foundNavRel {
		t.Errorf("expected relation to 'nav', got %v", defSym.Relations)
	}

	// Second symbol: file-level
	fileSym := symbols[1]
	if fileSym.Metadata["template_type"] != "go_template" {
		t.Errorf("expected template_type=go_template, got %v", fileSym.Metadata["template_type"])
	}
	// Signature should contain "defines: base"
	if fileSym.Signature == "" {
		t.Error("expected non-empty signature")
	}

	// File-level should have includes relation
	foundIncRel := false
	for _, rel := range fileSym.Relations {
		if rel.TargetName == "nav" && rel.Type == pkgParser.RelDependency {
			foundIncRel = true
		}
	}
	if !foundIncRel {
		t.Errorf("expected file-level relation to 'nav', got %v", fileSym.Relations)
	}
}

func TestConvertToSymbols_Partial(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("partial.gohtml")})
	symbols := ConvertToSymbols(templates)

	// No defines → only 1 file-level symbol
	if len(symbols) != 1 {
		t.Fatalf("expected 1 symbol (file-level only), got %d", len(symbols))
	}

	sym := symbols[0]
	if sym.Name != "partial" {
		t.Errorf("expected name 'partial', got %q", sym.Name)
	}
	if sym.Language != "html" {
		t.Errorf("expected language 'html', got %q", sym.Language)
	}

	// Check metadata
	vars, ok := sym.Metadata["variables"].([]string)
	if !ok || len(vars) == 0 {
		t.Error("expected variables in metadata")
	}
	rangeVars, ok := sym.Metadata["ranges"].([]string)
	if !ok || len(rangeVars) == 0 {
		t.Error("expected ranges in metadata")
	}
}

func TestConvertToSymbols_Metadata(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("page.tmpl")})
	symbols := ConvertToSymbols(templates)

	// page.tmpl has 1 define ("content") + 1 file-level
	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(symbols))
	}

	fileSym := symbols[1] // file-level is always last
	includes, ok := fileSym.Metadata["includes"].([]string)
	if !ok {
		t.Fatal("expected includes in metadata")
	}
	found := false
	for _, inc := range includes {
		if inc == "base" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected include 'base' in metadata, got %v", includes)
	}

	customFuncs, ok := fileSym.Metadata["custom_funcs"].([]string)
	if !ok || len(customFuncs) == 0 {
		t.Error("expected custom_funcs in metadata")
	}
}

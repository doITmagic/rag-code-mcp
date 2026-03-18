package gotemplate

import (
	"path/filepath"
	"testing"
)

func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

func TestAnalyze_Layout(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("layout.html")})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]

	// Defines
	if len(tpl.Defines) != 1 {
		t.Errorf("expected 1 define, got %d", len(tpl.Defines))
	} else if tpl.Defines[0].Name != "base" {
		t.Errorf("expected define name 'base', got %q", tpl.Defines[0].Name)
	}

	// Template includes
	if len(tpl.TemplateIncludes) != 1 {
		t.Errorf("expected 1 template include, got %d: %v", len(tpl.TemplateIncludes), tpl.TemplateIncludes)
	} else if tpl.TemplateIncludes[0].Name != "nav" {
		t.Errorf("expected template include 'nav', got %q", tpl.TemplateIncludes[0].Name)
	}

	// Blocks
	if len(tpl.Blocks) != 1 {
		t.Errorf("expected 1 block, got %d: %v", len(tpl.Blocks), tpl.Blocks)
	} else if tpl.Blocks[0].Name != "content" {
		t.Errorf("expected block 'content', got %q", tpl.Blocks[0].Name)
	}

	// Ranges
	if len(tpl.Ranges) != 1 {
		t.Errorf("expected 1 range, got %d: %v", len(tpl.Ranges), tpl.Ranges)
	} else if tpl.Ranges[0].Variable != ".Items" {
		t.Errorf("expected range variable '.Items', got %q", tpl.Ranges[0].Variable)
	}

	// Conditionals
	if len(tpl.Conditionals) != 1 {
		t.Errorf("expected 1 conditional, got %d: %v", len(tpl.Conditionals), tpl.Conditionals)
	} else {
		cond := tpl.Conditionals[0]
		if cond.Condition != ".IsAdmin" {
			t.Errorf("expected condition '.IsAdmin', got %q", cond.Condition)
		}
		if !cond.HasElse {
			t.Error("expected HasElse=true")
		}
	}

	// With
	if len(tpl.WithBlocks) != 1 {
		t.Errorf("expected 1 with block, got %d: %v", len(tpl.WithBlocks), tpl.WithBlocks)
	} else if tpl.WithBlocks[0].Pipeline != ".Footer" {
		t.Errorf("expected with pipeline '.Footer', got %q", tpl.WithBlocks[0].Pipeline)
	}

	// Comments
	if len(tpl.Comments) != 1 {
		t.Errorf("expected 1 comment, got %d: %v", len(tpl.Comments), tpl.Comments)
	}

	// Custom funcs
	if len(tpl.CustomFuncs) == 0 {
		t.Error("expected at least 1 custom func (formatDate)")
	} else {
		found := false
		for _, f := range tpl.CustomFuncs {
			if f == "formatDate" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected custom func 'formatDate', got %v", tpl.CustomFuncs)
		}
	}

	// TotalLines
	if tpl.TotalLines == 0 {
		t.Error("expected TotalLines > 0")
	}
}

func TestAnalyze_Page(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("page.tmpl")})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]

	// Template includes ({{ template "base" . }})
	if len(tpl.TemplateIncludes) != 1 {
		t.Errorf("expected 1 template include, got %d", len(tpl.TemplateIncludes))
	} else if tpl.TemplateIncludes[0].Name != "base" {
		t.Errorf("expected include 'base', got %q", tpl.TemplateIncludes[0].Name)
	}

	// Defines ({{ define "content" }})
	if len(tpl.Defines) != 1 {
		t.Errorf("expected 1 define, got %d", len(tpl.Defines))
	} else if tpl.Defines[0].Name != "content" {
		t.Errorf("expected define 'content', got %q", tpl.Defines[0].Name)
	}

	// Ranges
	if len(tpl.Ranges) != 1 {
		t.Errorf("expected 1 range, got %d", len(tpl.Ranges))
	} else if tpl.Ranges[0].Variable != ".Posts" {
		t.Errorf("expected range '.Posts', got %q", tpl.Ranges[0].Variable)
	}

	// Custom funcs
	found := false
	for _, f := range tpl.CustomFuncs {
		if f == "formatDate" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected custom func 'formatDate', got %v", tpl.CustomFuncs)
	}
}

func TestAnalyze_Partial(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("partial.gohtml")})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]

	// Conditionals
	if len(tpl.Conditionals) != 1 {
		t.Errorf("expected 1 conditional, got %d", len(tpl.Conditionals))
	}

	// Ranges
	if len(tpl.Ranges) != 1 {
		t.Errorf("expected 1 range, got %d", len(tpl.Ranges))
	} else if tpl.Ranges[0].Variable != ".Widgets" {
		t.Errorf("expected range '.Widgets', got %q", tpl.Ranges[0].Variable)
	}
}

func TestAnalyze_MultipleFiles(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{
		testdataPath("layout.html"),
		testdataPath("page.tmpl"),
		testdataPath("partial.gohtml"),
	})

	if len(templates) != 3 {
		t.Fatalf("expected 3 templates, got %d", len(templates))
	}
}

func TestAnalyze_NonexistentFile(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{"testdata/nonexistent.html"})

	if len(templates) != 0 {
		t.Errorf("expected 0 templates for nonexistent file, got %d", len(templates))
	}
}

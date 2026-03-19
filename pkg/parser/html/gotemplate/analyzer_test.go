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

func TestAnalyze_ElseIf(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("elseif.tmpl")})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	tpl := templates[0]

	// Should have 3 conditionals: if .IsAdmin, else if .IsModerator, else if .IsEditor
	if len(tpl.Conditionals) < 3 {
		t.Errorf("expected at least 3 conditionals (if + 2 else-if), got %d: %v",
			len(tpl.Conditionals), tpl.Conditionals)
	}

	// First conditional (.IsAdmin) should have HasElse=true
	if len(tpl.Conditionals) > 0 {
		if tpl.Conditionals[0].Condition != ".IsAdmin" {
			t.Errorf("expected first condition '.IsAdmin', got %q", tpl.Conditionals[0].Condition)
		}
		if !tpl.Conditionals[0].HasElse {
			t.Error("expected first conditional HasElse=true (has else-if branch)")
		}
	}

	// Second conditional (.IsModerator) from else-if
	if len(tpl.Conditionals) > 1 {
		if tpl.Conditionals[1].Condition != ".IsModerator" {
			t.Errorf("expected second condition '.IsModerator', got %q", tpl.Conditionals[1].Condition)
		}
	}

	// Third conditional (.IsEditor) from else-if
	if len(tpl.Conditionals) > 2 {
		if tpl.Conditionals[2].Condition != ".IsEditor" {
			t.Errorf("expected third condition '.IsEditor', got %q", tpl.Conditionals[2].Condition)
		}
	}

	// Defines
	if len(tpl.Defines) != 1 {
		t.Errorf("expected 1 define, got %d", len(tpl.Defines))
	} else if tpl.Defines[0].Name != "dashboard" {
		t.Errorf("expected define 'dashboard', got %q", tpl.Defines[0].Name)
	}
}

func TestAnalyze_BlockRelations(t *testing.T) {
	ba := &GoTemplateAnalyzer{}
	templates := ba.Analyze([]string{testdataPath("layout.html")})

	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}

	// Convert and check for RelInheritance on blocks
	symbols := ConvertToSymbols(templates)
	if len(symbols) == 0 {
		t.Fatal("expected at least 1 symbol")
	}

	// File-level symbol should have RelInheritance for block "content"
	var hasInheritance bool
	for _, sym := range symbols {
		for _, rel := range sym.Relations {
			if rel.Type == "inheritance" && rel.TargetName == "content" {
				hasInheritance = true
			}
		}
	}
	if !hasInheritance {
		t.Error("expected RelInheritance for block 'content' in symbols")
	}
}

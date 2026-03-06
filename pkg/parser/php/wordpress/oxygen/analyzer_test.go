package oxygen

import (
	"testing"

	"github.com/VKCOM/php-parser/pkg/ast"
	"github.com/VKCOM/php-parser/pkg/conf"
	"github.com/VKCOM/php-parser/pkg/errors"
	"github.com/VKCOM/php-parser/pkg/parser"
	"github.com/VKCOM/php-parser/pkg/version"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

func parsePHP(t *testing.T, code string) ast.Vertex {
	t.Helper()
	root, err := parser.Parse([]byte(code), conf.Config{
		Version:          &version.Version{Major: 8, Minor: 0},
		ErrorHandlerFunc: func(e *errors.Error) {},
	})
	if err != nil {
		t.Fatalf("failed to parse PHP: %v", err)
	}
	return root
}

func TestAnalyzer_OxygenElement(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "global",
			Classes: []php.ClassInfo{
				{
					Name:      "HeroSection",
					Namespace: "global",
					FullName:  "HeroSection",
					Extends:   "OxyEl",
					FilePath:  "hero.php",
					StartLine: 1,
					EndLine:   50,
					Methods: []php.MethodInfo{
						{Name: "init"},
						{Name: "name"},
						{Name: "slug"},
						{Name: "icon"},
						{Name: "controls"},
						{Name: "render"},
					},
				},
				{
					Name:    "RegularClass",
					Extends: "BaseClass",
				},
			},
		},
	}

	analyzer := NewAnalyzer()
	info := analyzer.AnalyzeFromPackages(packages)

	if len(info.Elements) != 1 {
		t.Fatalf("expected 1 OxyEl element, got %d", len(info.Elements))
	}

	elem := info.Elements[0]
	if elem.ClassName != "HeroSection" {
		t.Errorf("expected 'HeroSection', got '%s'", elem.ClassName)
	}
	if !elem.SlugMethod {
		t.Error("expected SlugMethod=true")
	}
	if len(elem.Methods) != 6 {
		t.Errorf("expected 6 methods, got %d", len(elem.Methods))
	}
}

func TestAnalyzer_OxyElShadow(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "global",
			Classes: []php.ClassInfo{
				{
					Name:      "ShadowElement",
					Namespace: "global",
					FullName:  "ShadowElement",
					Extends:   "OxyElShadow",
					FilePath:  "shadow.php",
					StartLine: 1,
					EndLine:   30,
					Methods: []php.MethodInfo{
						{Name: "init"},
						{Name: "render"},
					},
				},
			},
		},
	}

	analyzer := NewAnalyzer()
	info := analyzer.AnalyzeFromPackages(packages)

	if len(info.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(info.Elements))
	}
	if info.Elements[0].ClassName != "ShadowElement" {
		t.Errorf("expected 'ShadowElement', got '%s'", info.Elements[0].ClassName)
	}
	if info.Elements[0].SlugMethod {
		t.Error("expected SlugMethod=false (no slug method)")
	}
	if len(info.Elements[0].Methods) != 2 {
		t.Errorf("expected 2 methods, got %d", len(info.Elements[0].Methods))
	}
}

func TestAnalyzer_OxygenTemplatePostType(t *testing.T) {
	code := `<?php
register_post_type('ct_template', array('public' => false));
register_post_type('oxy_user_library', array('public' => false));
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(info.Templates))
	}
	if info.Templates[0].PostType != "ct_template" {
		t.Errorf("expected 'ct_template', got '%s'", info.Templates[0].PostType)
	}
	if info.Templates[1].PostType != "oxy_user_library" {
		t.Errorf("expected 'oxy_user_library', got '%s'", info.Templates[1].PostType)
	}
}

func TestAnalyzer_OxygenTemplateInsideFunction(t *testing.T) {
	code := `<?php
function register_oxygen_types() {
    register_post_type('ct_template', array('public' => false));
}
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(info.Templates))
	}
}

func TestAnalyzer_IgnoresNonOxygenPostTypes(t *testing.T) {
	code := `<?php
register_post_type('book', array('public' => true));
register_post_type('product', array('public' => true));
`
	root := parsePHP(t, code)
	analyzer := NewAnalyzer()
	info := analyzer.Analyze(root, "test.php")

	if len(info.Templates) != 0 {
		t.Errorf("expected 0 templates for non-oxygen post types, got %d", len(info.Templates))
	}
}

func TestAnalyzer_NoOxygenElements(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "global",
			Classes: []php.ClassInfo{
				{Name: "Controller", Extends: "BaseController"},
				{Name: "Widget", Extends: "WP_Widget"},
			},
		},
	}

	analyzer := NewAnalyzer()
	info := analyzer.AnalyzeFromPackages(packages)

	if len(info.Elements) != 0 {
		t.Errorf("expected 0 elements, got %d", len(info.Elements))
	}
}

func TestAnalyzer_NamespacedOxyEl(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "MyPlugin\\Elements",
			Classes: []php.ClassInfo{
				{
					Name:      "CustomCard",
					Namespace: "MyPlugin\\Elements",
					FullName:  "MyPlugin\\Elements\\CustomCard",
					Extends:   "OxyEl\\OxyEl",
					FilePath:  "custom-card.php",
					StartLine: 5,
					EndLine:   40,
					Methods: []php.MethodInfo{
						{Name: "init"},
						{Name: "name"},
						{Name: "slug"},
						{Name: "controls"},
						{Name: "render"},
					},
				},
			},
		},
	}

	analyzer := NewAnalyzer()
	info := analyzer.AnalyzeFromPackages(packages)

	if len(info.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(info.Elements))
	}
	if info.Elements[0].Namespace != "MyPlugin\\Elements" {
		t.Errorf("expected namespace 'MyPlugin\\Elements', got '%s'", info.Elements[0].Namespace)
	}
	if !info.Elements[0].SlugMethod {
		t.Error("expected SlugMethod=true")
	}
}

func TestAnalyzer_TemplatesFromPackages(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "global",
			Functions: []php.FunctionInfo{
				{
					Name:     "register_types",
					FilePath: "init.php",
					Calls: []php.MethodCall{
						{Method: "register_post_type", Args: []string{"ct_template"}},
						{Method: "register_post_type", Args: []string{"book"}},
					},
				},
			},
		},
	}

	analyzer := NewAnalyzer()
	info := analyzer.AnalyzeFromPackages(packages)

	if len(info.Templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(info.Templates))
	}
	if info.Templates[0].PostType != "ct_template" {
		t.Errorf("expected 'ct_template', got '%s'", info.Templates[0].PostType)
	}
}

func TestAnalyzer_EmptyPackages(t *testing.T) {
	analyzer := NewAnalyzer()
	info := analyzer.AnalyzeFromPackages(nil)

	if len(info.Elements) != 0 || len(info.Templates) != 0 {
		t.Error("expected empty results for nil packages")
	}
}

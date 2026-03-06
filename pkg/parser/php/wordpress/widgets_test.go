package wordpress

import (
	"testing"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

func TestWidgetAnalyzer_ExtendsWPWidget(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "global",
			Classes: []php.ClassInfo{
				{
					Name:      "MyWidget",
					Namespace: "global",
					FullName:  "MyWidget",
					Extends:   "WP_Widget",
					FilePath:  "widget.php",
					StartLine: 1,
					EndLine:   20,
				},
				{
					Name:      "NotAWidget",
					Namespace: "global",
					FullName:  "NotAWidget",
					Extends:   "Controller",
					FilePath:  "controller.php",
					StartLine: 1,
					EndLine:   10,
				},
			},
		},
	}

	analyzer := NewWidgetAnalyzer()
	widgets := analyzer.AnalyzeWidgets(packages)

	if len(widgets) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(widgets))
	}
	if widgets[0].ClassName != "MyWidget" {
		t.Errorf("expected 'MyWidget', got '%s'", widgets[0].ClassName)
	}
	if widgets[0].FullName != "MyWidget" {
		t.Errorf("expected full name 'MyWidget', got '%s'", widgets[0].FullName)
	}
}

func TestWidgetAnalyzer_NamespacedWPWidget(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "App\\Widgets",
			Classes: []php.ClassInfo{
				{
					Name:      "SocialWidget",
					Namespace: "App\\Widgets",
					FullName:  "App\\Widgets\\SocialWidget",
					Extends:   "\\WP_Widget",
					FilePath:  "social-widget.php",
					StartLine: 5,
					EndLine:   50,
				},
			},
		},
	}

	analyzer := NewWidgetAnalyzer()
	widgets := analyzer.AnalyzeWidgets(packages)

	if len(widgets) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(widgets))
	}
	if widgets[0].Namespace != "App\\Widgets" {
		t.Errorf("expected namespace 'App\\Widgets', got '%s'", widgets[0].Namespace)
	}
}

func TestWidgetAnalyzer_NoWidgets(t *testing.T) {
	packages := []*php.PackageInfo{
		{
			Namespace: "global",
			Classes: []php.ClassInfo{
				{
					Name:    "MyClass",
					Extends: "BaseClass",
				},
			},
		},
	}

	analyzer := NewWidgetAnalyzer()
	widgets := analyzer.AnalyzeWidgets(packages)

	if len(widgets) != 0 {
		t.Errorf("expected 0 widgets, got %d", len(widgets))
	}
}

func TestWidgetAnalyzer_EmptyPackages(t *testing.T) {
	analyzer := NewWidgetAnalyzer()
	widgets := analyzer.AnalyzeWidgets(nil)

	if len(widgets) != 0 {
		t.Errorf("expected 0 widgets, got %d", len(widgets))
	}
}

package wordpress

import (
	"strings"

	"github.com/doITmagic/rag-code-mcp/pkg/parser/php"
)

// WidgetAnalyzer detects WordPress widget classes (extending WP_Widget)
type WidgetAnalyzer struct{}

// NewWidgetAnalyzer creates a new widget analyzer
func NewWidgetAnalyzer() *WidgetAnalyzer {
	return &WidgetAnalyzer{}
}

// AnalyzeWidgets detects classes that extend WP_Widget
func (a *WidgetAnalyzer) AnalyzeWidgets(packages []*php.PackageInfo) []Widget {
	var widgets []Widget

	for _, pkg := range packages {
		for _, class := range pkg.Classes {
			if a.isWidget(class) {
				widgets = append(widgets, Widget{
					ClassName: class.Name,
					Namespace: class.Namespace,
					FullName:  class.FullName,
					FilePath:  class.FilePath,
					StartLine: class.StartLine,
					EndLine:   class.EndLine,
				})
			}
		}
	}

	return widgets
}

// isWidget checks if a class extends WP_Widget
func (a *WidgetAnalyzer) isWidget(class php.ClassInfo) bool {
	if class.Extends == "" {
		return false
	}

	extends := class.Extends
	return extends == "WP_Widget" ||
		strings.HasSuffix(extends, "\\WP_Widget")
}

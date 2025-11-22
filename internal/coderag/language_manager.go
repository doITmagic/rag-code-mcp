package coderag

import (
	"strings"

	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/golang"
	htmlanalyzer "github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/html"
	"github.com/doITmagic/coderag-mcp/internal/coderag/analyzers/php/laravel"
	"github.com/doITmagic/coderag-mcp/internal/codetypes"
)

// Language identifies a programming language family for code analysis.
type Language string

const (
	LanguageGo   Language = "go"
	LanguagePHP  Language = "php"
	LanguageHTML Language = "html"
)

// AnalyzerManager selects analyzers based on language or workspace project type.
type AnalyzerManager struct{}

// NewAnalyzerManager creates a new analyzer manager.
func NewAnalyzerManager() *AnalyzerManager {
	return &AnalyzerManager{}
}

// normalizeProjectType maps a workspace/project type string to a Language value.
// For backward compatibility, unknown/empty types default to Go for now.
func normalizeProjectType(projectType string) Language {
	pt := strings.ToLower(strings.TrimSpace(projectType))
	switch pt {
	case "", "go", "unknown":
		return LanguageGo
	case "php", "php-laravel", "laravel":
		return LanguagePHP
	case "html", "web", "static-html":
		return LanguageHTML
	default:
		return Language(pt)
	}
}

// CodeAnalyzerForProjectType returns a PathAnalyzer appropriate for the project type.
// It returns nil when no analyzer is available for the given language.
func (m *AnalyzerManager) CodeAnalyzerForProjectType(projectType string) codetypes.PathAnalyzer {
	lang := normalizeProjectType(projectType)
	switch lang {
	case LanguageGo:
		return golang.NewCodeAnalyzer()
	case LanguagePHP:
		return laravel.NewAdapter()
	case LanguageHTML:
		return htmlanalyzer.NewCodeAnalyzer()
	default:
		return nil
	}
}

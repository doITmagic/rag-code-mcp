package internalutil

import (
	"path/filepath"
	"strings"
)

// LanguageFromPath determines the best markdown code block language for a given file path.
func LanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".php":
		return "php"
	case ".js", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py":
		return "python"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".json":
		return "json"
	case ".md":
		return "markdown"
	case ".yaml", ".yml":
		return "yaml"
	case ".sh", ".bash":
		return "bash"
	default:
		return "text"
	}
}

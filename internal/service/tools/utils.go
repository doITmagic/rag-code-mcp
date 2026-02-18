package tools

import (
	"os"
	"path/filepath"
	"strings"
)

// extractFilePathFromParams extracts file path from common parameter names.
func extractFilePathFromParams(params map[string]interface{}) string {
	pathParams := []string{
		"file_path",
		"filePath",
		"path",
		"file",
		"source_file",
		"target_file",
	}

	for _, param := range pathParams {
		if value, ok := params[param]; ok {
			if path, ok := value.(string); ok && path != "" {
				return path
			}
		}
	}

	return ""
}

// truncateString truncates s to at most max bytes.
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 0 {
		return ""
	}
	return s[:max]
}

// inferLanguageFromPath infers programming language from file extension.
func inferLanguageFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx", ".mjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".php":
		return "php"
	case ".html", ".htm":
		return "html"
	default:
		return ""
	}
}

// readFileLines reads specific lines from a file (1-indexed, inclusive).
func readFileLines(filePath string, startLine, endLine int) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(content), "\n")
	if startLine < 1 || endLine > len(lines) || startLine > endLine {
		return "", nil
	}

	selected := lines[startLine-1 : endLine]
	return strings.Join(selected, "\n"), nil
}

package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// LanguageDetector detects programming languages in a workspace
type LanguageDetector struct{}

// NewLanguageDetector creates a new language detector
func NewLanguageDetector() *LanguageDetector {
	return &LanguageDetector{}
}

// DetectLanguages scans a workspace and returns detected programming languages
// Returns a slice of language identifiers (e.g., "go", "python", "php")
func (ld *LanguageDetector) DetectLanguages(rootPath string) ([]string, error) {
	languageMap := make(map[string]bool)

	// Walk the directory tree
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		// Skip hidden directories and common exclusions
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") ||
				name == "node_modules" ||
				name == "vendor" ||
				name == "target" ||
				name == "build" ||
				name == "dist" ||
				name == "__pycache__" ||
				name == ".venv" ||
				name == "venv" {
				return filepath.SkipDir
			}
			return nil
		}

		// Detect language by file extension
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go":
			languageMap["go"] = true
		case ".py":
			languageMap["python"] = true
		case ".php":
			languageMap["php"] = true
		case ".js", ".jsx", ".mjs":
			languageMap["javascript"] = true
		case ".ts", ".tsx":
			languageMap["typescript"] = true
		case ".java":
			languageMap["java"] = true
		case ".rs":
			languageMap["rust"] = true
		case ".rb":
			languageMap["ruby"] = true
		case ".c", ".h":
			languageMap["c"] = true
		case ".cpp", ".cc", ".cxx", ".hpp":
			languageMap["cpp"] = true
		case ".cs":
			languageMap["csharp"] = true
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Convert map to sorted slice
	languages := make([]string, 0, len(languageMap))
	for lang := range languageMap {
		languages = append(languages, lang)
	}

	return languages, nil
}

// GetPrimaryLanguage returns the primary language based on project markers
// This is a heuristic-based approach for workspace-level detection
func (ld *LanguageDetector) GetPrimaryLanguage(rootPath string, markers []string) string {
	// Check for language-specific project files
	for _, marker := range markers {
		switch marker {
		case "go.mod", "go.sum":
			return "go"
		case "requirements.txt", "setup.py", "pyproject.toml", "Pipfile":
			return "python"
		case "composer.json":
			return "php"
		case "package.json":
			// Could be JS or TS, check for tsconfig.json
			if _, err := os.Stat(filepath.Join(rootPath, "tsconfig.json")); err == nil {
				return "typescript"
			}
			return "javascript"
		case "Cargo.toml":
			return "rust"
		case "pom.xml", "build.gradle":
			return "java"
		case "Gemfile":
			return "ruby"
		}
	}

	// Fallback: detect by scanning files
	languages, err := ld.DetectLanguages(rootPath)
	if err != nil || len(languages) == 0 {
		return ""
	}

	// Return first detected language
	// TODO: Could be improved with file counting heuristics
	return languages[0]
}

// LanguageFileExtensions returns the file extensions for a given language
func LanguageFileExtensions(language string) []string {
	switch strings.ToLower(language) {
	case "go":
		return []string{".go"}
	case "python":
		return []string{".py"}
	case "php":
		return []string{".php"}
	case "javascript":
		return []string{".js", ".jsx", ".mjs"}
	case "typescript":
		return []string{".ts", ".tsx"}
	case "java":
		return []string{".java"}
	case "rust":
		return []string{".rs"}
	case "ruby":
		return []string{".rb"}
	case "c":
		return []string{".c", ".h"}
	case "cpp", "c++":
		return []string{".cpp", ".cc", ".cxx", ".hpp", ".h"}
	case "csharp", "c#":
		return []string{".cs"}
	default:
		return []string{}
	}
}

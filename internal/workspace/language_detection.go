package workspace

import (
	"log"
	"os"
	"path/filepath"
	"sort"
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
	languageCounts, err := ld.DetectLanguagesWithCounts(rootPath)
	if err != nil {
		return nil, err
	}

	// Convert map to sorted slice
	languages := make([]string, 0, len(languageCounts))
	for lang := range languageCounts {
		languages = append(languages, lang)
	}

	sort.Strings(languages)
	return languages, nil
}

// DetectLanguagesWithCounts returns a map of detected languages and their file counts
func (ld *LanguageDetector) DetectLanguagesWithCounts(rootPath string) (map[string]int, error) {
	// Validate root path before scanning to prevent broad filesystem access
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("workspace: could not determine user home directory: %v", err)
	}
	if rootPath == "/" || rootPath == homeDir || rootPath == "/tmp" {
		return make(map[string]int), nil
	}

	languageCounts := make(map[string]int)

	err = filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

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
				name == "venv" ||
				name == "storage" ||
				name == "public" {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		lang := ""
		switch ext {
		case ".go":
			lang = "go"
		case ".py":
			lang = "python"
		case ".php":
			lang = "php"
		case ".js", ".jsx", ".mjs":
			lang = "javascript"
		case ".ts", ".tsx":
			lang = "typescript"
		case ".java":
			lang = "java"
		case ".rs":
			lang = "rust"
		case ".rb":
			lang = "ruby"
		case ".c", ".h":
			lang = "c"
		case ".cpp", ".cc", ".cxx", ".hpp":
			lang = "cpp"
		case ".cs":
			lang = "csharp"
		case ".vue":
			lang = "vue"
		case ".svelte":
			lang = "svelte"
		case ".sh", ".bash", ".zsh":
			lang = "shell"
		case ".sql":
			lang = "sql"
		case ".html", ".htm":
			lang = "html"
		case ".css", ".scss", ".sass", ".less":
			lang = "css"
		case ".md", ".markdown":
			lang = "markdown"
		case ".yaml", ".yml":
			lang = "yaml"
		case ".json":
			lang = "json"
		case ".toml":
			lang = "toml"
		}

		if lang != "" {
			languageCounts[lang]++
		}

		return nil
	})

	return languageCounts, err
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

	// Fallback: detect by scanning files and picking the one with the highest count
	counts, err := ld.DetectLanguagesWithCounts(rootPath)
	if err != nil || len(counts) == 0 {
		return ""
	}

	maxCount := -1
	primary := ""
	for lang, count := range counts {
		if count > maxCount {
			maxCount = count
			primary = lang
		}
	}

	return primary
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
	case "vue":
		return []string{".vue"}
	case "svelte":
		return []string{".svelte"}
	case "shell", "sh", "bash":
		return []string{".sh", ".bash", ".zsh"}
	case "sql":
		return []string{".sql"}
	case "html":
		return []string{".html", ".htm"}
	case "css":
		return []string{".css", ".scss", ".sass", ".less"}
	case "markdown":
		return []string{".md", ".markdown"}
	case "yaml":
		return []string{".yaml", ".yml"}
	case "json":
		return []string{".json"}
	case "toml":
		return []string{".toml"}
	default:
		return []string{}
	}
}

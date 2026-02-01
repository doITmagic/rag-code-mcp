package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Detector detects workspace roots from file paths
type Detector struct {
	// Markers to identify workspace root, in priority order
	markers []string

	// ExcludePatterns are path patterns to exclude from workspace detection
	excludePatterns []string
}

// NewDetector creates a new workspace detector with default markers
func NewDetector() *Detector {
	return &Detector{
		markers: []string{
			".git",           // Git repository (highest priority)
			"go.mod",         // Go project
			"composer.json",  // PHP/Laravel project
			"artisan",        // Laravel project (specific)
			"package.json",   // Node.js project
			"Cargo.toml",     // Rust project
			"pyproject.toml", // Python project (PEP 518)
			"setup.py",       // Python project (legacy)
			"pom.xml",        // Maven project (Java)
			"build.gradle",   // Gradle project (Java/Kotlin)
			".project",       // Generic project marker
			".vscode",        // VS Code workspace
		},
		excludePatterns: []string{
			// Only exclude common cache/temp directories in home or system paths
			// Don't exclude test temp directories
			"/.cache/",
			"/node_modules/",
			"/vendor/",
		},
	}
}

// NewDetectorWithConfig creates a detector with configuration
func NewDetectorWithConfig(markers []string, excludePatterns []string) *Detector {
	d := NewDetector()
	if len(markers) > 0 {
		d.markers = markers
	}
	if len(excludePatterns) > 0 {
		d.excludePatterns = excludePatterns
	}
	return d
}

// SetMarkers allows customizing workspace markers
func (d *Detector) SetMarkers(markers []string) {
	d.markers = markers
}

// SetExcludePatterns sets path patterns to exclude
func (d *Detector) SetExcludePatterns(patterns []string) {
	d.excludePatterns = patterns
}

// DetectFromPath detects workspace from a file path
func (d *Detector) DetectFromPath(filePath string) (*Info, error) {
	// Normalize to absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Check if path should be excluded
	if d.shouldExclude(absPath) {
		return nil, fmt.Errorf("path matches exclusion pattern: %s", absPath)
	}

	// Start from file's directory
	current := absPath
	if !isDir(absPath) {
		current = filepath.Dir(absPath)
	}

	// Walk up directory tree looking for workspace markers
	for {
		// Check for workspace markers
		foundMarkers, projectType, languages := d.findMarkers(current)
		if len(foundMarkers) > 0 {
			// Found workspace root
			return &Info{
				Root:        current,
				ID:          generateWorkspaceID(current),
				ProjectType: projectType,
				Languages:   languages,
				Markers:     foundMarkers,
				DetectedAt:  time.Now(),
			}, nil
		}

		// Move to parent directory
		parent := filepath.Dir(current)
		if parent == current {
			// Reached filesystem root without finding markers
			break
		}
		current = parent
	}

	// No markers found - use file's directory as fallback
	fallbackDir := filepath.Dir(absPath)

	// Validate fallback directory - reject suspicious workspace roots
	homeDir, _ := os.UserHomeDir()
	if fallbackDir == "/" || fallbackDir == homeDir || strings.HasPrefix(fallbackDir, "/tmp") {
		return nil, fmt.Errorf(
			"could not detect workspace for file '%s'.\n\n"+
				"The file appears to be outside any project directory.\n"+
				"Please ensure the file is inside a project with workspace markers like:\n"+
				"  - .git (Git repository)\n"+
				"  - go.mod (Go project)\n"+
				"  - composer.json (PHP project)\n"+
				"  - package.json (Node.js project)\n"+
				"  - pyproject.toml (Python project)\n\n"+
				"Detected fallback directory: %s",
			absPath, fallbackDir,
		)
	}

	return &Info{
		Root:        fallbackDir,
		ID:          generateWorkspaceID(fallbackDir),
		ProjectType: "unknown",
		Languages:   []string{},
		Markers:     []string{},
		DetectedAt:  time.Now(),
	}, nil
}

// DetectFromParams detects workspace from MCP tool parameters
// Looks for file paths in common parameter names
func (d *Detector) DetectFromParams(params map[string]interface{}) (*Info, error) {
	// Common parameter names that contain file paths
	pathParams := []string{
		"file_path",
		"filePath",
		"path",
		"file",
		"source_file",
		"target_file",
		"directory",
		"dir",
	}

	// Try to find a file path in parameters
	for _, param := range pathParams {
		if value, ok := params[param]; ok {
			if path, ok := value.(string); ok && path != "" {
				return d.DetectFromPath(path)
			}
		}
	}

	// Fallback: use current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("no file path in params and failed to get cwd: %w", err)
	}

	return d.DetectFromPath(cwd)
}

// findMarkers checks for workspace markers in a directory
// Returns found markers, detected project type, and list of languages
func (d *Detector) findMarkers(dir string) ([]string, string, []string) {
	var found []string
	var languages []string
	languageMap := make(map[string]bool) // Deduplicate languages
	projectType := "unknown"

	for _, marker := range d.markers {
		markerPath := filepath.Join(dir, marker)
		if exists(markerPath) {
			found = append(found, marker)

			// Determine project type from first marker
			if projectType == "unknown" {
				projectType = inferProjectType(marker)
			}

			// Collect all detected languages
			lang := inferLanguageFromMarker(marker)
			if lang != "" && !languageMap[lang] {
				languageMap[lang] = true
				languages = append(languages, lang)
			}
		}
	}

	return found, projectType, languages
}

// shouldExclude checks if path matches any exclusion pattern
func (d *Detector) shouldExclude(path string) bool {
	for _, pattern := range d.excludePatterns {
		if strings.Contains(path, pattern) {
			return true
		}
	}
	return false
}

// generateWorkspaceID creates a stable, unique ID from workspace root path
func generateWorkspaceID(rootPath string) string {
	// Use SHA256 hash of absolute path
	h := sha256.Sum256([]byte(rootPath))
	// Return first 12 characters of hex for readability
	return hex.EncodeToString(h[:])[:12]
}

// inferProjectType determines project type from marker
func inferProjectType(marker string) string {
	switch marker {
	case "go.mod":
		return "go"
	case "artisan":
		return "laravel"
	case "composer.json":
		return "php"
	case "index.html", "index.htm", "package-lock.json", "vite.config.js", "vite.config.ts":
		return "html"
	case "package.json":
		return "nodejs"
	case "Cargo.toml":
		return "rust"
	case "pyproject.toml", "setup.py":
		return "python"
	case "pom.xml":
		return "maven"
	case "build.gradle":
		return "gradle"
	case ".git":
		return "git"
	default:
		return "unknown"
	}
}

// inferLanguageFromMarker determines programming language from marker
// Returns normalized language name for collection naming
func inferLanguageFromMarker(marker string) string {
	switch marker {
	case "go.mod":
		return "go"
	case "package.json":
		return "javascript" // or "nodejs"
	case "Cargo.toml":
		return "rust"
	case "pyproject.toml", "setup.py", "requirements.txt":
		return "python"
	case "composer.json":
		return "php"
	case "pom.xml", "build.gradle":
		return "java"
	case "Gemfile":
		return "ruby"
	case "Package.swift":
		return "swift"
	default:
		return ""
	}
}

// Helper functions

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

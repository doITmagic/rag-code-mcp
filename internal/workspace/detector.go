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

	// AllowedPaths restricts workspace detection to specific directories
	// If set, only paths within these directories are allowed
	allowedPaths []string

	// DisableUpwardSearch when true, disables searching parent directories
	disableUpwardSearch bool
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
func NewDetectorWithConfig(markers []string, excludePatterns []string, allowedPaths []string, disableUpwardSearch bool) *Detector {
	d := NewDetector()
	if len(markers) > 0 {
		d.markers = markers
	}
	if len(excludePatterns) > 0 {
		d.excludePatterns = excludePatterns
	}
	d.allowedPaths = allowedPaths
	d.disableUpwardSearch = disableUpwardSearch
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

// SetAllowedPaths sets allowed workspace paths
func (d *Detector) SetAllowedPaths(paths []string) {
	d.allowedPaths = paths
}

// SetDisableUpwardSearch sets whether to disable upward directory search
func (d *Detector) SetDisableUpwardSearch(disable bool) {
	d.disableUpwardSearch = disable
}

// DetectFromPath detects workspace from a file path
func (d *Detector) DetectFromPath(filePath string) (*Info, error) {
	// Normalize to absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	homeDir, _ := os.UserHomeDir()
	
	// Early validation: Only reject filesystem root and bare /tmp
	// DO NOT reject Home directory here - it might contain valid projects!
	startDir := absPath
	if !isDir(absPath) {
		startDir = filepath.Dir(absPath)
	}
	
	// Validate against allowed paths if configured
	if len(d.allowedPaths) > 0 {
		allowed := false
		for _, allowedPath := range d.allowedPaths {
			// Normalize allowed path
			absAllowed, err := filepath.Abs(allowedPath)
			if err != nil {
				continue
			}
			// Check if startDir is within allowed path
			if startDir == absAllowed || strings.HasPrefix(startDir, absAllowed+string(filepath.Separator)) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf(
				"path '%s' is not within allowed workspace paths.\n\n"+
					"Configured allowed paths:\n"+
					"  %s\n\n"+
					"To use this path, add it to 'workspace.allowed_workspace_paths' in your config.",
				startDir, strings.Join(d.allowedPaths, "\n  "),
			)
		}
	}
	
	// Reject filesystem root - never valid as a project
	if startDir == "/" {
		return nil, fmt.Errorf(
			"cannot use '%s' as workspace.\n\n"+
				"For security reasons, the tool cannot operate on the filesystem root.\n"+
				"Please provide a file path inside a project directory with workspace markers.",
			startDir,
		)
	}
	
	// Reject bare /tmp directory (but allow subdirectories for testing)
	if startDir == "/tmp" {
		return nil, fmt.Errorf(
			"cannot use '%s' as workspace.\n\n"+
				"For security reasons, the tool cannot operate on the /tmp directory directly.\n"+
				"Please provide a file path inside a valid project directory.",
			startDir,
		)
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

	// If upward search is disabled, only check current directory
	if d.disableUpwardSearch {
		foundMarkers, projectType, languages := d.findMarkers(current)
		if len(foundMarkers) > 0 {
			// Found workspace root in current directory
			return &Info{
				Root:        current,
				ID:          generateWorkspaceID(current),
				ProjectType: projectType,
				Languages:   languages,
				Markers:     foundMarkers,
				DetectedAt:  time.Now(),
			}, nil
		}
		// No markers in current directory and upward search is disabled
		return nil, fmt.Errorf(
			"no workspace markers found in '%s'.\n\n"+
				"Upward directory search is disabled (workspace.disable_upward_search = true).\n"+
				"Please ensure workspace markers exist in the current directory, or enable upward search.",
			current,
		)
	}

	// Walk up directory tree looking for workspace markers
	// Stop at Home directory to prevent scanning beyond user's projects
	// Also limit traversal depth to prevent excessive walking
	maxDepth := 10 // Maximum number of parent directories to check
	depth := 0
	for depth < maxDepth {
		depth++
		
		// Stop if we've reached Home directory - don't scan beyond it
		if current == homeDir {
			break
		}
		
		// Check for workspace markers
		foundMarkers, projectType, languages := d.findMarkers(current)
		if len(foundMarkers) > 0 {
			// Found workspace root - validate it's in allowed paths if configured
			if len(d.allowedPaths) > 0 {
				allowed := false
				for _, allowedPath := range d.allowedPaths {
					absAllowed, err := filepath.Abs(allowedPath)
					if err != nil {
						continue
					}
					if current == absAllowed || strings.HasPrefix(current, absAllowed+string(filepath.Separator)) {
						allowed = true
						break
					}
				}
				if !allowed {
					// Found markers but outside allowed paths, continue searching
					parent := filepath.Dir(current)
					if parent == current {
						break
					}
					current = parent
					continue
				}
			}
			
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

	// No markers found - this is a security issue
	// We should NOT use fallback directories without workspace markers
	// This prevents the tool from accidentally scanning large directory trees
	
	// If we've searched up to 10 levels and found no markers, reject the request
	return nil, fmt.Errorf(
		"could not detect workspace for file '%s'.\n\n"+
			"No workspace markers found in any parent directory (searched up to %d levels).\n"+
			"For security reasons, the tool requires explicit workspace markers to prevent "+
			"accidentally scanning large directory trees.\n\n"+
			"Please ensure the file is inside a project with workspace markers like:\n"+
			"  - .git (Git repository)\n"+
			"  - go.mod (Go project)\n"+
			"  - composer.json (PHP project)\n"+
			"  - package.json (Node.js project)\n"+
			"  - pyproject.toml (Python project)\n\n"+
			"If this is a new project, initialize it with one of these markers:\n"+
			"  $ git init          # Creates .git directory\n"+
			"  $ go mod init name  # Creates go.mod file\n"+
			"  $ npm init          # Creates package.json file",
		absPath, maxDepth,
	)
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

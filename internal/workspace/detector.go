package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
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

// isPathAllowed checks if a path is within the allowed workspace paths
func (d *Detector) isPathAllowed(path string) bool {
	if len(d.allowedPaths) == 0 {
		return true
	}
	return isWithinAllowedPaths(path, d.allowedPaths)
}

// isWithinAllowedPaths helper to check if path is within allowed list
func isWithinAllowedPaths(path string, allowedPaths []string) bool {
	// Normalize path: handle relative paths and evaluate symlinks for security
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	evalPath, err := filepath.EvalSymlinks(absPath)
	if err == nil {
		absPath = evalPath
	}

	for _, allowedPath := range allowedPaths {
		// Normalize allowed path
		absAllowed, err := filepath.Abs(allowedPath)
		if err != nil {
			continue
		}
		evalAllowed, err := filepath.EvalSymlinks(absAllowed)
		if err == nil {
			absAllowed = evalAllowed
		}

		// Check if absPath is within absAllowed
		if absPath == absAllowed || strings.HasPrefix(absPath, absAllowed+string(filepath.Separator)) {
			return true
		}
	}

	return false
}

// isInvalidRoot checks if the path is a filesystem root or other restricted directory
func isInvalidRoot(path string) bool {
	// Using generic filepath.Dir(path) == path to detect root on any OS
	if path != "" && filepath.Dir(path) == path {
		return true
	}

	// Check path against os.TempDir for platform agnostic temp dir check
	// Also explicitly check /tmp for unix systems just in case
	sysTemp := os.TempDir()
	if sysTemp != "" && (path == sysTemp || strings.TrimSuffix(path, string(filepath.Separator)) == strings.TrimSuffix(sysTemp, string(filepath.Separator))) {
		return true
	}
	if runtime.GOOS != "windows" && (path == "/tmp" || path == "/var/tmp") {
		return true
	}

	// Check for system directories boundary - specifically Home
	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" && path == homeDir {
		return true
	} else if err != nil {
		// If we can't get home dir, verify we are not erroring out
		// We'll just log it lightly if needed, but here we can't block what we don't know
	}

	return false
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

	// Check home dir and handle error properly
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Not fatal, but we can't protect home dir if we don't know it
		log.Printf("[WARN] workspace: could not determine user home directory: %v", err)
	}

	// Early validation
	startDir := absPath
	if !isDir(absPath) {
		startDir = filepath.Dir(absPath)
	}

	// Validate against allowed paths if configured
	if !d.isPathAllowed(startDir) {
		return nil, fmt.Errorf(
			"path '%s' is not within allowed workspace paths.\n\n"+
				"Configured allowed paths:\n"+
				"  %s\n\n"+
				"Safety: Only paths within these directories are accepted.\n"+
				"Update your IDE MCP configuration to include this project path.",
			startDir, strings.Join(d.allowedPaths, ", "))
	}

	// Reject invalid roots (/, /tmp, home bare)
	if isInvalidRoot(startDir) {
		return nil, fmt.Errorf(
			"cannot use '%s' as workspace root.\n\n"+
				"For security reasons, the tool cannot operate on the filesystem root or other restricted directories.\n"+
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
	maxDepth := 10 // Maximum number of parent directories to check
	depth := 0
	for depth < maxDepth {
		depth++

		// Stop if we've reached Home directory or other invalid root
		if current == homeDir || isInvalidRoot(current) {
			break
		}

		// Check for workspace markers
		foundMarkers, projectType, languages := d.findMarkers(current)
		if len(foundMarkers) > 0 {
			// Found markers - validate it's in allowed paths if configured
			// Found markers - validate it's in allowed paths if configured
			if len(d.allowedPaths) > 0 && !isWithinAllowedPaths(current, d.allowedPaths) {
				// Found markers but outside allowed paths, continue searching
				parent := filepath.Dir(current)
				if parent == current {
					break
				}
				current = parent
				continue
			}

			// Found valid workspace root
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

	// No markers found
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
func (d *Detector) DetectFromParams(params map[string]interface{}) (*Info, error) {
	// Priority 1: Check for explicit workspace_root parameter
	if workspaceRoot, ok := params["workspace_root"]; ok {
		if rootPath, ok := workspaceRoot.(string); ok && rootPath != "" {
			return d.DetectFromPath(rootPath)
		}
	}

	// Priority 2: Extract file path from standard parameters
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

	// Fallback: use current working directory of the server
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("no file path in params and failed to get cwd: %w", err)
	}

	return d.DetectFromPath(cwd)
}

// findMarkers checks for workspace markers in a directory
func (d *Detector) findMarkers(dir string) ([]string, string, []string) {
	var found []string
	var languages []string
	languageMap := make(map[string]bool)
	projectType := "unknown"

	for _, marker := range d.markers {
		markerPath := filepath.Join(dir, marker)
		if exists(markerPath) {
			found = append(found, marker)

			if projectType == "unknown" {
				projectType = inferProjectType(marker)
			}

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

// generateWorkspaceID creates a stable, unique ID
func generateWorkspaceID(rootPath string) string {
	h := sha256.Sum256([]byte(rootPath))
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
func inferLanguageFromMarker(marker string) string {
	switch marker {
	case "go.mod":
		return "go"
	case "package.json":
		return "javascript"
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
